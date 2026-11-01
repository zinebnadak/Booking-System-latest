package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

// Orders at the tables and the chef's kitchen screen.
//
//   - /admin/orders          every table's open bill, and what was paid today
//   - /admin/orders/{table}  take an order: dishes and drinks onto the table's bill
//   - /admin/kitchen         the chef's screen: food only, no prices
//
// Guests pay at the end: everything they order during the meal goes on the
// same bill until staff press Paid.

// Limits for what staff type in.
const (
	maxQty     = 50
	maxNoteLen = 120
	maxNameLen = 80
)

// ---------- Orders overview ----------

type orderTile struct {
	No      int
	Room    string
	Bill    *db.Bill
	Opened  string // "18:40"
	Cooking int    // tickets the chef hasn't finished
	Held    int    // courses waiting for the waiter's go ("Vänta varmt")
	Ready   int    // food done, waiting at the pass to be taken to the table
	ReadyAt string // since when ("19:02")
	Bring   int    // drinks guests ordered on their phones, not brought yet
	Served  bool   // everything is on the table: next comes the bill
	Billed  bool
	Who     string
	OldOpen bool // still open from an earlier day: someone forgot to close it
	Paying  bool // a guest is paying on their phone right now

	// Split tables where some guests paid and others haven't yet.
	Alert      bool
	Unpaid     []unpaidGuest
	Nobodys    int // cents of things nobody had yet
	PaidCents  int
	TotalCents int
}

type unpaidGuest struct {
	Label  string
	Cents  int
	Paying bool // paying on their phone right now
}

func (s *Server) orderTiles(lang string, now time.Time) []orderTile {
	bills := map[int]db.Bill{}
	for _, b := range s.Store.OpenBills() {
		bills[b.Table] = b
	}
	who := s.seatedNow(lang, now)
	today := schedule.FormatDate(schedule.Today(now))
	tiles := make([]orderTile, schedule.TableCount())
	for i, tb := range schedule.TablePlan {
		t := &tiles[i]
		t.No, t.Room = tb.No, tb.Room
		t.Who = who[t.No]
		b, ok := bills[t.No]
		if !ok {
			continue
		}
		t.Bill = &b
		t.Opened = b.OpenedAt.In(schedule.Location).Format("15:04")
		t.Billed = b.BilledAt != nil
		t.OldOpen = schedule.FormatDate(b.OpenedAt) < today
		holds := s.Store.Holds(b.ID, now)
		t.Paying = len(holds) > 0
		s.unpaidAlert(lang, t, b, holds, now)
		var firstReady time.Time
		for _, tk := range s.Store.TicketsForBill(b.ID) {
			switch {
			case tk.Message != "" || !tk.Live(): // a message to the kitchen, or all cancelled
			case tk.Bar:
				if tk.DoneAt == nil {
					t.Bring++
				}
			case tk.DoneAt == nil && tk.Held:
				t.Held++
			case tk.DoneAt == nil:
				t.Cooking++
			case tk.ReadyToServe():
				t.Ready++
				if firstReady.IsZero() || tk.DoneAt.Before(firstReady) {
					firstReady = *tk.DoneAt
				}
			}
		}
		if t.Ready > 0 {
			t.ReadyAt = clockOf(firstReady)
		}
		t.Served = b.Count() > 0 && t.Cooking+t.Held+t.Ready+t.Bring == 0
	}
	return tiles
}

type dayTakings struct {
	Bills, Total int
	Rows         []takingRow // per way of paying
	Tips         int         // added on phones, for the staff (not in Total)
	VouchersSold int         // gift cards and lunch booklets sold today (not in Total)
	Vouchers     int         // how many
}

func (s *Server) adminOrders(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	var day dayTakings
	today := schedule.FormatDate(schedule.Today(now))
	takings := map[string]int{}
	for _, b := range s.Store.PaidBillsOn(today) {
		day.Bills++
		for m, c := range b.Takings() {
			takings[m] += c
			day.Total += c
		}
		day.Tips += b.Tips()
	}
	day.Rows = takingRows(lang, takings)
	for _, v := range s.Store.VouchersSoldOn(today) {
		day.Vouchers++
		day.VouchersSold += v.SoldCents
	}
	tiles := s.orderTiles(lang, now)
	var alerts []orderTile
	for _, t := range tiles {
		if t.Alert {
			alerts = append(alerts, t)
		}
	}
	data := M{
		"Admin": true, "Nav": "orders", "Title": st(lang, "Orders"),
		"Tiles": tiles, "Alerts": alerts, "Day": day, "Toast": flashText(r),
		"Rooms":  byRoom(tiles, func(t orderTile) string { return t.Room }, func(t orderTile) bool { return t.Bill != nil }),
		"Drinks": s.drinksToBring(), "Receipts": s.receiptsToPrint(lang), "Calls": s.callsNow(lang, now),
		"PhoneOrders": s.pendingOrders(lang, now), "CheckPhoneOrders": s.Store.CheckPhoneOrders(),
		"Stock": s.stockStrip(), "OnlinePaid": s.onlinePaid(lang, now), "ReadyFood": s.readyFood(now),
	}
	if r.URL.Query().Get("part") == "tiles" {
		s.renderBlock(w, r, "admin_orders", "tiles", http.StatusOK, data)
		return
	}
	s.render(w, r, "admin_orders", http.StatusOK, data)
}

