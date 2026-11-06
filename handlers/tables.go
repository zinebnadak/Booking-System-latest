package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

// The table buttons and the "3 meals" weekend deal.
//
//   - POST /api/press      a table's button was pressed (the button sends its secret code)
//   - POST /api/checkin    a button reporting once a day that it's alive, with its battery
//   - /admin/tables        the live Tables screen for staff, with sound
//   - /owner/buttons       the buttons' secret codes, battery and last signal (head manager)
//   - /demo/buttons        on-screen buttons that act like the real ones (demo mode only)

// ---------- Live updates ----------

// hub tells every open Tables screen that something changed, the moment it
// happens (Server-Sent Events: one long-lived request per screen).
type hub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
	ver  uint64
	done chan struct{} // closed when the server stops
	once sync.Once
}

func newHub() *hub { return &hub{subs: map[chan struct{}]struct{}{}, done: make(chan struct{})} }

func (h *hub) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// publish wakes every screen. A screen that hasn't caught up yet already has
// a wake-up waiting, so nothing ever blocks here.
func (h *hub) publish() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ver++
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (h *hub) version() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ver
}

// StopStreams ends the live connections so the server can shut down quickly.
func (s *Server) StopStreams() { s.live.once.Do(func() { close(s.live.done) }) }

// tablesEvents keeps a connection open and sends "change" whenever a table
// changes, plus a small ping every 25 seconds so proxies don't hang up.
func (s *Server) tablesEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // this request is meant to stay open
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // tell nginx-style proxies not to hold events back
	ch, cancel := s.live.subscribe()
	defer cancel()
	fmt.Fprintf(w, "retry: 3000\nevent: change\ndata: %d\n\n", s.live.version())
	if rc.Flush() != nil {
		return
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.live.done:
			return
		case <-ch:
			fmt.Fprintf(w, "event: change\ndata: %d\n\n", s.live.version())
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		if rc.Flush() != nil {
			return
		}
	}
}

// ---------- The buttons ----------

// limitButton: wrong button codes from one address before it only gets
// "try later" for a while.
var limitButton = limit{n: 20, window: 10 * time.Minute}

// buttonCredentials reads the secret code and battery level a button sends:
// the code in an "Authorization: Bearer <code>" or "X-Button-Token" header
// (or a "token" field in the body), the battery as "battery" (0–100) in the
// body or an "X-Battery" header. Codes in the address are ignored on
// purpose: addresses end up in logs.
func buttonCredentials(r *http.Request) (token string, battery int) {
	if a := r.Header.Get("Authorization"); len(a) > 7 && strings.EqualFold(a[:7], "Bearer ") {
		token = strings.TrimSpace(a[7:])
	}
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-Button-Token"))
	}
	battery, _ = strconv.Atoi(r.Header.Get("X-Battery"))
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct == "application/json" {
		var body struct {
			Token   string `json:"token"`
			Battery int    `json:"battery"`
		}
		if json.NewDecoder(r.Body).Decode(&body) == nil {
			if token == "" {
				token = strings.TrimSpace(body.Token)
			}
			if body.Battery != 0 {
				battery = body.Battery
			}
		}
	} else if r.ParseForm() == nil {
		if token == "" {
			token = strings.TrimSpace(r.PostForm.Get("token"))
		}
		if b, err := strconv.Atoi(r.PostForm.Get("battery")); err == nil {
			battery = b
		}
	}
	if battery < 0 || battery > 100 {
		battery = 0
	}
	return token, battery
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// apiButton handles a press (call=true) or a daily check-in from a button.
func (s *Server) apiButton(call bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := s.Now()
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		token, battery := buttonCredentials(r)
		// A right code always works. Wrong codes are counted per address, and
		// after too many that address only gets "try later". (Guessing a code
		// is hopeless anyway; this keeps scripts from filling the logs. And a
		// guest sending wrong codes from the restaurant's Wi-Fi can't block
		// the real buttons, which share that address.)
		key := "button|" + s.clientIP(r)
		var res db.PressResult
		var err error
		if call {
			res, err = s.Store.Press(token, battery, now)
		} else {
			res.Table, err = s.Store.ButtonCheckIn(token, battery, now)
		}
		switch {
		case errors.Is(err, db.ErrNoButton):
			if !s.rl.allow(key, limitButton, now) {
				writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "too many wrong codes, try later"})
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unknown button code"})
			return
		case err != nil:
			log.Printf("button: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "could not save"})
			return
		}
		s.live.publish()
		if !call {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "table": res.Table})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "table": res.Table, "kind": res.Kind, "meal": res.Meal, "again": res.Again})
	}
}

