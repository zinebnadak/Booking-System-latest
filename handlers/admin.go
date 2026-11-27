package handlers

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// Short messages shown after an action (?msg=...).
var flashes = map[string]string{
	"stockbad":        "How many are left must be a number from 0 to 9999.",
	"ingredientbad":   "An ingredient needs a name, and amounts as whole numbers (for example 5000 for 5 kg in grams).",
	"ingredienttaken": "There is already an ingredient with that name.",
	"discounted":      "Discount given.",
	"pinset":          "PIN saved. They can log in at the till with their number and PIN.",
	"pinbad":          "A PIN is 4 to 6 digits.",
	"tillon":          "This device is a till now: the keypad is at /till.",
	"tilloff":         "This device isn't a till any more.",
	"shiftfixed":      "Shift saved.",
	"shiftbad":        "A shift ends after it starts, and lasts at most 16 hours.",
	"cardused":        "That card has been used, so it can't be voided.",
	"cardvoided":      "The card is voided. Give the money back the way it was paid.",
	"partpaid":        "Payment added. The rest is still to pay.",
	"guestpart":       "Payment added. The rest of this guest's part is still to pay.",
	"guestpartly":     "A guest who paid some of their part already pays the rest on their own.",
	"partundone":      "Payment taken back.",
	"partlypaid":      "Part of this bill is already paid. Take those payments back first (under Other ways to pay), or pay the rest.",
	"nocard":          "No gift card or lunch booklet with that code. Check the code on the card.",
	"cardempty":       "There's nothing left on that card.",
	"toomanycoupons":  "The booklet doesn't have that many coupons left.",
	"extracoupons":    "Fewer coupons already pay what's left. Use fewer, so none are wasted.",
	"badamount":       "Check the amount: more than 0 and not more than what's left.",
	"addedheld":       "Added. The starters went to the chef; the rest waits for your go.",
	"firedmains":      "The chef has the mains now.",
	"fireddessert":    "The chef has the dessert now.",
	"nothingheld":     "Nothing is waiting: it has already gone to the chef.",
	"messagesent":     "Message sent to the kitchen.",
	"cancelled":       "Booking cancelled. The deposit was kept.",
	"refunded":        "Booking cancelled and the deposit refunded.",
	"released":        "Unpaid booking cancelled.",
	"saved":           "Saved.",
	"deleted":         "Deleted.",
	"refundfailed":    "Stripe couldn't refund this payment. Nothing was changed. Try again, or refund it in the Stripe dashboard.",
	"invalid":         "Please check the form. Something was missing or invalid.",
	"arrived":         "Checked in.",
	"arrivedfinal":    "This guest is checked in, so the booking can't be cancelled.",
	"moved":           "Moved to the new table.",
	"tablebusy":       "That table is taken at that time. Pick another table.",
	"dishon":          "Dish is on the menu.",
	"dishoff":         "Dish is hidden from the menu.",
	"phonebooked":     "Phone booking saved.",
	"pickedup":        "Marked as picked up.",
	"pickedfinal":     "This order was picked up, so it can't be cancelled.",
	"duplicate":       "This guest already has a booking at that time.",
	"waitremoved":     "Removed from the waiting list.",
	"cancelfree":      "Booking cancelled.",

	// Tables screen and table buttons
	"dealstarted":  "3 meals started. Meal 1 is with the kitchen.",
	"mealserved":   "Meal served.",
	"mealsent":     "Sent to the kitchen.",
	"waiterset":    "Saved who looks after the table.",
	"guestsset":    "Saved how many sit at the table.",
	"undone":       "Undone.",
	"undolate":     "Too late to undo (more than a minute, or the chef finished it). Ask the manager.",
	"pinwrong":     "That isn't the manager's PIN.",
	"mgrpinset":    "The manager's PIN is saved.",
	"mgrpinoff":    "The manager's PIN is off.",
	"kitchenonly":  "The kitchen screen is for the chef. You'll see here when food is ready to pick up.",
	"askmanager":   "Only the manager can take things off a bill, lower a price or give a discount. Ask the manager: open the dish and tap Ask the manager.",
	"asksent":      "Sent to the manager. It stays on the bill until the manager takes it off.",
	"askdropped":   "It stays on the bill.",
	"noreason":     "Say why it should come off.",
	"skipped":      "Skipped for now. The bill waits for the manager, and the table is free.",
	"billback":     "The bill is back on the table.",
	"writtenoff":   "Written off as unpaid. It shows on the charts.",
	"guestsbad":    "Pick how many people sit at the table: 1 to 99.",
	"tablecleared": "Table cleared.",
	"notyet":       "OK, they're still eating.",
	"answered":     "Done.",
	"tablestale":   "That table changed in the meantime. Here is how it is now.",
	"newcode":      "New code made. Put it in the button: the old code no longer works.",

	// Orders at the tables
	"addedfood":     "Added to the bill. The chef has the food.",
	"dealbad":       "Check how many guests, and give every meal a dish (and the order a name if you save it).",
	"addeddrinks":   "Added to the bill.",
	"lineremoved":   "Taken off the bill.",
	"linesaved":     "Saved.",
	"billpaid":      "Paid. The table is free.",
	"splitset":      "Split. Now tap who had what.",
	"guestadded":    "Guest added. Tap what they had.",
	"unsplit":       "The bill isn't split any more.",
	"assigned":      "Saved.",
	"spliteven":     "Split evenly between everyone.",
	"separated":     "Now one line each: give each to whoever had it.",
	"guestpaid":     "Paid. Next guest.",
	"paidtogether":  "Paid together.",
	"guestundone":   "Payment taken back.",
	"allpaid":       "Everyone paid. The table is free.",
	"woltdone":      "Picked up by the Wolt courier. The order is paid by Wolt.",
	"woltname":      "Write the name on the Wolt order.",
	"woltfull":      "Too many Wolt orders are open. Finish one first.",
	"woltcancelled": "The Wolt order is cancelled.",
	"cashsaved":     "Cash count saved.",
	"plansaved":     "Saved. Bookings and Orders use the new tables now.",
	"notable":       "There's no table with that number.",
	"rotaadded":     "Shift added.",
	"rotaremoved":   "Shift removed.",
	"rotabad":       "Pick a person, a day and the times.",
	"rotacopied":    "Last week's shifts are copied into this week (the ones already planned were skipped).",
	"joined":        "The tables are joined: everything is on this bill now.",
	"linemoved":     "Moved to the other table's bill.",
	"joinbusy":      "Can't do that now: a bill is split, partly paid, being paid on a phone, or has 3 meals running.",
	"joindeposits":  "Both tables have an online deposit. Pay one of the bills first.",
	"jointable":     "Pick another table.",
	"seatssaved":    "Saved. Online bookings use the new number of seats.",
	"seatsbad":      "A table has 1 to 30 seats.",
	"bizsaved":      "Saved. Receipts show the business ID now.",
	"bizbad":        "That isn't a business ID. It looks like 1234567-8 (Y-tunnus / FO-nummer).",
	"shiftstarted":  "Your shift has started. Have a good one!",
	"shiftended":    "Your shift has ended. Thanks!",
	"shiftnone":     "You weren't on a shift.",
	"cashbad":       "Check the amounts: numbers in euros, like 150 or 152,50.",
	"woltpctsaved":  "Wolt's cut saved.",
	"woltpctbad":    "Wolt's cut is a whole number from 0 to 60 %.",
	"phonepaying":   "A guest is paying for that on their phone right now. Wait a moment.",
	"qrguests":      "Guests are on this bill with their phones, so they can't be taken off it.",
	"paidonline":    "That was paid online. To give it back, refund it in Stripe.",
	"qropen":        "QR orders are on for this table.",
	"qrclosed":      "QR orders are off for this table.",
	"qrnew":         "New code made. Print the new sticker: the old one doesn't work any more.",
	"photosaved":    "Photo saved. Guests see it on the menu now.",
	"staffadded":    "Waiter added. They can log in now.",
	"staffpassword": "New password saved. They're logged out everywhere until they use it.",
	"staffremoved":  "Waiter removed. They're logged out everywhere.",
	"usertaken":     "That username is taken. Pick another one.",
	"staffbad":      "Fill in the name and username, and a password of at least 8 characters.",
	"photoremoved":  "Photo removed.",
	"photobig":      "That photo is too big. Try one under 15 MB.",
	"photonone":     "Pick a photo first.",
	"photobad":      "That file isn't a photo we can use. Try a JPEG or PNG.",
	"orderwaiting":  "That guest has a phone order waiting. Accept or reject it first.",
	"checkon":       "Phone orders now wait for a waiter to accept them (or go through by themselves after 5 minutes).",
	"checkoff":      "Phone orders go straight to the kitchen again.",
	"sharepaid":     "That guest already paid, so their part can't change.",
	"unassigned":    "Some things aren't given to anyone yet. Give them out first.",
	"billsplit":     "This bill is split: each guest pays their part on the Split page.",
	"billmoved":     "Bill moved to this table.",
	"billvoided":    "Bill voided.",
	"billstale":     "That bill changed in the meantime (paid or moved). Here is how it is now.",
	"sentalready":   "The chef already has that dish, so only its price can change. To change it, take it off and add it again.",
	"nothingadded":  "Nothing added: pick at least one dish or drink.",
	"billempty":     "The bill is empty. Void it instead.",
	"tabletaken":    "That table already has an open bill.",
}