// ---------- One table: take an order ----------

type lineView struct {
	db.BillLine
	Status string // "", "cooking", "held", "ready" (at the pass), "served"; drinks guests ordered: "bring", "brought"
	Ready  string // when the chef finished it, or when it was served
	Who    string // whose it is, when guests ordered it from their phones
}

func tableFromPath(r *http.Request) int {
	n := atoiDefault(r.PathValue("table"), 0)
	if !schedule.ValidTable(n) {
		return 0
	}
	return n
}

// bookingAtTable is the online booking whose guests sit at the table now:
// its deposit comes off their bill.
func (s *Server) bookingAtTable(table int, now time.Time) (ref string, deposit int) {
	var best *db.Booking
	bookings := s.Store.BookingsOn(schedule.FormatDate(schedule.Today(now)), now)
	for i := range bookings {
		b := &bookings[i]
		if b.Kind != db.KindTable || b.TableNo != table || !b.Online() || b.DepositCents <= 0 ||
			b.EffectiveStatus(now) != db.StatusArrived {
			continue
		}
		start := b.Start()
		if now.Before(start.Add(-30*time.Minute)) || !now.Before(start.Add(b.Duration()+time.Hour)) {
			continue
		}
		if best == nil || b.Start().After(best.Start()) {
			best = b
		}
	}
	if best == nil {
		return "", 0
	}
	return best.Ref, best.DepositCents
}

func (s *Server) adminOrder(w http.ResponseWriter, r *http.Request) {
	table := tableFromPath(r)
	if table == 0 {
		s.notFound(w, r)
		return
	}
	lang := langOf(r)
	now := s.Now()
	bill, open := s.Store.OpenBill(table)
	var lines []lineView
	if open {
		tickets := s.Store.TicketsForBill(bill.ID)
		for _, l := range bill.Lines {
			v := lineView{BillLine: l}
			if tk, ok := tickets[l.TicketID]; ok && tk.Bar {
				v.Status = "bring"
				if tk.DoneAt != nil {
					v.Status = "brought"
				}
			} else if ok {
				v.Status = "cooking"
				if tk.Held {
					v.Status = "held"
				}
				switch {
				case tk.ServedAt != nil:
					v.Status, v.Ready = "served", clockOf(*tk.ServedAt)
				case tk.DoneAt != nil:
					v.Status, v.Ready = "ready", clockOf(*tk.DoneAt)
				}
			}
			if bill.QRGuests() && len(l.Guests) == 1 && l.Guests[0] <= len(bill.Split) {
				v.Who = guestLabel(lang, bill.Split[l.Guests[0]-1])
			}
			lines = append(lines, v)
		}
	}
	var freeTables []int
	busy := map[int]bool{}
	for _, b := range s.Store.OpenBills() {
		busy[b.Table] = true
	}
	for _, t := range schedule.TableNos() {
		if !busy[t] && t != table {
			freeTables = append(freeTables, t)
		}
	}
	ref, deposit := "", 0
	if !open {
		ref, deposit = s.bookingAtTable(table, now)
	}
	data := M{
		"Admin": true, "Nav": "orders", "Title": stf(lang, "Table %d", table),
		"Table": table, "Open": open, "Bill": bill, "Lines": lines,
		"Menu": groupMenu(s.Store.StaffMenu()), "FreeTables": freeTables, "Today": schedule.FormatDate(schedule.Today(now)),
		"Who": s.seatedNow(lang, now)[table], "NextDeposit": deposit, "NextBooking": ref,
		"Toast": flashText(r), "Stock": s.stockStrip(), "Ingredients": s.Store.Ingredients(),
	}
	switch r.URL.Query().Get("msg") {
	case "addedfood", "addeddrinks", "addedheld":
		// Just added to the bill: say so with a big way back to Orders.
		data["Added"], data["Toast"] = data["Toast"], ""
	}
	if open {
		data["Opened"] = bill.OpenedAt.In(schedule.Location).Format("15:04")
		data["Holds"] = s.holdViews(lang, bill, now)
		if bill.IsSplit() {
			data["ForGuests"] = s.forGuests(lang, bill, now)
		}
	}
	data["QROpen"] = open && bill.QROpen
	if open {
		data["Held"] = s.Store.HeldCourses(bill.ID)
	}
	s.render(w, r, "admin_order", http.StatusOK, data)
}