// ---------- The Tables screen ----------

// tableTile is one table on the screen.
type tableTile struct {
	No        int
	Room      string
	State     string   // "free", "eating", "next" (ready for the next meal), "last" (all meals done), "waiter"
	Meal      int      // meal on the table now
	Next      int      // the meal to bring next
	Dots      []bool   // one per meal in the deal: true = served
	Since     string   // "18:20": when the current step began
	Minutes   int      // minutes since then
	Presses   int      // presses while waiting
	CallKey   string   // changes with every press, so the screen knows when to beep
	Who       string   // seated guests from a booking, e.g. "Andersson · 4 guests"
	BillCents int      // what the table's open bill comes to so far (0 = no bill)
	Caller    string   // called from a guest's phone: "Guest 2 · Anna"
	Dishes    []string // 3 meals: what each meal is, in this table's order
	Guests    int      // 3 meals: how many guests have it
	NextDish  string   // what to bring next
	Sent      bool     // 3 meals: the next meal is with the kitchen
}

// Waiting reports whether the table needs someone now.
func (t tableTile) Waiting() bool {
	return t.State == "next" || t.State == "last" || t.State == "waiter"
}

func minutesSince(t, now time.Time) int {
	if now.Before(t) {
		return 0
	}
	return int(now.Sub(t) / time.Minute)
}

// tableTiles builds the screen's tiles from the store.
func (s *Server) tableTiles(lang string, now time.Time) []tableTile {
	who := s.seatedNow(lang, now)
	bills := map[int]int{}
	for _, b := range s.Store.OpenBills() {
		bills[b.Table] = b.Total()
	}
	var out []tableTile
	for _, t := range s.Store.TablesNow() {
		tile := tableTile{No: t.No, Room: t.Room, State: "free", Who: who[t.No], BillCents: bills[t.No]}
		if run := t.Run; run != nil {
			tile.Meal = run.Meal()
			tile.Next = run.Meal() + 1
			tile.Dishes, tile.Guests, tile.NextDish = run.Dishes, run.Guests, run.Dish(run.Meal()+1)
			tile.Sent = s.Store.MealSent(t.No)
			for i := 1; i <= schedule.MealsInDeal; i++ {
				tile.Dots = append(tile.Dots, i <= run.Meal())
			}
			since := run.Since()
			tile.Since = since.In(schedule.Location).Format("15:04")
			tile.Minutes = minutesSince(since, now)
			tile.State = "eating"
			if run.Waiting() {
				tile.State = "next"
				if run.LastMeal() {
					tile.State = "last"
				}
			}
		}
		if c := t.Call; c != nil {
			if t.Run == nil || (c.Waiter && tile.State == "eating") {
				tile.State = "waiter"
				tile.Since = c.At.In(schedule.Location).Format("15:04")
				tile.Minutes = minutesSince(c.At, now)
			}
			tile.Presses = c.Presses
			if c.Guest > 0 {
				tile.Caller = guestLabel(lang, db.Share{Guest: c.Guest, Name: c.Name})
			}
			tile.CallKey = fmt.Sprintf("%d-%d-%d", t.No, c.At.Unix(), c.Presses)
		}
		out = append(out, tile)
	}
	return out
}

// seatedNow: who is at each table right now, from today's checked-in bookings.
func (s *Server) seatedNow(lang string, now time.Time) map[int]string {
	out := map[int]string{}
	for _, b := range s.Store.BookingsOn(schedule.FormatDate(schedule.Today(now)), now) {
		if b.Kind != db.KindTable || b.TableNo == 0 || b.EffectiveStatus(now) != db.StatusArrived {
			continue
		}
		start := b.Start()
		if now.Before(start) || !now.Before(start.Add(b.Duration())) {
			continue
		}
		out[b.TableNo] = b.Name + " · " + guestsCount(lang, b.PartySize)
	}
	return out
}