// flashText is the message for ?msg=..., in the visitor's language.
func flashText(r *http.Request) string {
	switch r.URL.Query().Get("msg") {
	case "soldout":
		return soldOutText(r) // names what ran out
	case "needprice":
		if item := clip(oneLine(r.URL.Query().Get("item")), maxNameLen); item != "" {
			return stf(langOf(r), "%s has no price on the menu yet. Type its price under the dish, then add again.", item)
		}
	}
	if f, ok := flashes[r.URL.Query().Get("msg")]; ok {
		return st(langOf(r), f)
	}
	return ""
}

func redirectMsg(w http.ResponseWriter, r *http.Request, path string, q url.Values, msg string) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("msg", msg)
	http.Redirect(w, r, path+"?"+q.Encode(), http.StatusSeeOther)
}

// ---------- Day view ----------

type bookingRow struct {
	db.Booking
	Status       db.Status
	End          string
	RefundPolicy bool   // true if cancelling now should refund (table, 24h+ before)
	CanCheckIn   bool   // the manager can press "Arrived" now
	NoShowAt     string // when it turns into a no-show, e.g. "18:20"
	MoveTo       []int  // tables it can be moved to
	Current      bool   // happening right now
	Past         bool   // the booking's time is over
	Group        string // for the filter tabs: waiting, arrived, noshow, cancelled, unpaid
	History      db.GuestHistory
	OccasionText string
	Highlight    bool // just created: flash it
}

