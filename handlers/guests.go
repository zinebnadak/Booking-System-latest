package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ozmo-booking/db"
	"ozmo-booking/payments"
	"ozmo-booking/schedule"
)

// Ordering and paying from the guests' own phones.
//
//   - /t/{code}  the QR sticker on the table. Once a waiter has opened the
//     table for QR orders, each phone that scans it becomes the next guest
//     ("Guest 3 at table 5").
//   - /g/{key}   that guest's own page: order from the menu (food goes to the
//     chef, drinks to the waiters), see their part, and pay it, or pay for
//     the whole table, with Apple Pay / Google Pay / card (Stripe).
//
// Paying holds what it pays for (see db/guests.go): while one guest pays for
// the whole table, everyone else sees "Guest 2 is paying for the whole
// table" and can't pay; it lifts the moment they pay or cancel, or after
// 3 minutes if they walk away (their payment page is cancelled at Stripe
// first, so it can't go through late).

const (
	tabCookie      = "ozmo_tab"  // remembers which guest this phone is
	tabCookieAge   = 8 * 60 * 60 // seconds: one evening
	guestMaxQty    = 20          // of one dish, in one order
	guestMaxItems  = 40          // in one order
	paySweepEvery  = 15 * time.Second
	stripePageLife = 31 * time.Minute // Stripe wants at least 30; we cancel it ourselves long before
)

var (
	limitUnlock      = limit{n: 5, window: 10 * time.Minute}   // wrong table codes from one phone
	limitUnlockTable = limit{n: 10, window: 10 * time.Minute}  // wrong table codes for one table, from anywhere
	limitJoin        = limit{n: 20, window: 10 * time.Minute}  // becoming a guest at a table
	limitGuestTab    = limit{n: 120, window: 10 * time.Minute} // ordering, paying, live updates
)

// ---------- joining a table ----------

func (s *Server) tabKeyCookie(r *http.Request) string {
	if c, err := r.Cookie(tabCookie); err == nil {
		return c.Value
	}
	return ""
}

func (s *Server) setTabCookie(w http.ResponseWriter, key string) {
	http.SetCookie(w, &http.Cookie{
		Name: tabCookie, Value: key, Path: "/", MaxAge: tabCookieAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(s.Cfg.BaseURL, "https://"),
	})
}