// buttonWarning is a button that needs charging or seems to be dead.
type buttonWarning struct {
	Table   int
	Battery int  // low battery, in percent
	Silent  int  // days without a signal (the button checks in every day)
	Low     bool // true: battery warning, false: silent warning
}

// Battery and silence limits for the warnings on the Tables screen.
const (
	lowBattery = 20
	silentDays = 2
)

func (s *Server) buttonWarnings(now time.Time) []buttonWarning {
	var out []buttonWarning
	for _, b := range s.Store.Buttons() {
		switch {
		case b.LastSeen == nil: // never set up: nothing to warn about yet
		case now.Sub(*b.LastSeen) > silentDays*24*time.Hour:
			out = append(out, buttonWarning{Table: b.Table, Silent: int(now.Sub(*b.LastSeen) / (24 * time.Hour))})
		case b.Battery > 0 && b.Battery <= lowBattery:
			out = append(out, buttonWarning{Table: b.Table, Battery: b.Battery, Low: true})
		}
	}
	return out
}

// dealDaysText: "Saturday and Sunday" / "lördag och söndag".
func dealDaysText(lang string) string {
	var days []string
	for _, wd := range []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday} {
		if schedule.MealDealDays[wd] {
			days = append(days, weekdayLong(lang, wd))
		}
	}
	and := " and "
	if lang == langSV {
		and = " och "
	}
	switch len(days) {
	case 0:
		return ""
	case 1:
		return days[0]
	}
	return strings.Join(days[:len(days)-1], ", ") + and + days[len(days)-1]
}

func roundMinutes(d time.Duration) int { return int((d + 30*time.Second) / time.Minute) }

func (s *Server) tablesData(r *http.Request) M {
	lang := langOf(r)
	now := s.Now()
	today := schedule.Today(now)
	tiles := s.tableTiles(lang, now)
	var alerts []tableTile
	for _, t := range tiles {
		if t.Waiting() {
			alerts = append(alerts, t)
		}
	}
	sort.SliceStable(alerts, func(i, j int) bool { return alerts[i].Minutes > alerts[j].Minutes })
	stats := s.Store.DealStatsOn(schedule.FormatDate(today), now)
	return M{
		"Admin": true, "Nav": "tables", "Title": st(lang, "Tables"),
		"Tiles": tiles, "Alerts": alerts, "MealsInDeal": schedule.MealsInDeal,
		"Rooms":     byRoom(tiles, func(t tableTile) string { return t.Room }, tableTile.Waiting),
		"DealToday": schedule.MealDealToday(today), "DealDays": dealDaysText(lang),
		"Stats": stats, "EatMin": roundMinutes(stats.Eat), "WaitMin": roundMinutes(stats.Wait),
		"Warnings": s.buttonWarnings(now), "Toast": flashText(r), "Stock": s.stockStrip(),
		"OnlinePaid": s.onlinePaid(lang, now), "ReadyFood": s.readyFood(now),
	}
}

// adminTables: the Tables screen is now part of Orders (3 meals, calls and
// bookings are on each table there). Old links and home-screen icons land on Orders.
func (s *Server) adminTables(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/orders", http.StatusSeeOther)
}