func autoRefresh(today bool) int {
	if today {
		return 60
	}
	return 0
}

func groupOf(st db.Status) string {
	switch st {
	case db.StatusPaid:
		return "waiting"
	case db.StatusArrived:
		return "arrived"
	case db.StatusNoShow:
		return "noshow"
	case db.StatusPending:
		return "unpaid"
	}
	return "cancelled"
}

// tableCard is one table in the "Tables" overview.
type tableCard struct {
	No       int
	Room     string
	Seats    int
	Bookings []bookingRow
	Busy     bool   // someone is (or should be) sitting there right now
	State    string // free, seated, expected, holding (today only)
	Who      string // who is there / expected
	Detail   string // "until 20:00", "no-show at 18:20", "next 19:30"
	Ref      string // booking to jump to
	Orders   bool   // seated on Orders (an open bill, no booking now)
}

// bookedCards keeps the tables that have bookings that day (the rest are
// summed up as "other tables": with 50 tables, a card each would be a wall).
func bookedCards(cards []tableCard) []tableCard {
	var out []tableCard
	for _, c := range cards {
		if len(c.Bookings) > 0 || c.Orders {
			out = append(out, c)
		}
	}
	return out
}

func quietTables(cards []tableCard) int { return len(cards) - len(bookedCards(cards)) }

// active reports whether a table booking still matters on the floor plan.
func active(st db.Status) bool {
	return st == db.StatusPaid || st == db.StatusArrived || st == db.StatusPending
}

type slotUse struct {
	Time string
	Used int
	Pct  int
}