// readOrder turns the order form into bill lines. Prices come from the menu
// unless staff typed another price; dishes that aren't on the menu (any
// more) are ignored.
//
// gone names something picked that has run out meanwhile (another waiter
// sold the last one, or the kitchen marked it): then nothing is added.
//
// needPrice names a dish with no price on the menu yet that was picked
// without typing one.
func (s *Server) readOrder(r *http.Request) (lines []db.NewLine, ok bool, gone, needPrice string) {
	ok = true
	// "Vänta varmt": mains wait while the starters are eaten, and desserts
	// ordered with the meal wait too, until the waiter gives the go.
	hold := r.FormValue("hold") == "1"
	today := schedule.FormatDate(schedule.Today(s.Now()))
	for _, m := range s.Store.StaffMenu() {
		id := strconv.Itoa(m.ID)
		n := atoiDefault(r.FormValue("qty_"+id), 0)
		if n <= 0 {
			continue
		}
		if m.Gone(today) {
			gone = m.Name
			continue
		}
		if n > maxQty {
			ok = false
			continue
		}
		price := m.PriceCents
		if p := strings.TrimSpace(r.FormValue("price_" + id)); p != "" {
			v, err := parseEuro(p)
			if err != nil {
				ok = false
				continue
			}
			price = v
		} else if m.PriceMissing() {
			ok, needPrice = false, m.Name // no price on the menu yet: staff must type one
			continue
		}
		note := clip(oneLine(r.FormValue("note_"+id)), maxNoteLen)
		if m.Drink {
			note = "" // drinks have no kitchen note
		}
		lines = append(lines, db.NewLine{MenuItemID: m.ID, Name: m.Name, PriceCents: price, ListCents: m.PriceCents,
			Qty: n, Drink: m.Drink, Note: note, VAT: m.VAT(), Course: m.Course, Hold: hold, NoKitchen: m.Package})
	}
	// Something that isn't on the menu ("Öppen mat", "Öppen alkohol"): typed
	// in with its own name and price.
	if name := clip(oneLine(r.FormValue("custom_name")), maxNameLen); name != "" {
		price, err := parseEuro(r.FormValue("custom_price"))
		n := atoiDefault(r.FormValue("custom_qty"), 1)
		if err != nil || n < 1 || n > maxQty {
			return nil, false, gone, needPrice
		}
		kind := r.FormValue("custom_type")
		drink := kind == "drink" || kind == "alcohol"
		if keep := r.FormValue("custom_keep"); keep == "today" || keep == "until" {
			// Kept on the menu as a special (today, or until a day): it goes
			// on the order form's first section, and takes its ingredients
			// from stock like any dish.
			until, okDay := specialUntil(keep, r.FormValue("custom_until"), s.Now())
			if !okDay || price <= 0 {
				return nil, false, gone, needPrice
			}
			sp, err := s.Store.SaveSpecial(db.MenuItem{Name: name, PriceCents: price, Drink: drink, Alcohol: kind == "alcohol",
				Until: until, Uses: readUses(r, "custom_use")})
			if err != nil {
				return nil, false, gone, needPrice
			}
			s.logActivity(r, 0, "added a special", sp.Name+" · "+euro(sp.PriceCents)+" · "+sp.Until)
			nl := db.NewLine{MenuItemID: sp.ID, Name: sp.Name, PriceCents: price, ListCents: price, Qty: n, Drink: drink,
				VAT: sp.VAT(), Course: sp.Course, Hold: hold}
			if !drink {
				nl.Note = clip(oneLine(r.FormValue("custom_note")), maxNoteLen)
			}
			return append(lines, nl), ok, gone, needPrice
		}
		nl := db.NewLine{Name: name, PriceCents: price, ListCents: price, Qty: n, Drink: drink, VAT: schedule.VATFood}
		if kind == "alcohol" {
			nl.VAT = schedule.VATAlcohol
		}
		if !drink {
			nl.Note = clip(oneLine(r.FormValue("custom_note")), maxNoteLen)
			nl.Course = db.CourseMain
		}
		lines = append(lines, nl)
	}
	return lines, ok, gone, needPrice
}