// tableAction is a staff tap on a table: start the deal, a meal served,
// table cleared, "not yet", or "done" for a waiter call.
func (s *Server) tableAction(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	now := s.Now()
	no := atoiDefault(r.PathValue("no"), 0)
	var err error
	var msg string
	switch r.PathValue("action") {
	case "start":
		if !schedule.MealDealToday(schedule.Today(now)) && r.FormValue("anyway") != "1" {
			err = errNotDealDay
		} else {
			err, msg = s.startDeal(r, no, now), "dealstarted"
		}
	case "serve":
		err, msg = s.Store.ServeNext(no, atoiDefault(r.FormValue("meal"), 0), now), "mealserved"
	case "send":
		err, msg = s.Store.SendNextMeal(no, now), "mealsent"
	case "end":
		err, msg = s.Store.EndDeal(no, now), "tablecleared"
	case "notyet":
		err, msg = s.Store.NotYet(no), "notyet"
	case "answer":
		err, msg = s.Store.Answer(no), "answered"
	default:
		s.notFound(w, r)
		return
	}
	if err == nil {
		s.live.publish()
	} else {
		if !errors.Is(err, errNotDealDay) && !errors.Is(err, db.ErrStale) && !errors.Is(err, db.ErrNoDeal) &&
			!errors.Is(err, db.ErrDealRunning) && !errors.Is(err, db.ErrLastMeal) && !errors.Is(err, db.ErrNoTable) {
			log.Printf("table %d: %v", no, err)
		}
		msg = "tablestale"
		if errors.Is(err, db.ErrPlanName) || errors.Is(err, db.ErrPlanDishes) || errors.Is(err, db.ErrBadGuests) {
			msg = "dealbad"
		}
	}
	// The screen's script sends X-Live and redraws the tables itself.
	if r.Header.Get("X-Live") == "1" {
		if err != nil {
			http.Error(w, st(langOf(r), flashes[msg]), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	back := "/admin/orders"
	if r.FormValue("back") == "table" && validTableNo(no) {
		back = orderPath(no) // tapped on the table's own page
	}
	redirectMsg(w, r, back, nil, msg)
}

func validTableNo(no int) bool {
	for _, t := range schedule.TableNos() {
		if t == no {
			return true
		}
	}
	return false
}

// bookedLater: today's table bookings that haven't arrived yet, per table,
// soonest first ("19:00 · Andersson · 4 guests"). A booking stays here until
// it's checked in or becomes a no-show.
func (s *Server) bookedLater(lang string, now time.Time) map[int][]string {
	out := map[int][]string{}
	bookings := s.Store.BookingsOn(schedule.FormatDate(schedule.Today(now)), now)
	sort.SliceStable(bookings, func(i, j int) bool { return bookings[i].Start().Before(bookings[j].Start()) })
	for _, b := range bookings {
		if b.Kind != db.KindTable || b.TableNo == 0 || b.EffectiveStatus(now) != db.StatusPaid {
			continue
		}
		out[b.TableNo] = append(out[b.TableNo], clockOf(b.Start())+" · "+b.Name+" · "+guestsCount(lang, b.PartySize))
	}
	return out
}

var errNotDealDay = errors.New("the deal doesn't run today")

// ---------- Button codes (head manager) ----------

func (s *Server) ownerButtons(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	type row struct {
		db.Button
		Seen   string
		Short  string
		Silent bool
	}
	var rows []row
	for _, b := range s.Store.Buttons() {
		x := row{Button: b, Short: b.Token[len(b.Token)-4:]}
		if b.LastSeen != nil {
			x.Seen = shortDateL(lang, schedule.FormatDate(*b.LastSeen)) + " " + b.LastSeen.In(schedule.Location).Format("15:04")
			x.Silent = now.Sub(*b.LastSeen) > silentDays*24*time.Hour
		}
		rows = append(rows, x)
	}
	s.render(w, r, "owner_buttons", http.StatusOK, M{
		"Admin": true, "Nav": "buttons", "Title": st(lang, "Table buttons"),
		"Rows": rows, "PressURL": s.Cfg.BaseURL + "/api/press", "CheckinURL": s.Cfg.BaseURL + "/api/checkin",
		"Toast": flashText(r), "Shown": atoiDefault(r.URL.Query().Get("show"), 0),
	})
}

func (s *Server) ownerNewCode(w http.ResponseWriter, r *http.Request) {
	no := atoiDefault(r.PathValue("no"), 0)
	if _, err := s.Store.NewButtonCode(no); err != nil {
		s.notFound(w, r)
		return
	}
	s.live.publish()
	redirectMsg(w, r, "/owner/buttons", url.Values{"show": {strconv.Itoa(no)}}, "newcode")
}

// ---------- On-screen test buttons (demo mode only) ----------

func (s *Server) demoButtons(w http.ResponseWriter, r *http.Request) {
	if s.Pay.Enabled() {
		s.notFound(w, r)
		return
	}
	lang := langOf(r)
	pressed := atoiDefault(r.URL.Query().Get("t"), 0)
	result := ""
	switch r.URL.Query().Get("r") {
	case db.PressNext:
		result = stf(lang, "Table %d: staff see that you're ready for the next meal.", pressed)
	case db.PressLast:
		result = stf(lang, "Table %d: staff see that you've finished all the meals.", pressed)
	case db.PressWaiter:
		result = stf(lang, "Table %d: staff see that you'd like a waiter.", pressed)
	case "again":
		result = stf(lang, "Table %d: staff already know. Pressing again shows you're still waiting.", pressed)
	}
	s.render(w, r, "demo_buttons", http.StatusOK, M{
		"Admin": true, "Bare": true, "Title": st(lang, "Test buttons"),
		"Tables": schedule.TableNos(), "Pressed": pressed, "Result": result,
	})
}

func (s *Server) demoPress(w http.ResponseWriter, r *http.Request) {
	if s.Pay.Enabled() {
		s.notFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	no := atoiDefault(r.PathValue("no"), 0)
	res, err := s.Store.PressTable(no, s.Now())
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.live.publish()
	kind := res.Kind
	if res.Again {
		kind = "again"
	}
	http.Redirect(w, r, "/demo/buttons?"+url.Values{"t": {strconv.Itoa(no)}, "r": {kind}}.Encode(), http.StatusSeeOther)
}

// startDeal starts 3 meals from the table's page: the meals in the order the
// guests want (typed, or a saved order), and the price per guest on the bill.
func (s *Server) startDeal(r *http.Request, no int, now time.Time) error {
	var dishes []string
	for i := 1; i <= schedule.MealsInDeal; i++ {
		dishes = append(dishes, r.FormValue("dish"+strconv.Itoa(i)))
	}
	if id := atoiDefault(r.FormValue("plan"), 0); id > 0 {
		for _, p := range s.Store.MealPlans() {
			if p.ID == id {
				dishes = p.Dishes
			}
		}
	}
	guests := atoiDefault(r.FormValue("guests"), 1)
	if r.FormValue("save") == "1" {
		if p, err := s.Store.SaveMealPlan(r.FormValue("plan_name"), dishes); err == nil {
			s.logActivity(r, no, "saved a 3 meals order", p.Name+": "+strings.Join(p.Dishes, ", "))
		} else {
			return err
		}
	}
	ref, deposit := "", 0
	if _, open := s.Store.OpenBill(no); !open {
		ref, deposit = s.bookingAtTable(no, now)
	}
	b, err := s.Store.StartDealWith(no, dishes, guests, ref, deposit, now)
	if err == nil {
		s.logActivity(r, no, "started 3 meals", fmt.Sprintf("%d guests", guests))
		s.claimTable(r, b.ID)
	}
	return err
}

// mealPlanRemove deletes a saved 3 meals order.
func (s *Server) mealPlanRemove(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	if err := s.Store.RemoveMealPlan(atoiDefault(r.PathValue("id"), 0)); err == nil {
		s.logActivity(r, 0, "removed a 3 meals order", "")
	}
	back := "/admin/orders"
	if no := atoiDefault(r.FormValue("table"), 0); validTableNo(no) {
		back = orderPath(no)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// partyAt: how many guests the checked-in booking at a table has (2 if none).
func (s *Server) partyAt(table int, now time.Time) int {
	for _, b := range s.Store.BookingsOn(schedule.FormatDate(schedule.Today(now)), now) {
		if b.Kind == db.KindTable && b.TableNo == table && b.EffectiveStatus(now) == db.StatusArrived && b.PartySize > 0 {
			return b.PartySize
		}
	}
	return 2
}

// mealNums is 1, 2, 3 for the meal pickers.
func mealNums() []int {
	var out []int
	for i := 1; i <= schedule.MealsInDeal; i++ {
		out = append(out, i)
	}
	return out
}