func (s *Server) adminDay(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	date := r.URL.Query().Get("date")
	d, err := schedule.ParseDate(date)
	if err != nil {
		d = schedule.Today(now)
		date = schedule.FormatDate(d)
	}
	var tables, takeaway []bookingRow
	cards := make([]tableCard, schedule.TableCount())
	for i, t := range schedule.Tables() {
		cards[i].No, cards[i].Room, cards[i].Seats = t.No, t.Room, t.Seats
	}
	guests, deposits, noShows := 0, 0, 0
	dayBookings := s.Store.BookingsOn(date, now)
	history := s.Store.HistoryFor(dayBookings, now)
	hl := r.URL.Query().Get("hl")
	for _, b := range dayBookings {
		row := bookingRow{Booking: b, Status: b.EffectiveStatus(now), End: b.EndClock(),
			History: history[b.Ref], OccasionText: occasionText(lang, b.Occasion), Highlight: b.Ref == hl}
		if b.Kind == db.KindTable {
			start := b.Start()
			end := start.Add(b.Duration())
			row.RefundPolicy = schedule.RefundEligible(b.Date, b.Time, now)
			row.NoShowAt = b.NoShowTime().In(schedule.Location).Format("15:04")
			row.Current = !now.Before(start) && now.Before(end)
			row.Past = !now.Before(end)
			// "Arrived" can be pressed from 2 hours before until the booking
			// ends. Once it's a no-show, "Arrived late" takes over.
			row.CanCheckIn = row.Status == db.StatusPaid &&
				now.After(start.Add(-2*time.Hour)) && now.Before(end)
			if row.Status == db.StatusPaid || row.Status == db.StatusArrived {
				row.MoveTo = s.Store.TablesFreeFor(b.Ref, now)
			}
			tables = append(tables, row)
			switch row.Status {
			case db.StatusPaid, db.StatusArrived:
				guests += b.PartySize
			case db.StatusNoShow:
				noShows++
			}
			if i := schedule.TableOrder(b.TableNo); i >= 0 && active(row.Status) {
				c := &cards[i]
				c.Bookings = append(c.Bookings, row)
				if row.Current {
					c.Busy = true
				}
			}
		} else {
			takeaway = append(takeaway, row)
		}
		if b.Status == db.StatusPaid {
			deposits += b.DepositCents
		}
	}

	// What each table is doing right now (today only).
	waiting, arrived := 0, 0
	counts := map[string]int{"all": len(tables)}
	for i := range tables {
		g := groupOf(tables[i].Status)
		tables[i].Group = g
		counts[g]++
		switch tables[i].Status {
		case db.StatusPaid:
			waiting++
		case db.StatusArrived:
			arrived++
		}
	}
	openAt := map[int]db.Bill{}
	if date == schedule.FormatDate(schedule.Today(now)) {
		for _, b := range s.Store.OpenBills() {
			if b.Wolt == "" {
				openAt[b.Table] = b
			}
		}
	}
	for i := range cards {
		c := &cards[i]
		c.State = "free"
		for _, row := range c.Bookings {
			if row.Current {
				c.Who, c.Ref = fmt.Sprintf("%s · %d", row.Name, row.PartySize), row.Ref
				switch row.Status {
				case db.StatusArrived:
					c.State, c.Detail = "seated", stf(lang, "until %s", row.End)
				case db.StatusPaid:
					c.State, c.Detail = "expected", stf(lang, "no-show at %s", row.NoShowAt)
					if row.LateMinutes > 0 {
						c.Detail = stf(lang, "%d min late", row.LateMinutes) + " · " + c.Detail
					}
				default:
					c.State, c.Detail = "holding", st(lang, "waiting for payment")
				}
				break
			}
		}
		if b, ok := openAt[c.No]; ok && c.State == "free" { // seated on Orders without a booking
			c.State, c.Orders = "seated", true
			c.Who = st(lang, "Seated on Orders")
			if b.Guests > 0 {
				c.Who += " · " + strconv.Itoa(b.Guests)
			}
			c.Detail = stf(lang, "since %s", clockOf(b.OpenedAt))
		}
		if c.State == "free" || c.Orders {
			for _, row := range c.Bookings {
				if row.Start().After(now) {
					next := stf(lang, "next %s", row.Time) + " · " + row.Name
					if c.Orders {
						next = c.Detail + " · " + next
					}
					c.Detail, c.Ref = next, row.Ref
					break
				}
			}
		}
	}

	// How many tables are in use at each start time (for the bar chart).
	var usage []slotUse
	if h, open := schedule.HoursFor(d); open {
		clocks := []string{}
		for m := h.Open; m <= h.Close-int(schedule.BookingDuration/time.Minute); m += int(schedule.TableSlotStep / time.Minute) {
			clocks = append(clocks, schedule.Clock(m))
		}
		free := s.Store.FreeTables(date, clocks, 0, false, now)
		for _, c := range clocks {
			used := schedule.TableCount() - free[c]
			usage = append(usage, slotUse{Time: c, Used: used, Pct: used * 100 / schedule.TableCount()})
		}
	}
	reason, closed := s.Store.ClosedReason(date)
	_, openDay := schedule.HoursFor(d)
	var slots []staffSlot
	if !closed && !d.Before(schedule.Today(now)) {
		slots = s.staffSlots(date, now)
	}

	s.render(w, r, "admin_day", 200, M{
		"Title":       capitalize(shortDateL(lang, date)),
		"Admin":       true,
		"Nav":         "day",
		"Date":        date,
		"Prev":        schedule.FormatDate(d.AddDate(0, 0, -1)),
		"Next":        schedule.FormatDate(d.AddDate(0, 0, 1)),
		"Today":       schedule.FormatDate(schedule.Today(now)),
		"Tables":      tables,
		"Cards":       bookedCards(cards),
		"IsToday":     date == schedule.FormatDate(schedule.Today(now)),
		"NoShows":     noShows,
		"Waiting":     waiting,
		"Counts":      counts,
		"AutoRefresh": autoRefresh(date == schedule.FormatDate(schedule.Today(now))),
		"ArrivedN":    arrived,
		"Toast":       flashText(r),
		"NoShowMin":   int(schedule.NoShowAfter / time.Minute),
		"Takeaway":    takeaway,
		"Guests":      guests,
		"Deposits":    deposits,
		"Usage":       usage,
		"TableCount":  schedule.TableCount(),
		"QuietTables": quietTables(cards),
		"Closed":      closed || !openDay,
		"ClosedNote":  reason,
		"PayEnabled":  s.Pay.Enabled(),
		"HasCalendar": s.Cfg.CalendarToken != "",
		"StaffSlots":  slots,
		"StaffFree":   anyFree(slots),
		"TableNos":    tableOptions(),
		"Occasions":   occasions,
		"Waitlist":    s.Store.Waitlist(date),
		"Sizes":       []int{1, 2, 3, 4, 5, 6},
	})
}