// specialUntil is a special's last day: today, or the day picked (from
// today up to a year ahead).
func specialUntil(keep, day string, now time.Time) (string, bool) {
	today := schedule.Today(now)
	if keep == "today" {
		return schedule.FormatDate(today), true
	}
	d, err := schedule.ParseDate(strings.TrimSpace(day))
	if err != nil || d.Before(today) || d.After(today.AddDate(1, 0, 0)) {
		return "", false
	}
	return schedule.FormatDate(d), true
}

// readUses reads "uses from stock" rows: prefix_ing (an ingredient) and
// prefix_amt (how much one portion takes), side by side.
func readUses(r *http.Request, prefix string) []db.IngredientUse {
	ids, amts := r.Form[prefix+"_ing"], r.Form[prefix+"_amt"]
	var out []db.IngredientUse
	for i := range ids {
		if i >= len(amts) {
			break
		}
		id := atoiDefault(ids[i], 0)
		amt := atoiDefault(strings.TrimSpace(amts[i]), 0)
		if id > 0 && amt > 0 {
			out = append(out, db.IngredientUse{ID: id, Amount: amt})
		}
	}
	return out
}

func (s *Server) parseStaffForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return false
	}
	return true
}

// orderError turns a store error into a message key for the staff page.
func orderError(err error) string {
	switch {
	case errors.Is(err, db.ErrStale), errors.Is(err, db.ErrBillClosed), errors.Is(err, db.ErrNoBill), errors.Is(err, db.ErrNoLine):
		return "billstale"
	case errors.Is(err, db.ErrSentToKitchen):
		return "sentalready"
	case errors.Is(err, db.ErrNothingToAdd):
		return "nothingadded"
	case errors.Is(err, db.ErrEmptyBill):
		return "billempty"
	case errors.Is(err, db.ErrTableTaken):
		return "tabletaken"
	case errors.Is(err, db.ErrSharePaid):
		return "sharepaid"
	case errors.Is(err, db.ErrUnassigned):
		return "unassigned"
	case errors.Is(err, db.ErrSplit):
		return "billsplit"
	case errors.Is(err, db.ErrNotSplit), errors.Is(err, db.ErrBadGuest):
		return "billstale"
	case errors.Is(err, db.ErrBusy):
		return "phonepaying"
	case errors.Is(err, db.ErrQRGuests):
		return "qrguests"
	case errors.Is(err, db.ErrPaidOnline):
		return "paidonline"
	case errors.Is(err, db.ErrOrderPending):
		return "orderwaiting"
	case errors.Is(err, db.ErrPartlyPaid):
		return "partlypaid"
	}
	log.Printf("orders: %v", err)
	return "invalid"
}

func orderPath(table int) string { return "/admin/orders/" + strconv.Itoa(table) }

// orderAdd puts the new dishes and drinks on the table's bill. Food goes to
// the chef as one ticket.
func (s *Server) orderAdd(w http.ResponseWriter, r *http.Request) {
	table := tableFromPath(r)
	if table == 0 || !s.parseStaffForm(w, r) {
		if table == 0 {
			s.notFound(w, r)
		}
		return
	}
	now := s.Now()
	lines, ok, gone, needPrice := s.readOrder(r)
	if gone != "" {
		redirectMsg(w, r, orderPath(table), url.Values{"item": {gone}}, "soldout")
		return
	}
	if needPrice != "" {
		redirectMsg(w, r, orderPath(table), url.Values{"item": {needPrice}}, "needprice")
		return
	}
	if !ok {
		redirectMsg(w, r, orderPath(table), nil, "invalid")
		return
	}
	expect := atoiDefault(r.FormValue("bill"), 0)
	ref, deposit := "", 0
	if expect == 0 {
		ref, deposit = s.bookingAtTable(table, now)
	}
	var ticket *db.Ticket
	var bill db.Bill
	var err error
	if g := atoiDefault(r.FormValue("for"), 0); g > 0 && expect > 0 {
		// for one guest at a split table (they asked the waiter, not their phone)
		bill, ticket, err = s.Store.AddForGuest(expect, g, lines, now)
	} else {
		bill, ticket, err = s.Store.AddToBill(table, expect, lines, ref, deposit, now)
	}
	if q, ok := soldOutQuery(err); ok {
		redirectMsg(w, r, orderPath(table), q, "soldout")
		return
	}
	if err != nil {
		redirectMsg(w, r, orderPath(table), nil, orderError(err))
		return
	}
	s.live.publish()
	msg := "addeddrinks"
	if ticket != nil {
		msg = "addedfood"
		// Something of this order waits for the go ("Vänta varmt").
		for _, l := range bill.Lines {
			if tk, ok := s.Store.TicketsForBill(bill.ID)[l.TicketID]; ok && tk.Held && !l.AddedAt.Before(now) {
				msg = "addedheld"
				break
			}
		}
	}
	redirectMsg(w, r, orderPath(table), nil, msg)
}