// tablePage is what a phone sees after scanning the table's QR code. It
// doesn't make a guest by itself (link previews and prefetching open pages
// too): the guest taps "Start ordering".
func (s *Server) tablePage(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	table := s.Store.TableForQR(code)
	if table == 0 {
		s.notFound(w, r)
		return
	}
	// Scanned again: back to the same guest, not a new one.
	if key := s.tabKeyCookie(r); key != "" && s.Store.StillAtTable(code, key) {
		http.Redirect(w, r, "/g/"+key, http.StatusSeeOther)
		return
	}
	lang := langOf(r)
	now := s.Now()
	bill, open := s.Store.OpenBill(table)
	data := M{
		"Title": stf(lang, "Table %d", table), "Table": table, "Code": code,
		"Open": open && bill.QROpen, "Full": open && len(bill.Split) >= db.MaxGuests,
		"Toast": guestTabFlash(lang, r.URL.Query().Get("msg")),
	}
	// Booked and the guests haven't arrived yet: locked, unless they type
	// their table code (from their booking email).
	if !(open && bill.QROpen) {
		if b, ok := s.Store.BookedNow(table, now); ok {
			data["BookedAt"] = b.Time
		}
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	s.render(w, r, "table_join", http.StatusOK, data)
}

// tableUnlock: the guests who booked the table type their table code. They're
// checked in, the table opens for QR orders, and this phone is their guest 1.
// Wrong codes are limited (per phone and per table), so the code can't be guessed.
func (s *Server) tableUnlock(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	table := s.Store.TableForQR(code)
	if table == 0 {
		s.notFound(w, r)
		return
	}
	now := s.Now()
	ipKey, tableKey := "unlock|"+s.clientIP(r), "unlock-table|"+strconv.Itoa(table)
	if s.rl.over(ipKey, limitUnlock, now) || s.rl.over(tableKey, limitUnlockTable, now) {
		redirectMsg(w, r, "/t/"+code, nil, "slow")
		return
	}
	pin := strings.TrimSpace(r.FormValue("pin"))
	if _, err := s.Store.UnlockTable(table, pin, now); err != nil {
		if errors.Is(err, db.ErrBadCode) {
			s.rl.hit(ipKey, now)
			s.rl.hit(tableKey, now)
			redirectMsg(w, r, "/t/"+code, nil, "badcode")
			return
		}
		redirectMsg(w, r, "/t/"+code, nil, "notbooked")
		return
	}
	s.openOnArrival(table, now)
	s.live.publish() // they show as arrived for the staff too
	_, guest, err := s.Store.JoinTable(code, now)
	if err != nil {
		// Checked in, but the table still has someone else's open bill.
		redirectMsg(w, r, "/t/"+code, nil, "checkedin")
		return
	}
	s.setTabCookie(w, guest.Key)
	redirectMsg(w, r, "/g/"+guest.Key, nil, "welcome")
}

func (s *Server) tableJoin(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !sameOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if key := s.tabKeyCookie(r); key != "" && s.Store.StillAtTable(code, key) {
		http.Redirect(w, r, "/g/"+key, http.StatusSeeOther) // pressed twice
		return
	}
	_, guest, err := s.Store.JoinTable(code, s.Now())
	switch {
	case errors.Is(err, db.ErrNoTable):
		s.notFound(w, r)
		return
	case errors.Is(err, db.ErrTableClosed):
		redirectMsg(w, r, "/t/"+code, nil, "closed")
		return
	case errors.Is(err, db.ErrTableFull):
		redirectMsg(w, r, "/t/"+code, nil, "full")
		return
	case err != nil:
		log.Printf("joining a table: %v", err)
		redirectMsg(w, r, "/t/"+code, nil, "stale")
		return
	}
	s.setTabCookie(w, guest.Key)
	s.live.publish() // the staff see a new guest at the table
	http.Redirect(w, r, "/g/"+guest.Key, http.StatusSeeOther)
}

// ---------- the guest's own page ----------

type guestLine struct {
	db.BillLine
	SharedBy int    // >1: shared with others
	Cents    int    // this guest's part of it
	Status   string // "cooking", "ready" (food), "" (drinks, or not sent)
}

type tableGuest struct {
	Guest  int
	Label  string
	Pay    int
	Paid   bool
	PaidBy string
	Payer  int
	Me     bool
}

type holdView struct {
	Ref    string
	Payer  int
	Label  string // "Guest 2 · Anna"
	All    bool
	Mine   bool
	LeftS  int // seconds left, for the countdown (not a clock time: phones' clocks can be wrong)
	Left   string
	URL    string // the payer can go back to their payment page
	Covers bool   // it holds my part
}

// guestData gathers everything a guest's page shows.
func (s *Server) guestData(r *http.Request, key string) (M, bool) {
	bill, me, ok := s.Store.GuestByKey(key)
	if !ok {
		return nil, false
	}
	lang := langOf(r)
	now := s.Now()
	pay, parts, dep := bill.Parts()
	sh := bill.Split[me-1]

	// My lines, in bill order; Parts lists my parts in the same order.
	tickets := s.Store.TicketsForBill(bill.ID)
	var mine []guestLine
	for _, l := range bill.Lines {
		if !l.Had(me) {
			continue
		}
		gl := guestLine{BillLine: l}
		if n := len(l.Guests); n > 1 {
			gl.SharedBy = n
		}
		if i := len(mine); i < len(parts[me-1]) {
			gl.Cents = parts[me-1][i].Cents
		}
		if tk, ok := tickets[l.TicketID]; ok && !l.Drink {
			gl.Status = "cooking"
			if tk.Held {
				gl.Status = "later" // after the starter (the waiter gives the go)
			}
			switch {
			case tk.ServedAt != nil:
				gl.Status = "" // on the table
			case tk.DoneAt != nil:
				gl.Status = "ready"
			}
		}
		mine = append(mine, gl)
	}

	var guests []tableGuest
	paidParts, unpaid, withWaiter := 0, 0, false
	for i, x := range bill.Split {
		guests = append(guests, tableGuest{
			Guest: x.Guest, Label: guestLabel(lang, x), Pay: pay[i], Paid: x.PaidAt != nil,
			PaidBy: x.PaidBy, Payer: x.Payer, Me: x.Guest == me,
		})
		if x.PaidAt != nil {
			paidParts += pay[i]
		} else {
			unpaid++
			paidParts += x.PartPaid() // some paid with a waiter already (a gift card)
			withWaiter = withWaiter || len(x.Payments) > 0
		}
	}
	unassigned := 0
	for _, l := range bill.Unassigned() {
		unassigned += l.Total()
	}
	rest := bill.ToPay() - paidParts // what "pay for the whole table" costs

	var hold *holdView
	for _, a := range s.Store.Holds(bill.ID, now) {
		if !now.Before(a.Until) && a.SessionID == "" {
			continue // ran out (demo): let go on the next action
		}
		h := &holdView{Ref: a.Ref, Payer: a.Payer, All: a.All, Mine: a.Payer == me, URL: a.URL, LeftS: secondsLeft(a.Until, now)}
		h.Label = guestLabel(lang, bill.Split[a.Payer-1])
		for _, g := range a.Guests {
			if g == me {
				h.Covers = true
			}
		}
		if left := a.Until.Sub(now); left > 0 {
			h.Left = strconv.Itoa(int(left.Minutes())) + ":" + pad2(int(left.Seconds())%60)
		} else {
			h.Left = "0:00"
		}
		if hold == nil || h.Mine || (h.Covers && !hold.Mine) {
			hold = h
		}
	}

	waitingOrders, rejectedOrders := s.Store.GuestPhoneOrders(bill.ID, me, now)
	tableWaiting := false
	for _, o := range s.Store.PendingPhoneOrders() {
		if o.BillID == bill.ID {
			tableWaiting = true
		}
	}
	open := bill.Open()
	canOrder := open && bill.QROpen && sh.PaidAt == nil && (hold == nil || !hold.Covers)
	// My own part: unless someone is paying it (or for everyone) right now.
	// Not when they paid some of it with a waiter: the waiter takes the rest.
	canPayMine := open && sh.PaidAt == nil && (hold == nil || (!hold.Covers && !hold.All)) && len(waitingOrders) == 0 && len(sh.Payments) == 0
	// The whole table: only while nobody is paying anything. Shown when it
	// means more than just my own part.
	// (Also when everyone paid their part but the waiter added something
	// that's nobody's yet: then it's "pay what's left".)
	canPayAll := open && hold == nil && rest > 0 && (unpaid > 1 || unassigned > 0 || sh.PaidAt != nil) && !tableWaiting && !withWaiter
	data := M{
		"Title": stf(lang, "Table %d", bill.Table), "Key": key, "Table": bill.Table,
		"Me": me, "MyLabel": guestLabel(lang, sh), "Name": sh.Name,
		"Bill": bill, "Open": open, "QROpen": bill.QROpen, "Voided": bill.Voided,
		"Mine": mine, "MyPart": pay[me-1], "MyDeposit": dep[me-1], "MyPaid": sh.PaidAt != nil,
		"MyLeft": bill.ShareLeft(me), "MyPartPaid": sh.PartPaid(), "WithWaiter": sh.PaidAt == nil && len(sh.Payments) > 0,
		"PaidBy": sh.PaidBy, "Payer": sh.Payer, "ReceiptRef": sh.Ref, "MyTip": sh.TipCents,
		"Guests": guests, "Unpaid": unpaid, "Rest": rest, "Unassigned": unassigned,
		"Hold": hold, "CanOrder": canOrder, "CanPay": canPayMine, "CanPayAll": canPayAll,
		"Waiting": phoneOrderViews(waitingOrders, now), "Rejected": rejectedOrders, "TableWaiting": tableWaiting && len(waitingOrders) == 0,
		"Min": db.MinOnlineCents, "Online": s.Pay.Enabled(),
		// After paying: a receipt on the phone, by email, or printed.
		"Receipt":    sh.PaidAt != nil && sh.PaidBy != db.PaidNothing,
		"PaperAsked": sh.PaperAt != nil && sh.PrintedAt == nil, "PaperGiven": sh.PrintedAt != nil,
		"Emailed": sh.Emailed, "CanEmail": sh.Emailed < db.MaxReceiptEmails,
		"Toast": guestTabFlash(lang, r.URL.Query().Get("msg")),
	}
	if canOrder {
		data["Menu"] = groupMenu(s.Store.GuestMenu())
	}
	if open {
		data["Call"] = s.callView(bill.Table, now)
	}
	return data, true
}

// callView: the "Call the waiter" part of a guest's page, and during the 3
// meals deal "Ready for the next meal".
type callView struct {
	Called bool   // a waiter has been called and hasn't come yet
	At     string // when
	Waiter bool   // the call is "please come" (not the next meal)
	Deal   bool   // 3 meals is running on this table
	Next   int    // the meal to bring next
	Last   bool   // they're on the last meal: "we're done"
	Coming bool   // the next meal is on its way
}

func (s *Server) callView(table int, now time.Time) callView {
	var v callView
	for _, t := range s.Store.TablesNow() {
		if t.No != table {
			continue
		}
		if c := t.Call; c != nil {
			v.Called, v.At, v.Waiter = true, clockOf(c.At), c.Waiter || t.Run == nil
		}
		if r := t.Run; r != nil {
			v.Deal, v.Next, v.Last, v.Coming = true, r.Meal()+1, r.LastMeal(), r.Waiting()
		}
	}
	return v
}

// guestCall: "Call the waiter" (kind=waiter) or "Ready for the next meal"
// (kind=next) from a guest's phone. The Tables screen chimes, like with the
// button on the table, and shows who called.
func (s *Server) guestCall(w http.ResponseWriter, r *http.Request) {
	key, ok := s.guestPost(w, r)
	if !ok {
		return
	}
	res, err := s.Store.CallFromPhone(key, r.FormValue("kind") != "next", s.Now())
	if err != nil {
		redirectMsg(w, r, "/g/"+key, nil, guestTabError(err))
		return
	}
	s.live.publish()
	msg := "called"
	if res.Kind != db.PressWaiter {
		msg = "nextmeal"
	}
	redirectMsg(w, r, "/g/"+key, nil, msg)
}

func secondsLeft(until, now time.Time) int {
	if d := until.Sub(now); d > 0 {
		return int(d.Seconds() + .5)
	}
	return 0
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// guestLabel is "Guest 3", or "Guest 3 · Anna" once they said their name.
func guestLabel(lang string, sh db.Share) string {
	l := stf(lang, "Guest %d", sh.Guest)
	if sh.Name != "" {
		l += " · " + sh.Name
	}
	return l
}

func (s *Server) guestPage(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	data, ok := s.guestData(r, key)
	if !ok {
		s.notFound(w, r)
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	if r.URL.Query().Get("part") == "live" {
		s.renderBlock(w, r, "guest_tab", "live", http.StatusOK, data)
		return
	}
	s.setTabCookie(w, key) // this phone is this guest
	s.render(w, r, "guest_tab", http.StatusOK, data)
}

// guestEvents is the guest page's live feed: it says "change" whenever
// something happens (an order, a payment, someone starting to pay).
func (s *Server) guestEvents(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.Store.GuestByKey(r.PathValue("key")); !ok {
		http.NotFound(w, r)
		return
	}
	s.tablesEvents(w, r)
}

// guestPost checks a guest's form: from our own site, and a real guest.
func (s *Server) guestPost(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.PathValue("key")
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return "", false
	}
	if _, _, ok := s.Store.GuestByKey(key); !ok {
		s.notFound(w, r)
		return "", false
	}
	return key, true
}

func (s *Server) guestName(w http.ResponseWriter, r *http.Request) {
	key, ok := s.guestPost(w, r)
	if !ok {
		return
	}
	if err := s.Store.SetGuestName(key, clip(oneLine(r.FormValue("name")), 30)); err != nil {
		redirectMsg(w, r, "/g/"+key, nil, guestTabError(err))
		return
	}
	s.live.publish()
	redirectMsg(w, r, "/g/"+key, nil, "named")
}

// guestOrder: the guest's order goes on the table's bill as theirs. Prices
// always come from the menu here; a guest can't type their own.
func (s *Server) guestOrder(w http.ResponseWriter, r *http.Request) {
	key, ok := s.guestPost(w, r)
	if !ok {
		return
	}
	var lines []db.NewLine
	total := 0
	for _, m := range s.Store.GuestMenu() {
		n := atoiDefault(r.FormValue("qty_"+strconv.Itoa(m.ID)), 0)
		if n <= 0 {
			continue
		}
		if n > guestMaxQty {
			redirectMsg(w, r, "/g/"+key, nil, "toomany")
			return
		}
		total += n
		note := ""
		if !m.Drink {
			note = clip(oneLine(r.FormValue("note_"+strconv.Itoa(m.ID))), maxNoteLen)
		}
		lines = append(lines, db.NewLine{MenuItemID: m.ID, Name: m.Name, PriceCents: m.PriceCents, ListCents: m.PriceCents,
			Qty: n, Drink: m.Drink, Note: note, VAT: m.VAT(), Course: m.Course, Hold: true})
	}
	if total > guestMaxItems {
		redirectMsg(w, r, "/g/"+key, nil, "toomany")
		return
	}
	// Something on their screen sold out while they were choosing.
	today := schedule.FormatDate(schedule.Today(s.Now()))
	for _, m := range s.Store.Menu(false) {
		if m.Gone(today) && atoiDefault(r.FormValue("qty_"+strconv.Itoa(m.ID)), 0) > 0 {
			redirectMsg(w, r, "/g/"+key, nil, "soldout")
			return
		}
	}
	waiting, err := s.Store.GuestOrder(key, lines, s.Now())
	if err != nil {
		redirectMsg(w, r, "/g/"+key, nil, guestTabError(err))
		return
	}
	s.live.publish() // the chef and the waiters see it at once
	if waiting {
		redirectMsg(w, r, "/g/"+key, nil, "waiting") // a waiter checks it first
		return
	}
	redirectMsg(w, r, "/g/"+key, nil, "ordered")
}

// tipOption is one button on the "Add a tip?" page.
type tipOption struct{ Pct, Tip, Total int }

// guestTip is the step before paying: no tip, 5 % or 10 %. Each button
// shows exactly what will be paid. Nothing is held yet: that starts with
// the button.
func (s *Server) guestTip(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	data, ok := s.guestData(r, key)
	if !ok {
		s.notFound(w, r)
		return
	}
	all := r.URL.Query().Get("all") == "1"
	amount, can := data["MyPart"].(int), data["CanPay"].(bool)
	if all {
		amount, can = data["Rest"].(int), data["CanPayAll"].(bool)
	}
	if !can || amount < db.MinOnlineCents {
		http.Redirect(w, r, "/g/"+key, http.StatusSeeOther) // someone started paying, or it's paid
		return
	}
	var opts []tipOption
	for _, pct := range db.TipChoices {
		tip := db.TipFor(amount, pct)
		opts = append(opts, tipOption{Pct: pct, Tip: tip, Total: amount + tip})
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	s.render(w, r, "guest_tip", http.StatusOK, M{
		"Title": st(langOf(r), "Add a tip?"), "Key": key, "Table": data["Table"], "All": all,
		"Amount": amount, "Options": opts,
	})
}

// guestPay starts paying: the guest's own part, or (all=1) the whole table,
// plus the tip they picked. The parts are held first, then the guest goes
// to Stripe's payment page (Apple Pay, Google Pay or card).
func (s *Server) guestPay(w http.ResponseWriter, r *http.Request) {
	key, ok := s.guestPost(w, r)
	if !ok {
		return
	}
	all := r.FormValue("all") == "1"
	tip := atoiDefault(r.FormValue("tip"), 0) // anything but 0, 5 or 10 counts as no tip
	now := s.Now()
	a, err := s.Store.StartPaymentTip(key, all, tip, db.PayHoldFor, now)
	var stale *db.StaleHoldError
	if errors.As(err, &stale) {
		// Someone's payment page ran out of time but is still open at
		// Stripe: cancel it first, then try again.
		for _, ref := range stale.Refs {
			if old, ok := s.Store.AttemptByRef(ref); ok {
				s.settleAttempt(r.Context(), old)
			}
		}
		a, err = s.Store.StartPaymentTip(key, all, tip, db.PayHoldFor, s.Now())
	}
	if err != nil {
		redirectMsg(w, r, "/g/"+key, nil, guestTabError(err))
		return
	}
	s.live.publish() // everyone at the table sees who's paying
	bill, me, _ := s.Store.GuestByKey(key)
	lang := langOf(r)
	if !s.Pay.Enabled() {
		demo := "/dev/tab/" + a.Ref
		_ = s.Store.SetAttemptSession(a.Ref, "", demo)
		http.Redirect(w, r, demo, http.StatusSeeOther)
		return
	}
	desc := stf(lang, "Table %d · Guest %d", bill.Table, me)
	if all {
		desc = stf(lang, "Table %d · the whole table", bill.Table)
	}
	base := s.publicBase(r) + "/g/" + key
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	sess, err := s.Pay.CreateCheckoutSession(ctx, payments.CheckoutParams{
		Reference:   a.Ref,
		Description: desc + " · " + s.Cfg.RestaurantName,
		AmountCents: a.Cents,
		TipCents:    a.TipCents,
		TipLabel:    stf(lang, "Tip for the staff (%d %%)", a.TipPercent),
		Locale:      lang,
		SuccessURL:  base + "/paid?a=" + url.QueryEscape(a.Ref) + "&session_id={CHECKOUT_SESSION_ID}",
		CancelURL:   base + "/back?a=" + url.QueryEscape(a.Ref),
		ExpiresAt:   time.Now().Add(stripePageLife),
	})
	if err != nil {
		log.Printf("stripe checkout for %s: %v", a.Ref, err)
		_ = s.Store.ReleaseAttempt(a.Ref)
		s.live.publish()
		redirectMsg(w, r, "/g/"+key, nil, "payfail")
		return
	}
	if err := s.Store.SetAttemptSession(a.Ref, sess.ID, sess.URL); err != nil {
		log.Printf("save checkout for %s: %v", a.Ref, err)
	}
	http.Redirect(w, r, sess.URL, http.StatusSeeOther)
}

// ownAttempt finds a payment this guest started.
func (s *Server) ownAttempt(key, ref string) (db.PayAttempt, bool) {
	bill, me, ok := s.Store.GuestByKey(key)
	if !ok {
		return db.PayAttempt{}, false
	}
	a, ok := s.Store.AttemptByRef(ref)
	if !ok || a.BillID != bill.ID || a.Payer != me {
		return db.PayAttempt{}, false
	}
	return a, true
}

// tabCancel: the payer gives up (the Cancel button, or Stripe's back link).
// Their payment page is cancelled at Stripe and the others can pay at once.
func (s *Server) tabCancel(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if r.Method == http.MethodPost {
		var ok bool
		if key, ok = s.guestPost(w, r); !ok {
			return
		}
	}
	a, ok := s.ownAttempt(key, r.FormValue("a"))
	if !ok {
		http.Redirect(w, r, "/g/"+key, http.StatusSeeOther)
		return
	}
	if !a.Pending() {
		http.Redirect(w, r, "/g/"+key, http.StatusSeeOther)
		return
	}
	switch s.settleAttempt(r.Context(), a) {
	case "paid":
		redirectMsg(w, r, "/g/"+key, nil, "paid")
	case "released":
		redirectMsg(w, r, "/g/"+key, nil, "cancelled")
	default:
		redirectMsg(w, r, "/g/"+key, nil, "payfail")
	}
}

// guestPaid: back from Stripe after paying. The webhook confirms it too;
// checking here means the guest sees "Paid" straight away.
func (s *Server) guestPaid(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	a, ok := s.ownAttempt(key, r.URL.Query().Get("a"))
	if !ok {
		http.Redirect(w, r, "/g/"+key, http.StatusSeeOther)
		return
	}
	sid := r.URL.Query().Get("session_id")
	if a.State != "paid" && s.Pay.Enabled() && sid != "" && sid == a.SessionID {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		sess, err := s.Pay.GetCheckoutSession(ctx, sid)
		cancel()
		if err == nil && sess.PaymentStatus == "paid" && sess.ClientReferenceID == a.Ref {
			s.tabPaid(a.Ref, sess.PaymentIntent)
		}
	}
	if a, _ = s.Store.AttemptByRef(a.Ref); a.State == "paid" {
		redirectMsg(w, r, "/g/"+key, nil, "paid")
		return
	}
	redirectMsg(w, r, "/g/"+key, nil, "paying")
}

// tabPaid records a payment from a guest's phone (from Stripe's webhook,
// the return link, or the demo page). Doing it twice changes nothing.
func (s *Server) tabPaid(ref, paymentIntent string) {
	closed, err := s.Store.AttemptPaid(ref, paymentIntent, s.Now())
	if err != nil {
		log.Printf("table payment %s: %v", ref, err)
		return
	}
	s.live.publish()
	a, _ := s.Store.AttemptByRef(ref)
	if a.Overpaid > 0 {
		// Can't happen while holds work; never keep money twice if it does.
		if a.Overpaid == a.Total() && s.Pay.Enabled() && paymentIntent != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			err := s.Pay.Refund(ctx, paymentIntent)
			cancel()
			log.Printf("table payment %s paid for parts already paid: refunded %d cents (err=%v)", ref, a.Total(), err)
		} else {
			log.Printf("table payment %s paid %d cents for parts already paid: refund it in Stripe", ref, a.Overpaid)
		}
	}
	if closed {
		log.Printf("table payment %s: the whole table is paid", ref)
	}
}

// settleAttempt ends a payment that's still holding parts: its Stripe page
// is cancelled and the hold let go. If Stripe says it was paid meanwhile,
// it's recorded as paid instead. Returns "paid", "released", or "" (Stripe
// couldn't be reached: it stays held and is tried again).
func (s *Server) settleAttempt(ctx context.Context, a db.PayAttempt) string {
	defer s.live.publish()
	if a.SessionID == "" || !s.Pay.Enabled() {
		_ = s.Store.ReleaseAttempt(a.Ref)
		return "released"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if sess, err := s.Pay.ExpireCheckoutSession(ctx, a.SessionID); err == nil && sess.Status == "expired" {
		_ = s.Store.ReleaseAttempt(a.Ref)
		return "released"
	}
	// Stripe refused: it was paid (or expired) meanwhile. Look.
	sess, err := s.Pay.GetCheckoutSession(ctx, a.SessionID)
	if err != nil {
		log.Printf("payment %s: Stripe unreachable, still held: %v", a.Ref, err)
		return ""
	}
	switch {
	case sess.PaymentStatus == "paid":
		s.tabPaid(a.Ref, sess.PaymentIntent)
		return "paid"
	case sess.Status == "expired":
		_ = s.Store.ReleaseAttempt(a.Ref)
		return "released"
	}
	log.Printf("payment %s: Stripe page is %s/%s, still held", a.Ref, sess.Status, sess.PaymentStatus)
	return ""
}

// SweepPayments lets go of payments nobody finished in time. The server runs
// it every 15 seconds; tests call it directly.
func (s *Server) SweepPayments(ctx context.Context) {
	now := s.Now()
	if s.Store.AutoAcceptPhoneOrders(now) { // phone orders nobody checked in 5 minutes
		s.live.publish()
	}
	for _, a := range s.Store.AttemptsToCancel(now) {
		s.settleAttempt(ctx, a)
	}
	if s.Store.ReleaseExpired(now) {
		s.live.publish()
	}
}

// ---------- demo payment page (no Stripe keys) ----------

func (s *Server) devTabPage(w http.ResponseWriter, r *http.Request) {
	if s.Pay.Enabled() {
		s.notFound(w, r)
		return
	}
	a, ok := s.Store.AttemptByRef(r.PathValue("ref"))
	if !ok {
		s.notFound(w, r)
		return
	}
	bill, err := s.Store.BillByID(a.BillID)
	if err != nil {
		s.notFound(w, r)
		return
	}
	lang := langOf(r)
	now := s.Now()
	live := a.Pending() && now.Before(a.Until)
	s.render(w, r, "dev_tab", http.StatusOK, M{
		"Title": st(lang, "Demo payment"), "A": a, "Table": bill.Table, "Live": live,
		"Payer": guestLabel(lang, bill.Split[a.Payer-1]), "Key": bill.Split[a.Payer-1].Key,
		"LeftS": secondsLeft(a.Until, now),
	})
}

func (s *Server) devTabSubmit(w http.ResponseWriter, r *http.Request) {
	if s.Pay.Enabled() {
		s.notFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	a, ok := s.Store.AttemptByRef(r.PathValue("ref"))
	if !ok {
		s.notFound(w, r)
		return
	}
	bill, err := s.Store.BillByID(a.BillID)
	if err != nil {
		s.notFound(w, r)
		return
	}
	back := "/g/" + bill.Split[a.Payer-1].Key
	if r.FormValue("action") == "cancel" {
		if a.Pending() {
			s.settleAttempt(r.Context(), a)
		}
		redirectMsg(w, r, back, nil, "cancelled")
		return
	}
	if !a.Pending() || !s.Now().Before(a.Until) {
		redirectMsg(w, r, back, nil, "timeout")
		return
	}
	s.tabPaid(a.Ref, "demo_"+a.Ref)
	redirectMsg(w, r, back, nil, "paid")
}

// ---------- messages ----------

var guestTabMsgs = map[string]string{
	"ordered":    "Thanks! Your order is on its way.",
	"named":      "Saved.",
	"paid":       "Paid. Thank you!",
	"paying":     "Your payment is being confirmed…",
	"cancelled":  "Payment cancelled. Nothing was charged.",
	"timeout":    "The payment took too long and was cancelled. Nothing was charged: try again.",
	"busy":       "Someone at your table is paying right now. Wait a moment.",
	"closed":     "This table isn't taking orders from phones right now. Ask your waiter.",
	"full":       "This table already has the most guests. Ask your waiter.",
	"nothing":    "There's nothing to pay.",
	"toosmall":   "That's too small to pay online. Pay your waiter instead.",
	"empty":      "Pick something first.",
	"toomany":    "That's a lot! For big orders, ask your waiter.",
	"payfail":    "The payment couldn't start. Try again, or pay your waiter.",
	"already":    "Your part is already paid.",
	"stale":      "Something changed meanwhile. Have a look and try again.",
	"emailed":    "Your receipt is on its way to your email.",
	"bademail":   "That email address doesn't look right. Check it and try again.",
	"mailmax":    "Your receipt was already emailed a few times. Ask your waiter if it didn't arrive.",
	"paper":      "A waiter will bring your printed receipt.",
	"notpaid":    "Your part isn't paid yet.",
	"called":     "Your waiter is on the way.",
	"soldout":    "Sorry, something you picked just sold out. Nothing was ordered: have a look and try again.",
	"waiting":    "Thanks! Your waiter checks your order, then it goes to the kitchen.",
	"pending":    "You can pay once your waiter has confirmed your order.",
	"badcode":    "That's not the right code. Check your booking email and try again.",
	"slow":       "Too many wrong codes. Wait 10 minutes, or ask your waiter.",
	"notbooked":  "There's no booking waiting at this table right now. Ask your waiter.",
	"checkedin":  "Welcome! You're checked in. Your waiter will be with you in a moment.",
	"welcome":    "Welcome! Order and pay from here whenever you like.",
	"nextmeal":   "Thanks! Your next meal is on its way.",
	"withwaiter": "You've paid some of your part with your waiter. Pay the rest with them too.",
}

func guestTabFlash(lang, msg string) string {
	if m, ok := guestTabMsgs[msg]; ok {
		return st(lang, m)
	}
	return ""
}

func guestTabError(err error) string {
	switch {
	case errors.Is(err, db.ErrBusy):
		return "busy"
	case errors.Is(err, db.ErrTableClosed):
		return "closed"
	case errors.Is(err, db.ErrNothingToPay):
		return "nothing"
	case errors.Is(err, db.ErrTooSmall):
		return "toosmall"
	case errors.Is(err, db.ErrNothingToAdd):
		return "empty"
	case errors.Is(err, db.ErrSharePaid):
		return "already"
	case errors.Is(err, db.ErrBillClosed), errors.Is(err, db.ErrNoBill):
		return "closed"
	case errors.Is(err, db.ErrNotPaidYet):
		return "notpaid"
	case errors.Is(err, db.ErrOrderPending):
		return "pending"
	case errors.Is(err, db.ErrTooManyMails):
		return "mailmax"
	case errors.Is(err, db.ErrSoldOut):
		return "soldout" // the last one went to someone else
	case errors.Is(err, db.ErrPartlyPaid):
		return "withwaiter"
	}
	log.Printf("guest at table: %v", err)
	return "stale"
}

// ---------- staff: QR orders on or off, the stickers ----------

// openOnArrival turns QR orders on by itself when guests sit down, so staff
// don't have to remember. (Staff can still turn it off on the table's page.)
func (s *Server) openOnArrival(table int, now time.Time) {
	if table == 0 {
		return
	}
	ref, deposit := s.bookingAtTable(table, now)
	if opened, err := s.Store.OpenOnArrival(table, ref, deposit, now); err != nil {
		log.Printf("QR orders for table %d: %v", table, err)
	} else if opened {
		s.live.publish()
	}
}

// tableQR opens a table for QR orders, or closes it again.
func (s *Server) tableQR(w http.ResponseWriter, r *http.Request) {
	table := tableFromPath(r)
	if table == 0 || !s.parseStaffForm(w, r) {
		if table == 0 {
			s.notFound(w, r)
		}
		return
	}
	open := r.FormValue("open") == "1"
	now := s.Now()
	ref, deposit := s.bookingAtTable(table, now)
	if _, err := s.Store.OpenForGuests(table, open, ref, deposit, now); err != nil {
		redirectMsg(w, r, orderPath(table), nil, orderError(err))
		return
	}
	s.live.publish()
	msg := "qrclosed"
	if open {
		msg = "qropen"
	}
	if r.Header.Get("X-Live") != "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	back := orderPath(table)
	if r.FormValue("back") == "orders" {
		back = "/admin/orders"
	}
	redirectMsg(w, r, back, nil, msg)
}

type stickerView struct {
	Table int
	URL   string
	Open  bool
}

// qrStickers is a printable page with every table's QR sticker.
func (s *Server) qrStickers(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	base := s.publicBase(r)
	open := map[int]bool{}
	for _, b := range s.Store.OpenBills() {
		open[b.Table] = b.QROpen
	}
	var list []stickerView
	for _, q := range s.Store.TableQRs() {
		list = append(list, stickerView{Table: q.Table, URL: base + "/t/" + q.Code, Open: open[q.Table]})
	}
	s.render(w, r, "qr_stickers", http.StatusOK, M{
		"Admin": true, "Bare": true, "QR": true, "Title": st(lang, "Table QR codes"),
		"Stickers": list, "Toast": flashText(r), "ReviewURL": base + "/review",
	})
}

// qrNewCode gives a table a new QR code: the old sticker stops working.
func (s *Server) qrNewCode(w http.ResponseWriter, r *http.Request) {
	no := atoiDefault(r.PathValue("no"), 0)
	if !s.parseStaffForm(w, r) {
		return
	}
	if _, err := s.Store.NewTableQR(no); err != nil {
		s.notFound(w, r)
		return
	}
	redirectMsg(w, r, "/admin/tables/qr", nil, "qrnew")
}

// holdView for staff: who is paying on their phone right now.
func (s *Server) holdViews(lang string, bill db.Bill, now time.Time) []holdView {
	var out []holdView
	for _, a := range s.Store.Holds(bill.ID, now) {
		if !now.Before(a.Until) && a.SessionID == "" {
			continue
		}
		h := holdView{Ref: a.Ref, Payer: a.Payer, All: a.All, LeftS: secondsLeft(a.Until, now)}
		if a.Payer >= 1 && a.Payer <= len(bill.Split) {
			h.Label = guestLabel(lang, bill.Split[a.Payer-1])
		}
		left := a.Until.Sub(now)
		if left < 0 {
			left = 0
		}
		h.Left = strconv.Itoa(int(left.Minutes())) + ":" + pad2(int(left.Seconds())%60)
		out = append(out, h)
	}
	return out
}

// drinksToBring lists the drinks guests ordered from their phones, for the
// waiters (the chef never sees drinks).
type drinkView struct {
	ID    int
	Table int
	At    string
	Items []db.TicketItem
}

func (s *Server) drinksToBring() []drinkView {
	var out []drinkView
	for _, t := range s.Store.DrinksToBring() {
		v := drinkView{ID: t.ID, Table: t.Table, At: t.At.In(schedule.Location).Format("15:04")}
		for _, it := range t.Items {
			if !it.Cancelled {
				v.Items = append(v.Items, it)
			}
		}
		if len(v.Items) > 0 {
			out = append(out, v)
		}
	}
	return out
}

// ---------- receipts after paying on the phone ----------

// guestReceiptPaper: the guest wants a printed receipt. It shows up on the
// Orders screen under "Receipts to print".
func (s *Server) guestReceiptPaper(w http.ResponseWriter, r *http.Request) {
	key, ok := s.guestPost(w, r)
	if !ok {
		return
	}
	if err := s.Store.AskPaperReceipt(key, s.Now()); err != nil {
		redirectMsg(w, r, "/g/"+key, nil, guestTabError(err))
		return
	}
	s.live.publish()
	redirectMsg(w, r, "/g/"+key, nil, "paper")
}

// guestReceiptEmail sends the guest their receipt. The address is used for
// this one email and not kept.
func (s *Server) guestReceiptEmail(w http.ResponseWriter, r *http.Request) {
	key, ok := s.guestPost(w, r)
	if !ok {
		return
	}
	to := strings.TrimSpace(r.FormValue("email"))
	if a, err := mail.ParseAddress(to); err != nil || a.Address != to || len(to) > 254 || !strings.Contains(to, ".") {
		redirectMsg(w, r, "/g/"+key, nil, "bademail")
		return
	}
	bill, g, err := s.Store.UseReceiptEmail(key)
	if err != nil {
		redirectMsg(w, r, "/g/"+key, nil, guestTabError(err))
		return
	}
	subject, body := s.receiptEmail(langOf(r), bill, g, s.publicBase(r))
	s.Mail.SendAsync(to, subject, body)
	redirectMsg(w, r, "/g/"+key, nil, "emailed")
}

// receiptEmail is a guest's receipt as an email, with the link to its online copy.
func (s *Server) receiptEmail(lang string, bill db.Bill, g int, base string) (string, string) {
	v := s.receiptFor(bill, g, base, s.Now())
	sh := bill.Split[g-1]
	at := s.Now()
	if sh.PaidAt != nil {
		at = *sh.PaidAt
	}
	var items strings.Builder
	for _, l := range v.Lines {
		q := strconv.Itoa(l.Qty) + " ×"
		if l.SharedBy > 1 {
			q = "1/" + strconv.Itoa(l.SharedBy)
		}
		fmt.Fprintf(&items, "  %s %s  %s\n", q, l.Name, euro(l.Cents))
	}
	var sums strings.Builder
	if v.Deposit > 0 {
		fmt.Fprintf(&sums, "%s: %s\n%s: −%s\n", st(lang, "Food and drinks"), euro(v.Food), st(lang, "Deposit paid online"), euro(v.Deposit))
	}
	fmt.Fprintf(&sums, "%s: %s", st(lang, "Your part"), euro(v.Total))
	if v.Tip > 0 {
		fmt.Fprintf(&sums, "\n%s: %s", st(lang, "Tip for the staff"), euro(v.Tip))
	}
	how := st(lang, "Paid by card")
	switch sh.PaidBy {
	case db.PaidOnline:
		how = st(lang, "Paid online")
	case db.PaidCash:
		how = st(lang, "Paid in cash")
	}
	if v.PaidByGuest > 0 {
		how += ". " + stf(lang, "Guest %d paid this for you.", v.PaidByGuest)
	}
	if len(v.PaidFor) > 0 {
		how += ". " + stf(lang, "You also paid for %s: %s in total.", guestList(lang, v.PaidFor), euro(v.PaidForTotal))
	}
	subject := stf(lang, "Your receipt from %s", s.Cfg.RestaurantName)
	body := mailBody(
		stf(lang, "Thank you for visiting %s!", s.Cfg.RestaurantName),
		stf(lang, "Table %d", bill.Table)+" · "+guestLabel(lang, sh)+" · "+longDate(lang, schedule.FormatDate(at))+", "+clockOf(at),
		items.String(),
		sums.String()+"\n"+how,
		st(lang, "Your receipt online:")+"\n"+v.URL,
		signature(s.Cfg),
	)
	return subject, body
}

// receiptGiven: a waiter brought a guest their printed receipt.
func (s *Server) receiptGiven(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	err := s.Store.ReceiptGiven(atoiDefault(r.PathValue("bill"), 0), atoiDefault(r.PathValue("guest"), 0), s.Now())
	if err == nil {
		s.live.publish()
	}
	if r.Header.Get("X-Live") != "" {
		if err != nil {
			http.Error(w, st(langOf(r), "That order changed in the meantime."), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/admin/orders", http.StatusSeeOther)
}

type paperView struct {
	BillID, Table, Guest int
	Label                string
	Cents                int
	At                   string
}

func (s *Server) receiptsToPrint(lang string) []paperView {
	var out []paperView
	for _, p := range s.Store.ReceiptsToPrint() {
		b, err := s.Store.BillByID(p.BillID)
		if err != nil || p.Guest > len(b.Split) {
			continue
		}
		pay, _, _ := b.Parts()
		out = append(out, paperView{BillID: p.BillID, Table: p.Table, Guest: p.Guest,
			Label: guestLabel(lang, b.Split[p.Guest-1]), Cents: pay[p.Guest-1], At: clockOf(p.At)})
	}
	return out
}

// ---------- staff: who hasn't paid, adding for one guest ----------

// unpaidAlert marks a split table where some guests paid and others haven't
// (yet): the Orders screen shows it in red until everything is settled.
func (s *Server) unpaidAlert(lang string, t *orderTile, b db.Bill, holds []db.PayAttempt, now time.Time) {
	if !b.IsSplit() || !b.AnySharePaid() {
		return
	}
	pay, items, _ := b.Parts()
	paying := map[int]bool{}
	for _, a := range holds {
		if now.Before(a.Until) || a.SessionID != "" {
			for _, g := range a.Guests {
				paying[g] = true
			}
		}
	}
	for i, sh := range b.Split {
		if sh.PaidAt != nil {
			t.PaidCents += pay[i]
			continue
		}
		if len(items[i]) == 0 {
			continue // had nothing: doesn't need to pay
		}
		t.PaidCents += sh.PartPaid() // paid some of it already (a gift card)
		t.Unpaid = append(t.Unpaid, unpaidGuest{Label: guestLabel(lang, sh), Cents: b.ShareLeft(sh.Guest), Paying: paying[sh.Guest]})
	}
	for _, l := range b.Unassigned() {
		t.Nobodys += l.Total()
	}
	t.TotalCents = b.ToPay()
	t.Alert = len(t.Unpaid) > 0 || t.Nobodys > 0
}

type forGuest struct {
	Guest int
	Label string
	Off   bool // paid, or paying right now: nothing more can go on their part
}

// forGuests are the guests staff can add something for, at a split table.
func (s *Server) forGuests(lang string, b db.Bill, now time.Time) []forGuest {
	held := map[int]bool{}
	for _, a := range s.Store.Holds(b.ID, now) {
		if now.Before(a.Until) || a.SessionID != "" {
			for _, g := range a.Guests {
				held[g] = true
			}
		}
	}
	var out []forGuest
	for _, sh := range b.Split {
		out = append(out, forGuest{Guest: sh.Guest, Label: guestLabel(lang, sh), Off: sh.PaidAt != nil || held[sh.Guest]})
	}
	return out
}

// callsNow: tables that want a waiter, for the Orders screen (the Tables
// screen shows them too).
func (s *Server) callsNow(lang string, now time.Time) []tableTile {
	var out []tableTile
	for _, t := range s.tableTiles(lang, now) {
		if t.Waiting() {
			out = append(out, t)
		}
	}
	return out
}

// ---------- checking phone orders ----------

type phoneOrderView struct {
	db.PhoneOrder
	Label    string // "Guest 2 · Anna" (staff)
	Time     string
	AutoAt   string // "18:47": goes through by itself then
	AutoLeft int    // seconds until then
}

func phoneOrderViews(list []db.PhoneOrder, now time.Time) []phoneOrderView {
	var out []phoneOrderView
	for _, o := range list {
		out = append(out, phoneOrderView{PhoneOrder: o, Time: clockOf(o.At), AutoAt: clockOf(o.AutoAt()), AutoLeft: secondsLeft(o.AutoAt(), now)})
	}
	return out
}

// pendingOrders: the phone orders waiting on the Orders screen.
func (s *Server) pendingOrders(lang string, now time.Time) []phoneOrderView {
	views := phoneOrderViews(s.Store.PendingPhoneOrders(), now)
	for i := range views {
		if b, err := s.Store.BillByID(views[i].BillID); err == nil && views[i].Guest <= len(b.Split) {
			views[i].Label = guestLabel(lang, b.Split[views[i].Guest-1])
		}
	}
	return views
}

// phoneOrderAction: a waiter taps Accept or Reject on a phone order.
func (s *Server) phoneOrderAction(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	accept := r.PathValue("action") == "accept"
	if !accept && r.PathValue("action") != "reject" {
		s.notFound(w, r)
		return
	}
	o, err := s.Store.DecidePhoneOrder(atoiDefault(r.PathValue("id"), 0), accept, s.Now())
	if err == nil {
		if !accept {
			var items []string
			for _, l := range o.Lines {
				items = append(items, fmt.Sprintf("%d× %s", l.Qty, l.Name))
			}
			s.logActivity(r, o.Table, "rejected a phone order", stf(langEN, "Guest %d", o.Guest)+": "+strings.Join(items, ", "))
		}
		s.live.publish()
	}
	if r.Header.Get("X-Live") != "" {
		if err != nil {
			http.Error(w, st(langOf(r), "That order changed in the meantime."), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/admin/orders", http.StatusSeeOther)
}

// checkPhoneOrders turns the check on or off (Orders screen).
func (s *Server) checkPhoneOrders(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	on := r.FormValue("on") == "1"
	if err := s.Store.SetCheckPhoneOrders(on, s.Now()); err != nil {
		log.Printf("check phone orders: %v", err)
	}
	s.live.publish()
	msg := "checkoff"
	if on {
		msg = "checkon"
	}
	redirectMsg(w, r, "/admin/orders", nil, msg)
}