// adminCancel cancels a booking, refunding the deposit if asked to.
func (s *Server) adminCancel(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	b, err := s.Store.Get(ref)
	if err != nil {
		s.notFound(w, r)
		return
	}
	back := url.Values{"date": {b.Date}}
	switch b.Status {
	case db.StatusPending, db.StatusExpired:
		// Never paid: just release it.
		if _, _, err := s.Store.Cancel(ref, false, "staff", s.Now()); err != nil {
			log.Printf("cancel %s: %v", ref, err)
		}
		if b.Kind == db.KindTable {
			s.notifyWaitlist(b.Date)
		}
		redirectMsg(w, r, "/admin/day", back, "released")
		return
	case db.StatusPaid:
		if b.ArrivedAt != nil {
			// Guests who came can't be cancelled: their deposit is on the bill.
			redirectMsg(w, r, "/admin/day", back, "arrivedfinal")
			return
		}
		if b.PickedUpAt != nil {
			redirectMsg(w, r, "/admin/day", back, "pickedfinal")
			return
		}
	default:
		redirectMsg(w, r, "/admin/day", back, "cancelled")
		return
	}
	// Takeaway deposits are non-refundable: cancelBooking never refunds
	// them, even if someone sends refund=yes by hand.
	updated, err := s.cancelBooking(r.Context(), b, r.FormValue("refund") == "yes", "staff")
	switch {
	case errors.Is(err, errRefundFailed):
		redirectMsg(w, r, "/admin/day", back, "refundfailed")
	case err != nil:
		log.Printf("cancel %s: %v", ref, err)
		redirectMsg(w, r, "/admin/day", back, "invalid")
	case updated.Status == db.StatusRefunded:
		s.logActivity(r, updated.TableNo, "refunded a deposit", fmt.Sprintf("#%d · %s", updated.ID, euro(updated.DepositCents)))
		redirectMsg(w, r, "/admin/day", back, "refunded")
	case updated.DepositCents == 0:
		s.logActivity(r, updated.TableNo, "cancelled a booking", fmt.Sprintf("#%d", updated.ID))
		redirectMsg(w, r, "/admin/day", back, "cancelfree")
	default:
		s.logActivity(r, updated.TableNo, "cancelled a booking", fmt.Sprintf("#%d · %s %s", updated.ID, st(langEN, "deposit kept"), euro(updated.DepositCents)))
		redirectMsg(w, r, "/admin/day", back, "cancelled")
	}
}