// orderLine changes a line's price (and for drinks the quantity), or removes it.
func (s *Server) orderLine(w http.ResponseWriter, r *http.Request) {
	table := tableFromPath(r)
	if table == 0 || !s.parseStaffForm(w, r) {
		if table == 0 {
			s.notFound(w, r)
		}
		return
	}
	billID := atoiDefault(r.FormValue("bill"), 0)
	lineID := atoiDefault(r.PathValue("line"), 0)
	before, _ := s.Store.BillByID(billID)
	var old db.BillLine
	for _, l := range before.Lines {
		if l.ID == lineID {
			old = l
		}
	}
	if r.PathValue("action") == "remove" {
		if _, err := s.Store.RemoveLine(billID, lineID); err != nil {
			redirectMsg(w, r, orderPath(table), nil, orderError(err))
			return
		}
		s.logActivity(r, table, "took off the bill", fmt.Sprintf("%d× %s · %s", old.Qty, old.Name, euro(old.Total())))
		s.live.publish()
		redirectMsg(w, r, orderPath(table), nil, "lineremoved")
		return
	}
	price, err := parseEuro(r.FormValue("price"))
	qty := atoiDefault(r.FormValue("qty"), 0)
	if err != nil || qty < 1 || qty > maxQty {
		redirectMsg(w, r, orderPath(table), nil, "invalid")
		return
	}
	bill, berr := s.Store.BillByID(billID)
	note := ""
	if berr == nil {
		for _, l := range bill.Lines {
			if l.ID == lineID {
				note = l.Note // the note isn't edited here
			}
		}
	}
	if err := s.Store.UpdateLine(billID, lineID, price, qty, note); err != nil {
		if q, ok := soldOutQuery(err); ok {
			redirectMsg(w, r, orderPath(table), q, "soldout")
			return
		}
		redirectMsg(w, r, orderPath(table), nil, orderError(err))
		return
	}
	if price != old.PriceCents || qty != old.Qty {
		s.logActivity(r, table, "changed a price", fmt.Sprintf("%s: %d× %s → %d× %s", old.Name, old.Qty, euro(old.PriceCents), qty, euro(price)))
	}
	if pct := atoiDefault(r.FormValue("discount"), old.DiscountPct); pct != old.DiscountPct {
		if err := s.Store.SetDiscount(billID, lineID, pct); err != nil {
			redirectMsg(w, r, orderPath(table), nil, orderError(err))
			return
		}
		after := old
		after.PriceCents, after.Qty, after.DiscountPct = price, qty, pct
		s.logActivity(r, table, "gave a discount", fmt.Sprintf("%d× %s −%d %% (−%s)", qty, old.Name, pct, euro(after.Discount())))
	}
	s.live.publish()
	redirectMsg(w, r, orderPath(table), nil, "linesaved")
}