// adminArrived checks a guest in. There is no undo.
func (s *Server) adminArrived(w http.ResponseWriter, r *http.Request) {
	now := s.Now()
	b, _, err := s.Store.CheckIn(r.PathValue("ref"), now)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if b.ArrivedAt != nil && b.Date == schedule.FormatDate(schedule.Today(now)) {
		s.openOnArrival(b.TableNo, now) // they can order and pay with the table's QR code
	}
	redirectMsg(w, r, "/admin/day", url.Values{"date": {b.Date}}, "arrived")
}

// adminMoveTable moves a booking to another table.
func (s *Server) adminMoveTable(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	b, err := s.Store.Get(ref)
	if err != nil {
		s.notFound(w, r)
		return
	}
	back := url.Values{"date": {b.Date}}
	table, _ := strconv.Atoi(r.FormValue("table"))
	if _, err := s.Store.MoveTable(ref, table, s.Now()); err != nil {
		redirectMsg(w, r, "/admin/day", back, "tablebusy")
		return
	}
	redirectMsg(w, r, "/admin/day", back, "moved")
}

// ---------- Month calendar ----------

type calDay struct {
	Date    string
	Day     int
	InMonth bool
	IsToday bool
	Closed  bool
	Count   db.DayCount
}

func (s *Server) adminCalendar(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	today := schedule.Today(now)
	month, err := time.ParseInLocation("2006-01", r.URL.Query().Get("month"), schedule.Location)
	if err != nil {
		month = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, schedule.Location)
	}
	// Start the grid on the Monday before the 1st.
	offset := (int(month.Weekday()) + 6) % 7
	start := month.AddDate(0, 0, -offset)
	end := start.AddDate(0, 0, 41)
	counts := s.Store.CountsBetween(schedule.FormatDate(start), schedule.FormatDate(end))

	var weeks [][]calDay
	for wk := 0; wk < 6; wk++ {
		var row []calDay
		for i := 0; i < 7; i++ {
			d := start.AddDate(0, 0, wk*7+i)
			ds := schedule.FormatDate(d)
			_, open := schedule.HoursFor(d)
			_, closedDate := s.Store.ClosedReason(ds)
			row = append(row, calDay{
				Date: ds, Day: d.Day(), InMonth: d.Month() == month.Month(),
				IsToday: ds == schedule.FormatDate(today), Closed: !open || closedDate, Count: counts[ds],
			})
		}
		if wk >= 4 && !row[0].InMonth {
			break // no need for an empty last week
		}
		weeks = append(weeks, row)
	}
	feed := ""
	if s.Cfg.CalendarToken != "" {
		feed = s.Cfg.BaseURL + "/calendar/" + s.Cfg.CalendarToken + ".ics"
	}
	s.render(w, r, "admin_calendar", 200, M{
		"Title":     monthYear(lang, month),
		"Admin":     true,
		"Nav":       "calendar",
		"Month":     monthYear(lang, month),
		"PrevMonth": month.AddDate(0, -1, 0).Format("2006-01"),
		"NextMonth": month.AddDate(0, 1, 0).Format("2006-01"),
		"Weeks":     weeks,
		"FeedURL":   feed,
	})
}

// ---------- Menu editor ----------

func (s *Server) adminMenu(w http.ResponseWriter, r *http.Request) {
	items := s.Store.Menu(false)
	var cats []string
	seen := map[string]bool{}
	for _, it := range items {
		if !seen[it.Category] {
			seen[it.Category] = true
			cats = append(cats, it.Category)
		}
	}
	s.render(w, r, "admin_menu", 200, M{
		"Title":       st(langOf(r), "Menu"),
		"Admin":       true,
		"Nav":         "menu",
		"Flash":       flashText(r),
		"Categories":  groupMenu(items),
		"CatNames":    cats,
		"NextSort":    len(items)*10 + 10,
		"Missing":     missingPrices(items),
		"Ingredients": s.Store.Ingredients(),
		"Today":       schedule.FormatDate(schedule.Today(s.Now())),
	})
}

func missingPrices(items []db.MenuItem) int {
	n := 0
	for _, it := range items {
		if it.PriceMissing() {
			n++
		}
	}
	return n
}

func (s *Server) adminMenuSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	price, err := parseEuro(r.FormValue("price"))
	item := db.MenuItem{
		ID:          atoiDefault(r.FormValue("id"), 0),
		Category:    clip(oneLine(r.FormValue("category")), 60),
		Name:        clip(oneLine(r.FormValue("name")), 80),
		Description: clip(oneLine(r.FormValue("description")), 300),
		PriceCents:  price,
		Tags:        clip(oneLine(r.FormValue("tags")), 60),
		Sort:        atoiDefault(r.FormValue("sort"), 0),
		Available:   r.FormValue("available") == "on",
		Drink:       r.FormValue("type") == "drink" || r.FormValue("type") == "alcohol",
		Alcohol:     r.FormValue("type") == "alcohol",
		Allergens:   db.CleanAllergens(r.Form["allergen"]),
		Package:     r.FormValue("setmenu") == "package",
		Included:    r.FormValue("setmenu") == "included",
		StaffOnly:   r.FormValue("staffonly") == "on" || r.FormValue("setmenu") != "",
	}
	if hp := strings.TrimSpace(r.FormValue("happy")); hp != "" {
		h, herr := parseEuro(hp)
		if herr != nil || h < 0 {
			redirectMsg(w, r, "/admin/menu", nil, "invalid")
			return
		}
		item.HappyCents = h
	}
	if v := strings.TrimSpace(r.FormValue("until")); v != "" {
		d, derr := schedule.ParseDate(v)
		if derr != nil {
			redirectMsg(w, r, "/admin/menu", nil, "invalid")
			return
		}
		item.Until = schedule.FormatDate(d)
	}
	item.Uses = readUses(r, "use")
	if item.Included {
		price, err = 0, nil // in the set menu's price
		item.PriceCents = 0
	}
	if !item.Drink {
		item.Course = atoiDefault(r.FormValue("course"), db.CourseMain)
		if item.Course < db.CourseStarter || item.Course > db.CourseDessert {
			item.Course = db.CourseMain
		}
	}
	if err != nil || (price <= 0 && !item.Included) || item.Name == "" || item.Category == "" {
		redirectMsg(w, r, "/admin/menu", nil, "invalid")
		return
	}
	if _, err := s.Store.SaveMenuItem(item); err != nil {
		log.Printf("save menu item: %v", err)
		redirectMsg(w, r, "/admin/menu", nil, "invalid")
		return
	}
	s.live.publish() // open kitchen, stock and order screens show the new menu
	redirectMsg(w, r, "/admin/menu", nil, "saved")
}

// adminMenuAvailable switches a dish on or off the takeaway menu.
func (s *Server) adminMenuAvailable(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	item, err := s.Store.MenuItem(id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	item.Available = r.FormValue("available") == "on"
	if _, err := s.Store.SaveMenuItem(item); err != nil {
		log.Printf("menu available %d: %v", id, err)
	}
	s.live.publish()
	msg := "dishoff"
	if item.Available {
		msg = "dishon"
	}
	redirectMsg(w, r, "/admin/menu", nil, msg)
}

func (s *Server) adminMenuDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	if item, err := s.Store.MenuItem(id); err == nil {
		defer s.removePhoto(item.Photo)
	}
	if err := s.Store.DeleteMenuItem(id); err != nil {
		log.Printf("delete menu item %d: %v", id, err)
	}
	s.live.publish()
	redirectMsg(w, r, "/admin/menu", nil, "deleted")
}

// ---------- Closed days ----------

type closedRow struct {
	db.ClosedDate
	Bookings int
}

func (s *Server) adminClosed(w http.ResponseWriter, r *http.Request) {
	var rows []closedRow
	for _, c := range s.Store.ClosedDates() {
		n := s.Store.CountsBetween(c.Date, c.Date)[c.Date]
		rows = append(rows, closedRow{ClosedDate: c, Bookings: n.Tables + n.Takeaway})
	}
	offline := map[string]bool{}
	for _, r := range s.Store.RoomsOffline() {
		offline[r] = true
	}
	type roomRow struct {
		Name   string
		Tables int
		Online bool
	}
	var rooms []roomRow
	for _, name := range schedule.RoomNames() {
		n := 0
		for _, t := range schedule.Tables() {
			if t.Room == name {
				n++
			}
		}
		rooms = append(rooms, roomRow{Name: name, Tables: n, Online: !offline[name]})
	}
	s.render(w, r, "admin_closed", 200, M{
		"Rooms":   rooms,
		"Title":   st(langOf(r), "Closed days"),
		"Admin":   true,
		"Nav":     "closed",
		"Flash":   flashText(r),
		"Closed":  rows,
		"MinDate": schedule.FormatDate(schedule.Today(s.Now())),
	})
}