// orderBillAction: show the bill (billed), paid, move to another table, or void.
func (s *Server) orderBillAction(w http.ResponseWriter, r *http.Request) {
	table := tableFromPath(r)
	if table == 0 || !s.parseStaffForm(w, r) {
		if table == 0 {
			s.notFound(w, r)
		}
		return
	}
	now := s.Now()
	billID := atoiDefault(r.FormValue("bill"), 0)
	switch r.PathValue("action") {
	case "paytable": // a split table: everything still to pay, in one go
		method := r.FormValue("method")
		cents, err := s.Store.PayTable(billID, method, now)
		if err != nil {
			msg := orderError(err)
			if errors.Is(err, db.ErrPartlyPaid) {
				msg = "guestpartly"
			}
			redirectMsg(w, r, orderPath(table), nil, msg)
			return
		}
		s.logActivity(r, table, paidWhat(method), euro(cents)+" ("+st(langEN, "whole table")+")")
		s.live.publish()
		redirectMsg(w, r, "/admin/orders", nil, "allpaid")
	case "discount":
		pct := atoiDefault(r.FormValue("pct"), -1)
		n, err := s.Store.DiscountBill(billID, pct)
		if err != nil {
			redirectMsg(w, r, orderPath(table), nil, orderError(err))
			return
		}
		if n > 0 {
			b, _ := s.Store.BillByID(billID)
			s.logActivity(r, table, "gave a discount on the whole bill", fmt.Sprintf("−%d %% (−%s)", pct, euro(b.Discounts())))
		}
		s.live.publish()
		redirectMsg(w, r, orderPath(table), nil, "discounted")
	case "fire": // "Varma fram" / "Fram efterrätt"
		course := atoiDefault(r.FormValue("course"), db.CourseMain)
		if _, err := s.Store.FireCourse(billID, course, now); err != nil {
			redirectMsg(w, r, orderPath(table), nil, "nothingheld")
			return
		}
		s.live.publish()
		msg := "firedmains"
		if course == db.CourseDessert {
			msg = "fireddessert"
		}
		redirectMsg(w, r, orderPath(table), nil, msg)
	case "message": // "Fritt köksmeddelande"
		if _, err := s.Store.KitchenMessage(table, clip(oneLine(r.FormValue("text")), db.MaxKitchenMessage), now); err != nil {
			redirectMsg(w, r, orderPath(table), nil, "invalid")
			return
		}
		s.live.publish()
		redirectMsg(w, r, orderPath(table), nil, "messagesent")
	case "billed":
		if err := s.Store.MarkBilled(billID, now); err != nil {
			redirectMsg(w, r, orderPath(table), nil, orderError(err))
			return
		}
		s.live.publish()
		http.Redirect(w, r, orderPath(table)+"/bill?bill="+strconv.Itoa(billID), http.StatusSeeOther)
	case "pay":
		paid, err := s.Store.PayBill(billID, r.FormValue("method"), now)
		if err != nil {
			redirectMsg(w, r, orderPath(table), nil, orderError(err))
			return
		}
		what, amount := paidWhat(paid.PaidBy), paid.ToPay()
		if n := len(paid.Payments); n > 0 { // the rest of a bill paid in parts
			what, amount = paidWhat(paid.Payments[n-1].Method), paid.Payments[n-1].Cents
		}
		s.logActivity(r, table, what, euro(amount)+" ("+st(langEN, "whole bill")+")")
		s.live.publish()
		redirectMsg(w, r, "/admin/orders", nil, "billpaid")
	case "part":
		s.payPart(w, r, table, billID)
	case "undopay":
		s.undoPart(w, r, table, billID)
	case "move":
		to := atoiDefault(r.FormValue("to"), 0)
		if err := s.Store.MoveBill(billID, to); err != nil {
			redirectMsg(w, r, orderPath(table), nil, orderError(err))
			return
		}
		s.logActivity(r, table, "moved the bill", stf(langEN, "to table %d", to))
		s.live.publish()
		redirectMsg(w, r, orderPath(to), nil, "billmoved")
	case "void":
		before, _ := s.Store.BillByID(billID)
		if err := s.Store.VoidBill(billID); err != nil {
			redirectMsg(w, r, orderPath(table), nil, orderError(err))
			return
		}
		s.logActivity(r, table, "voided the bill", euro(before.Total()))
		s.live.publish()
		redirectMsg(w, r, "/admin/orders", nil, "billvoided")
	default:
		s.notFound(w, r)
	}
}

// orderBillView is the bill for the guests: big, clear, printable.
func (s *Server) orderBillView(w http.ResponseWriter, r *http.Request) {
	table := tableFromPath(r)
	bill, err := s.Store.BillByID(atoiDefault(r.URL.Query().Get("bill"), 0))
	if table == 0 || err != nil || bill.Table != table {
		s.notFound(w, r)
		return
	}
	lang := langOf(r)
	at := s.Now()
	if bill.PaidAt != nil {
		at = *bill.PaidAt
	}
	rv := s.receiptFor(bill, 0, s.publicBase(r), s.Now())
	rv.When = shortDateL(lang, schedule.FormatDate(at)) + " " + at.In(schedule.Location).Format("15:04")
	s.render(w, r, "order_bill", http.StatusOK, M{
		"Admin": true, "Bare": true, "Title": stf(lang, "Bill, table %d", table), "R": rv,
		"Table": table, "Bill": bill, "When": shortDateL(lang, schedule.FormatDate(at)) + " " + at.In(schedule.Location).Format("15:04"),
		"No": billNumbers(s.Store.BillsForTable(table))[bill.ID],
		"QR": true, "QRURL": s.publicBase(r) + "/r/" + bill.Ref,
	})
}

// ---------- A table's history ----------

// historyDays is how many days one history page shows.
const historyDays = 14

// historyBill is one bill in a table's history.
type historyBill struct {
	db.Bill
	No       int          // the table's 1st, 2nd, 3rd... bill that day (0 = voided)
	From, To string       // "18:05", "19:20"
	State    string       // "open", "paid" or "voided"
	Shares   []splitGuest // split bills: each guest's part
}

// historyDay is one day of a table's history.
type historyDay struct {
	Date  string
	Bills []historyBill // in the order they were opened
	Paid  int           // bills paid that day
	Total int           // what they paid
}

// billNumbers numbers each bill among its table's bills on the same day:
// the day's first bill is 1, the next 2. Voided bills get no number.
// bills must be oldest first, all from one table.
func billNumbers(bills []db.Bill) map[int]int {
	nums := map[int]int{}
	perDay := map[string]int{}
	for _, b := range bills {
		if b.Voided {
			continue
		}
		d := schedule.FormatDate(b.OpenedAt)
		perDay[d]++
		nums[b.ID] = perDay[d]
	}
	return nums
}

// orderHistory is everything a table ever ordered: one card per bill, the
// newest day first, 14 days per page.
func (s *Server) orderHistory(w http.ResponseWriter, r *http.Request) {
	table := tableFromPath(r)
	if table == 0 {
		s.notFound(w, r)
		return
	}
	lang := langOf(r)
	bills := s.Store.BillsForTable(table)
	nums := billNumbers(bills)
	clock := func(t time.Time) string { return t.In(schedule.Location).Format("15:04") }
	var days []*historyDay
	byDate := map[string]*historyDay{}
	paidAll, totalAll := 0, 0
	for _, b := range bills {
		v := historyBill{Bill: b, No: nums[b.ID], From: clock(b.OpenedAt), State: "paid"}
		if b.IsSplit() {
			pay, items, dep := b.Parts()
			for i, sh := range b.Split {
				v.Shares = append(v.Shares, splitGuest{Guest: sh.Guest, Pay: pay[i], Deposit: dep[i], Items: items[i], Paid: sh.PaidAt != nil, PaidBy: sh.PaidBy})
			}
		}
		switch {
		case b.Voided:
			v.State = "voided"
		case b.PaidAt == nil:
			v.State = "open"
		default:
			v.To = clock(*b.PaidAt)
		}
		date := schedule.FormatDate(b.OpenedAt)
		day := byDate[date]
		if day == nil {
			day = &historyDay{Date: date}
			byDate[date] = day
			days = append(days, day)
		}
		day.Bills = append(day.Bills, v)
		if v.State == "paid" {
			sum := 0
			for _, c := range b.Takings() {
				sum += c
			}
			day.Paid++
			day.Total += sum
			paidAll++
			totalAll += sum
		}
	}
	// Newest day first; pages of 14 days going back with ?before=<date>.
	for i, j := 0, len(days)-1; i < j; i, j = i+1, j-1 {
		days[i], days[j] = days[j], days[i]
	}
	before := r.URL.Query().Get("before")
	start := 0
	if before != "" {
		for start < len(days) && days[start].Date >= before {
			start++
		}
	}
	end := min(start+historyDays, len(days))
	page := days[start:end]
	older := ""
	if end < len(days) && len(page) > 0 {
		older = page[len(page)-1].Date
	}
	s.render(w, r, "order_history", http.StatusOK, M{
		"Admin": true, "Nav": "orders", "Title": stf(lang, "Table %d · history", table),
		"Table": table, "Days": page, "Older": older, "Paged": before != "",
		"PaidAll": paidAll, "TotalAll": totalAll,
	})
}

// ---------- The chef's screen ----------

type ticketView struct {
	db.Ticket
	Time    string
	Minutes int
	Age     string // "", "late" (15+ min), "very-late" (25+ min)
	Key     string // changes when the ticket changes, so the screen chimes

	// Takeaway orders paid online.
	New       bool   // the chef hasn't tapped Seen yet
	Due       bool   // time to start cooking (an hour before pickup)
	PickupAt  string // "18:00"
	PickupDay string // "Sat 31 Oct" when it isn't today
	Until     int    // minutes until pickup
	DueClock  string // when it comes back on the screen
}