func (s *Server) adminClosedAdd(w http.ResponseWriter, r *http.Request) {
	date := r.FormValue("date")
	if _, err := schedule.ParseDate(date); err != nil {
		redirectMsg(w, r, "/admin/closed", nil, "invalid")
		return
	}
	if err := s.Store.AddClosedDate(date, clip(oneLine(r.FormValue("reason")), 120)); err != nil {
		log.Printf("add closed date: %v", err)
	}
	redirectMsg(w, r, "/admin/closed", nil, "saved")
}

// adminRoomOnline opens or closes a room for online bookings.
func (s *Server) adminRoomOnline(w http.ResponseWriter, r *http.Request) {
	room := r.FormValue("room")
	on := r.FormValue("on") == "1"
	if err := s.Store.SetRoomOnline(room, on); err != nil {
		redirectMsg(w, r, "/admin/closed", nil, "invalid")
		return
	}
	what := "closed a room for online bookings"
	if on {
		what = "opened a room for online bookings"
	}
	s.logActivity(r, 0, what, room)
	redirectMsg(w, r, "/admin/closed", nil, "saved")
}

func (s *Server) adminClosedDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.RemoveClosedDate(r.PathValue("date")); err != nil {
		log.Printf("remove closed date: %v", err)
	}
	redirectMsg(w, r, "/admin/closed", nil, "deleted")
}

// ---------- Calendar feed for the manager's phone ----------

// calendarFeed serves paid bookings as an .ics feed. The manager subscribes
// to this link once in Google Calendar or Apple Calendar, and every new
// booking shows up there automatically.
func (s *Server) calendarFeed(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSuffix(r.PathValue("token"), ".ics")
	if s.Cfg.CalendarToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.Cfg.CalendarToken)) != 1 {
		http.NotFound(w, r)
		return
	}
	now := s.Now()
	today := schedule.Today(now)
	from := schedule.FormatDate(today.AddDate(0, 0, -30))
	to := schedule.FormatDate(today.AddDate(0, 0, 90))

	var sb strings.Builder
	line := func(s string) { sb.WriteString(foldICS(s) + "\r\n") }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//emeal-booking//EN")
	line("CALSCALE:GREGORIAN")
	line("X-WR-CALNAME:" + escICS(s.Cfg.RestaurantName+" bookings"))
	line("REFRESH-INTERVAL;VALUE=DURATION:PT15M")
	for d := from; d <= to; {
		for _, b := range s.Store.BookingsOn(d, now) {
			if b.Status != db.StatusPaid {
				continue
			}
			day, _ := schedule.ParseDate(b.Date)
			start, err := schedule.At(day, b.Time)
			if err != nil {
				continue
			}
			end := start.Add(b.Duration())
			summary := fmt.Sprintf("Table for %d – %s", b.PartySize, b.Name)
			if b.Kind == db.KindTakeaway {
				end = start.Add(15 * time.Minute)
				summary = "Takeaway pickup – " + b.Name
			}
			line("BEGIN:VEVENT")
			line("UID:" + b.Ref + "@emeal-booking")
			line("DTSTAMP:" + now.UTC().Format("20060102T150405Z"))
			line("DTSTART:" + start.UTC().Format("20060102T150405Z"))
			line("DTEND:" + end.UTC().Format("20060102T150405Z"))
			line("SUMMARY:" + escICS(summary))
			line("DESCRIPTION:" + escICS(details(langEN, b)))
			line("END:VEVENT")
		}
		next, _ := schedule.ParseDate(d)
		d = schedule.FormatDate(next.AddDate(0, 0, 1))
	}
	line("END:VCALENDAR")
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Write([]byte(sb.String()))
}

func escICS(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`)
	return r.Replace(s)
}

// foldICS splits long lines as the calendar format requires (max 75 bytes).
func foldICS(s string) string {
	if len(s) <= 75 {
		return s
	}
	var sb strings.Builder
	n := 0
	for _, r := range s {
		size := len(string(r))
		if n+size > 75 {
			sb.WriteString("\r\n ")
			n = 1
		}
		sb.WriteRune(r)
		n += size
	}
	return sb.String()
}