func (s *Server) adminKitchen(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	var open, later []ticketView
	active, upcoming := s.Store.KitchenNow(now)
	for _, t := range active {
		v := ticketView{Ticket: t, Time: t.Since().In(schedule.Location).Format("15:04"), Minutes: minutesSince(t.Since(), now)}
		switch {
		case v.Minutes >= 25:
			v.Age = "very-late"
		case v.Minutes >= 15:
			v.Age = "late"
		}
		cancelled := 0
		for _, it := range t.Items {
			if it.Cancelled {
				cancelled++
			}
		}
		v.Key = strconv.Itoa(t.ID) + "-" + strconv.Itoa(cancelled)
		if t.IsTakeaway() {
			s.takeawayView(lang, &v, now)
		}
		open = append(open, v)
	}
	for _, t := range upcoming {
		v := ticketView{Ticket: t}
		s.takeawayView(lang, &v, now)
		later = append(later, v)
	}
	var waiting []ticketView
	for _, t := range s.Store.HeldTickets() {
		waiting = append(waiting, ticketView{Ticket: t, Time: t.At.In(schedule.Location).Format("15:04"), Minutes: minutesSince(t.At, now)})
	}
	var done []ticketView
	for _, t := range s.Store.RecentDone(4) {
		at := *t.DoneAt
		if t.ServedAt != nil {
			at = *t.ServedAt
		}
		done = append(done, ticketView{Ticket: t, Time: clockOf(at)})
	}
	data := M{
		"Admin": true, "Bare": true, "Title": st(lang, "Kitchen"),
		"Tickets": open, "Done": done, "Later": later, "Dishes": s.kitchenDishes(now), "Waiting": waiting,
		"Ready": s.readyFood(now),
	}
	if r.URL.Query().Get("part") == "tickets" {
		s.renderBlock(w, r, "admin_kitchen", "tickets", http.StatusOK, data)
		return
	}
	s.render(w, r, "admin_kitchen", http.StatusOK, data)
}

func (s *Server) kitchenAction(w http.ResponseWriter, r *http.Request) {
	id := atoiDefault(r.PathValue("id"), 0)
	undo := r.PathValue("action") == "undo"
	var err error
	switch r.PathValue("action") {
	case "done", "undo":
		err = s.Store.TicketDone(id, undo, s.Now())
	case "served": // the food left the kitchen for its table
		err = s.Store.TicketServed(id, false, s.Now())
	case "seen": // a new takeaway order: the chef saw it
		err = s.Store.TicketSeen(id, s.Now())
	case "fire": // the chef starts a waiting course himself
		err = s.Store.FireTicket(id, s.Now())
	default:
		s.notFound(w, r)
		return
	}
	if err == nil {
		s.live.publish()
	}
	if r.Header.Get("X-Live") == "1" {
		if err != nil {
			http.Error(w, st(langOf(r), "That order changed in the meantime."), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.FormValue("back") == "orders" {
		http.Redirect(w, r, "/admin/orders", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/kitchen", http.StatusSeeOther)
}

// takeawayView fills in a takeaway ticket's pickup details. Its colour
// follows the pickup time: orange 30 minutes before, red 10 minutes before.
func (s *Server) takeawayView(lang string, v *ticketView, now time.Time) {
	pickup := v.Pickup.In(schedule.Location)
	v.New, v.Due = v.SeenAt == nil, !now.Before(v.DueAt())
	v.PickupAt, v.DueClock = pickup.Format("15:04"), v.DueAt().In(schedule.Location).Format("15:04")
	if day := schedule.FormatDate(pickup); day != schedule.FormatDate(schedule.Today(now)) {
		v.PickupDay = shortDateL(lang, day)
	}
	v.Until = int(pickup.Sub(now) / time.Minute)
	v.Age = ""
	switch {
	case !v.Due:
	case v.Until <= 10:
		v.Age = "very-late"
	case v.Until <= 30:
		v.Age = "late"
	}
	state := "new"
	if !v.New {
		state = "due" // back on the screen to start cooking: chime again
	}
	v.Key += "-" + state
}

// dishView is a dish on the kitchen's "Sold out" list.
type dishView struct {
	ID      int
	Name    string
	Cat     string
	SoldOut bool
}

// kitchenDishes: the food on the menu, for the chef to mark sold out.
func (s *Server) kitchenDishes(now time.Time) []dishView {
	today := schedule.FormatDate(schedule.Today(now))
	var out []dishView
	for _, m := range s.Store.Menu(false) {
		if m.Active(today) && !m.Drink {
			out = append(out, dishView{ID: m.ID, Name: m.Name, Cat: m.Category, SoldOut: m.Gone(today)})
		}
	}
	return out
}

// menuSoldOut: the chef taps a dish on the kitchen screen: sold out for the
// rest of today (off the phone menu, takeaway and the staff order form), or
// back on.
func (s *Server) menuSoldOut(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	_, err := s.Store.SetSoldOut(atoiDefault(r.PathValue("id"), 0), r.FormValue("soldout") == "1", s.Now())
	if err == nil {
		s.live.publish() // guests' menus and staff screens update
	}
	if r.Header.Get("X-Live") != "" {
		if err != nil {
			http.Error(w, st(langOf(r), "That order changed in the meantime."), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/admin/kitchen", http.StatusSeeOther)
}
