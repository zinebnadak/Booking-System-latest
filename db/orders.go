package db

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"ozmo-booking/schedule"
)

// Table bills and the chef's kitchen tickets.
//
// Guests order at the table and pay at the end. Each table has one open bill;
// staff add dishes and drinks to it during the meal (and may change a price),
// and it's paid when the guests leave.
//
// Every time staff add food, the chef gets a ticket on the kitchen screen:
// "Table 5 · 2× Pizza – no onions". Drinks never go to the kitchen.

var (
	ErrNoBill        = errors.New("no open bill on this table")
	ErrBillClosed    = errors.New("this bill is already paid or voided")
	ErrNoLine        = errors.New("no such line on the bill")
	ErrSentToKitchen = errors.New("this line is already with the chef: only its price can change")
	ErrNothingToAdd  = errors.New("nothing to add")
	ErrEmptyBill     = errors.New("the bill is empty")
	ErrTableTaken    = errors.New("that table already has an open bill")
	ErrNoTicket      = errors.New("no such kitchen ticket")

	// Split bills
	ErrSplit      = errors.New("this bill is split: each guest pays their own part")
	ErrNotSplit   = errors.New("this bill isn't split")
	ErrSharePaid  = errors.New("a guest already paid their part, so this can't change")
	ErrUnassigned = errors.New("some things on the bill aren't given to anyone yet")
	ErrBadGuest   = errors.New("no such guest")
)

// MaxGuests is the most guests one bill can be split between.
const MaxGuests = 12

// Ways to pay (more in payments.go).
const (
	PaidCard = "card" // the card terminal
	PaidCash = "cash"
)

// BillLine is one row on a bill.
type BillLine struct {
	ID         int       `json:"id"`                     // unique within the bill
	MenuItemID int       `json:"menu_item_id,omitempty"` // 0 = typed in by staff
	Name       string    `json:"name"`
	PriceCents int       `json:"price_cents"` // per item, as charged (staff may change it)
	ListCents  int       `json:"list_cents"`  // the menu price when it was added
	Qty        int       `json:"qty"`
	Drink      bool      `json:"drink,omitempty"`
	Note       string    `json:"note,omitempty"` // for the chef, e.g. "no onions"
	AddedAt    time.Time `json:"added_at"`
	TicketID   int       `json:"ticket_id,omitempty"` // the kitchen ticket it went on (food only)
	Guests     []int     `json:"guests,omitempty"`    // split bills: who had it (shared equally), e.g. [1] or [2 3]
	VAT        int       `json:"vat,omitempty"`       // hundredths of a percent (2550 = 25,5 %); 0 on old lines = food VAT
	Course     int       `json:"course,omitempty"`    // CourseStarter, CourseMain, CourseDessert; 0 for drinks
	// DiscountPct: "Rabatt" off this line, in percent (100 = on the house).
	DiscountPct int `json:"discount_pct,omitempty"`
}

// VATRate is the line's VAT in hundredths of a percent (1350 = 13,5 %).
func (l BillLine) VATRate() int {
	if l.VAT == 0 {
		return schedule.VATFood
	}
	return l.VAT
}

// Had reports whether guest g had (a part of) this line.
func (l BillLine) Had(g int) bool {
	for _, x := range l.Guests {
		if x == g {
			return true
		}
	}
	return false
}

// Total is price × quantity, less any discount (rounded to the cent).
func (l BillLine) Total() int {
	full := l.PriceCents * l.Qty
	if l.DiscountPct <= 0 {
		return full
	}
	return full - l.Discount()
}

// Discount is what the line's discount takes off, in cents.
func (l BillLine) Discount() int {
	if l.DiscountPct <= 0 {
		return 0
	}
	return (l.PriceCents*l.Qty*min(l.DiscountPct, 100) + 50) / 100
}

// PriceChanged: staff charged something other than the menu price.
func (l BillLine) PriceChanged() bool { return l.MenuItemID != 0 && l.PriceCents != l.ListCents }

// Bill is everything one table ordered, paid at the end of the meal.
type Bill struct {
	ID           int        `json:"id"`
	Table        int        `json:"table"`
	OpenedAt     time.Time  `json:"opened_at"`
	Lines        []BillLine `json:"lines"`
	NextLineID   int        `json:"next_line_id"`
	BookingRef   string     `json:"booking_ref,omitempty"`   // the online booking at this table
	DepositCents int        `json:"deposit_cents,omitempty"` // its deposit, taken off the bill
	BilledAt     *time.Time `json:"billed_at,omitempty"`     // the bill was shown to the guests
	PaidAt       *time.Time `json:"paid_at,omitempty"`
	PaidBy       string     `json:"paid_by,omitempty"`  // PaidCard, PaidCash, or PaidSplit
	Voided       bool       `json:"voided,omitempty"`   // opened by mistake, or the guests left
	Ref          string     `json:"ref,omitempty"`      // random code for the bill's online copy (QR code)
	Split        []Share    `json:"split,omitempty"`    // the guests paying separately, if split
	QROpen       bool       `json:"qr_open,omitempty"`  // guests may join and order with the table's QR code
	Payments     []Payment  `json:"payments,omitempty"` // paid in parts ("Kvar att betala"), see payments.go
}

// PaidSplit: every guest paid their own part (see Bill.Split).
const PaidSplit = "split"

// Share is one guest's part of a split bill.
type Share struct {
	Guest  int        `json:"guest"`             // 1, 2, 3...
	Ref    string     `json:"ref"`               // random code for this guest's online receipt (QR code)
	PaidAt *time.Time `json:"paid_at,omitempty"` // this guest paid
	PaidBy string     `json:"paid_by,omitempty"` // PaidCard or PaidCash
	Payer  int        `json:"payer,omitempty"`   // paid together with others: the guest who paid for them all
	Key    string     `json:"key,omitempty"`     // QR guests: secret for their own page on their phone
	Name   string     `json:"name,omitempty"`    // QR guests: what they called themselves

	// Receipts for guests who paid on their phone.
	PaperAt   *time.Time `json:"paper_at,omitempty"`   // asked for a printed receipt
	PrintedAt *time.Time `json:"printed_at,omitempty"` // a waiter gave it to them
	Emailed   int        `json:"emailed,omitempty"`    // receipts sent by email (at most MaxReceiptEmails)

	// TipCents: a tip this guest added when paying on their phone (for the
	// staff, on top of the bill, never part of it).
	TipCents int `json:"tip_cents,omitempty"`

	// Payments: this guest paid their part in parts (a gift card first, the
	// rest at the terminal). See sharepay.go.
	Payments []Payment `json:"payments,omitempty"`
}

// Label is "Guest 3" or "Guest 3 · Anna", in English (for the kitchen).
func (sh Share) Label() string {
	if sh.Name != "" {
		return "Guest " + strconv.Itoa(sh.Guest) + " · " + sh.Name
	}
	return "Guest " + strconv.Itoa(sh.Guest)
}

// IsSplit reports whether guests pay separately.
func (b Bill) IsSplit() bool { return len(b.Split) > 0 }

// share returns guest g's share, or nil.
func (b *Bill) share(g int) *Share {
	for i := range b.Split {
		if b.Split[i].Guest == g {
			return &b.Split[i]
		}
	}
	return nil
}

// SharesPaid is how many guests have paid their part.
func (b Bill) SharesPaid() int {
	n := 0
	for _, sh := range b.Split {
		if sh.PaidAt != nil {
			n++
		}
	}
	return n
}

// AnySharePaid reports whether a guest has paid already, or paid some of
// their part.
func (b Bill) AnySharePaid() bool {
	for _, sh := range b.Split {
		if sh.Started() {
			return true
		}
	}
	return false
}

// touchesPaid reports whether a line is (or would be) part of a paid guest's
// share, or of one they've started paying.
func (b Bill) touchesPaid(guests []int) bool {
	for _, g := range guests {
		if sh := b.share(g); sh != nil && sh.Started() {
			return true
		}
	}
	return false
}

// Unassigned lists the lines nobody has been given yet (split bills).
func (b Bill) Unassigned() []BillLine {
	var out []BillLine
	if !b.IsSplit() {
		return nil
	}
	for _, l := range b.Lines {
		if len(l.Guests) == 0 {
			out = append(out, l)
		}
	}
	return out
}

// ShareLine is one row on a guest's own receipt.
type ShareLine struct {
	Name     string
	Qty      int // whole dishes, when the line divides evenly
	SharedBy int // >1: shared with others (Qty is 0 then)
	Cents    int
	Note     string
	VAT      int // the line's VAT rate
}

// Parts works out a split bill: what each guest pays (index 0 = guest 1),
// what they had, and how much of the online deposit comes off their part.
//
// Each line is divided equally between the guests who had it. A leftover
// cent goes to whoever has had the fewest leftover cents so far, so the parts
// always add up to the bill exactly and an even split is even to the cent.
// The online deposit was paid by the guest who booked, so it comes off
// guest 1 first (and off the next guest if guest 1's part is smaller).
func (b Bill) Parts() (pay []int, lines [][]ShareLine, deposit []int) {
	n := len(b.Split)
	pay, lines, deposit = make([]int, n), make([][]ShareLine, n), make([]int, n)
	extra := make([]int, n+1) // leftover cents given to each guest so far
	for _, l := range b.Lines {
		var who []int
		for _, g := range l.Guests {
			if g >= 1 && g <= n {
				who = append(who, g)
			}
		}
		k := len(who)
		if k == 0 {
			continue
		}
		total := l.Total()
		order := append([]int(nil), who...)
		sort.SliceStable(order, func(i, j int) bool { return extra[order[i]] < extra[order[j]] })
		plus := map[int]bool{}
		for _, g := range order[:total%k] {
			plus[g] = true
			extra[g]++
		}
		for _, g := range who {
			c := total / k
			if plus[g] {
				c++
			}
			sl := ShareLine{Name: l.Name, Cents: c, Note: l.Note, VAT: l.VATRate()}
			if l.DiscountPct > 0 {
				sl.Name += fmt.Sprintf(" (−%d %%)", l.DiscountPct)
			}
			if l.Qty%k == 0 {
				sl.Qty = l.Qty / k
			} else {
				sl.SharedBy = k
			}
			pay[g-1] += c
			lines[g-1] = append(lines[g-1], sl)
		}
	}
	left := b.Deposit()
	for i := range pay {
		take := min(left, pay[i])
		pay[i] -= take
		deposit[i] = take
		left -= take
	}
	return pay, lines, deposit
}

// PaidWith adds up what a bill brought in: on the card terminal, in cash,
// and paid online by guests on their phones.
// (The other ways of paying are in Takings.)
func (b Bill) PaidWith() (card, cash, online int) {
	t := b.Takings()
	return t[PaidCard], t[PaidCash], t[PaidOnline]
}

// Tips adds up the tips guests added when paying on their phones.
func (b Bill) Tips() int {
	t := 0
	for _, sh := range b.Split {
		t += sh.TipCents
	}
	return t
}

// Open reports whether the bill can still change.
func (b Bill) Open() bool { return b.PaidAt == nil && !b.Voided }

// Total is the food and drinks.
func (b Bill) Total() int {
	t := 0
	for _, l := range b.Lines {
		t += l.Total()
	}
	return t
}

// Discounts is what discounts took off the whole bill, in cents.
func (b Bill) Discounts() int {
	d := 0
	for _, l := range b.Lines {
		d += l.Discount()
	}
	return d
}

// Deposit is the online deposit taken off (never more than the bill).
func (b Bill) Deposit() int { return min(b.DepositCents, b.Total()) }

// ToPay is what the guests pay now.
func (b Bill) ToPay() int { return b.Total() - b.Deposit() }

// Count is how many things are on the bill.
func (b Bill) Count() int {
	n := 0
	for _, l := range b.Lines {
		n += l.Qty
	}
	return n
}

func (b *Bill) clone() Bill {
	c := *b
	c.Lines = append([]BillLine(nil), b.Lines...)
	for i := range c.Lines {
		c.Lines[i].Guests = append([]int(nil), b.Lines[i].Guests...)
	}
	c.Split = append([]Share(nil), b.Split...)
	for i := range c.Split {
		c.Split[i].Payments = append([]Payment(nil), b.Split[i].Payments...)
	}
	c.Payments = append([]Payment(nil), b.Payments...)
	return c
}

func (b *Bill) line(id int) (int, *BillLine) {
	for i := range b.Lines {
		if b.Lines[i].ID == id {
			return i, &b.Lines[i]
		}
	}
	return -1, nil
}

// Ticket is food sent to the chef in one go.
type Ticket struct {
	ID     int          `json:"id"`
	Table  int          `json:"table"`
	BillID int          `json:"bill_id"`
	At     time.Time    `json:"at"`
	Items  []TicketItem `json:"items"`
	DoneAt *time.Time   `json:"done_at,omitempty"` // the chef pressed Done
	Bar    bool         `json:"bar,omitempty"`     // drinks a guest ordered with the QR code: for the waiters to bring

	// Takeaway orders paid online (Table is 0). The chef taps Seen; the
	// ticket comes back TakeawayKitchenHeadsUp before pickup.
	Takeaway string     `json:"takeaway,omitempty"` // the booking's code
	OrderNo  int        `json:"order_no,omitempty"` // #12, as staff know it
	Customer string     `json:"customer,omitempty"` // who picks it up
	Pickup   *time.Time `json:"pickup,omitempty"`
	SeenAt   *time.Time `json:"seen_at,omitempty"`
	Note     string     `json:"note,omitempty"` // the customer's note

	// Courses: mains (or desserts) that wait for the waiter's go. The chef
	// sees them greyed out under "Waiting for the go" until then.
	Course  int        `json:"course,omitempty"`
	Held    bool       `json:"held,omitempty"`
	FiredAt *time.Time `json:"fired_at,omitempty"` // when the waiter gave the go

	// Message: a note to the kitchen from a waiter, with no dishes
	// ("Fritt köksmeddelande"), e.g. "Table 5: one guest is allergic to nuts".
	Message string `json:"message,omitempty"`

	// ServedAt: the food was taken from the kitchen to the table (the chef
	// or a waiter tapped Served). Until then a done ticket is "ready to pick
	// up" and the waiters' screens keep telling them.
	ServedAt *time.Time `json:"served_at,omitempty"`

	// The "3 meals" deal: Deal is the MealRun's ID and Meal which of its
	// meals this is (1, 2, 3). Each meal goes to the chef when the guests
	// ask for it (see dealkitchen.go).
	Deal int `json:"deal,omitempty"`
	Meal int `json:"meal,omitempty"`
}

// Since is when the ticket became the chef's to do: when it was ordered, or
// when the waiter gave the go for a held course.
func (t Ticket) Since() time.Time {
	if t.FiredAt != nil {
		return *t.FiredAt
	}
	return t.At
}

// TicketItem is one dish on a ticket. No prices: the chef doesn't need them.
type TicketItem struct {
	LineID    int    `json:"line_id"`
	Name      string `json:"name"`
	Qty       int    `json:"qty"`
	Note      string `json:"note,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"` // taken off the bill after it was sent
	Guest     int    `json:"guest,omitempty"`     // QR orders: whose it is, so the plate goes to the right person
	Who       string `json:"who,omitempty"`       // and their name, if they gave one
}

// Live reports whether anything on the ticket still needs cooking.
func (t Ticket) Live() bool {
	if t.Message != "" {
		return true
	}
	for _, it := range t.Items {
		if !it.Cancelled {
			return true
		}
	}
	return false
}

// ForTable: food from the kitchen for a table (not drinks to bring, not a
// message, not takeaway).
func (t Ticket) ForTable() bool {
	return t.BillID != 0 && !t.Bar && t.Message == "" && !t.IsTakeaway()
}

// ReadyToServe: done in the kitchen, not taken to the table yet.
func (t Ticket) ReadyToServe() bool {
	return t.ForTable() && t.DoneAt != nil && t.ServedAt == nil && t.Live()
}

func (t *Ticket) clone() Ticket {
	c := *t
	c.Items = append([]TicketItem(nil), t.Items...)
	return c
}

// NewLine is one thing staff add to a bill.
type NewLine struct {
	MenuItemID int
	Name       string
	PriceCents int // what to charge
	ListCents  int // the menu price
	Qty        int
	Drink      bool
	Note       string
	VAT        int // hundredths of a percent; 0 = food VAT
	Course     int // CourseStarter, CourseMain, CourseDessert (0 for drinks)
	// NoKitchen: food that isn't cooked (a set menu's price line).
	NoKitchen bool
	// Hold: "Vänta varmt". A main course ordered together with starters,
	// or a dessert ordered with other food, waits for the waiter's go
	// ("Varma fram", "Fram efterrätt") before the chef sees it as an order.
	Hold bool
}

// ---------- saving with undo ----------

type ordersSnapshot struct {
	bills      []Bill
	tickets    []Ticket
	attempts   []PayAttempt
	tableQRs   []TableQR
	phone      []PhoneOrder
	reviews    []Review
	nextReview int
	staff      []StaffMember
	nextStaff  int
	activity   []*Activity
	settings   Settings
	nextBill   int
	nextTicket int
	nextPhone  int
	vouchers   []Voucher
	shifts     []Shift
	nextShift  int
	stock      []stockSnap
	ingr       []Ingredient
	nextIngr   int
}

// changeOrders runs fn under the lock and saves if it reports a change. If
// fn fails or saving fails, bills and tickets are put back as they were.
func (s *Store) changeOrders(fn func() (bool, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	restore := s.snapshotOrders()
	changed, err := fn()
	if err != nil || !changed {
		if err != nil {
			restore()
		}
		return err
	}
	if err := s.save(); err != nil {
		restore()
		return err
	}
	return nil
}

// changeDeal is change and changeOrders in one: for the "3 meals" deal,
// whose steps change the table and the kitchen's tickets together.
func (s *Store) changeDeal(fn func() (bool, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	restore := s.snapshotOrders()
	tables := s.snapshotTables()
	back := func() { restore(); s.restoreTables(tables) }
	changed, err := fn()
	if err != nil || !changed {
		back()
		return err
	}
	if err := s.save(); err != nil {
		back()
		return err
	}
	return nil
}

// snapshotOrders copies bills, tickets and the rest that changeOrders
// guards, and returns how to put them back. Callers hold s.mu.
func (s *Store) snapshotOrders() (restore func()) {
	var snap ordersSnapshot
	for _, b := range s.d.Bills {
		snap.bills = append(snap.bills, b.clone())
	}
	for _, t := range s.d.Tickets {
		snap.tickets = append(snap.tickets, t.clone())
	}
	for _, a := range s.d.PayAttempts {
		snap.attempts = append(snap.attempts, a.clone())
	}
	for _, q := range s.d.TableQRs {
		snap.tableQRs = append(snap.tableQRs, *q)
	}
	for _, o := range s.d.PhoneOrders {
		snap.phone = append(snap.phone, o.clone())
	}
	snap.settings, snap.nextPhone = s.d.Settings, s.d.NextPhoneOrderID
	for _, r := range s.d.Reviews {
		snap.reviews = append(snap.reviews, *r)
	}
	snap.nextReview = s.d.NextReviewID
	for _, m := range s.d.Staff {
		snap.staff = append(snap.staff, *m)
	}
	snap.nextStaff = s.d.NextStaffID
	snap.activity = append([]*Activity(nil), s.d.Activity...) // entries are never changed, only added
	for _, v := range s.d.Vouchers {
		snap.vouchers = append(snap.vouchers, v.clone())
	}
	for _, sh := range s.d.Shifts {
		c := *sh
		if sh.Out != nil {
			t := *sh.Out
			c.Out = &t
		}
		snap.shifts = append(snap.shifts, c)
	}
	snap.nextShift = s.d.NextShiftID
	for _, m := range s.d.Menu {
		snap.stock = append(snap.stock, stockSnap{id: m.ID, counted: m.Counted, left: m.Left, soldOut: m.SoldOutOn,
			uses: append([]IngredientUse(nil), m.Uses...)})
	}
	for _, i := range s.d.Ingredients {
		snap.ingr = append(snap.ingr, *i)
	}
	snap.nextIngr = s.d.NextIngredientID
	snap.nextBill, snap.nextTicket = s.d.NextBillID, s.d.NextTicketID
	return func() {
		s.d.Bills, s.d.Tickets, s.d.PayAttempts, s.d.TableQRs, s.d.PhoneOrders = nil, nil, nil, nil, nil
		for i := range snap.phone {
			o := snap.phone[i]
			s.d.PhoneOrders = append(s.d.PhoneOrders, &o)
		}
		s.d.Settings, s.d.NextPhoneOrderID = snap.settings, snap.nextPhone
		s.d.Staff, s.d.NextStaffID, s.d.Activity = nil, snap.nextStaff, snap.activity
		for i := range snap.staff {
			m := snap.staff[i]
			s.d.Staff = append(s.d.Staff, &m)
		}
		s.d.Reviews, s.d.NextReviewID = nil, snap.nextReview
		for i := range snap.reviews {
			r := snap.reviews[i]
			s.d.Reviews = append(s.d.Reviews, &r)
		}
		for i := range snap.bills {
			b := snap.bills[i]
			s.d.Bills = append(s.d.Bills, &b)
		}
		for i := range snap.tickets {
			t := snap.tickets[i]
			s.d.Tickets = append(s.d.Tickets, &t)
		}
		for i := range snap.attempts {
			a := snap.attempts[i]
			s.d.PayAttempts = append(s.d.PayAttempts, &a)
		}
		for i := range snap.tableQRs {
			q := snap.tableQRs[i]
			s.d.TableQRs = append(s.d.TableQRs, &q)
		}
		s.d.NextBillID, s.d.NextTicketID = snap.nextBill, snap.nextTicket
		s.d.Vouchers = nil
		for i := range snap.vouchers {
			v := snap.vouchers[i]
			s.d.Vouchers = append(s.d.Vouchers, &v)
		}
		s.d.Shifts, s.d.NextShiftID = nil, snap.nextShift
		for i := range snap.shifts {
			sh := snap.shifts[i]
			s.d.Shifts = append(s.d.Shifts, &sh)
		}
		for _, st := range snap.stock {
			if m := s.menuByID(st.id); m != nil {
				m.Counted, m.Left, m.SoldOutOn, m.Uses = st.counted, st.left, st.soldOut, st.uses
			}
		}
		s.d.Ingredients, s.d.NextIngredientID = nil, snap.nextIngr
		for i := range snap.ingr {
			ing := snap.ingr[i]
			s.d.Ingredients = append(s.d.Ingredients, &ing)
		}
	}
}

// ---------- bills ----------

// openBillAt is the table's open bill. Callers hold s.mu.
func (s *Store) openBillAt(table int) *Bill {
	for _, b := range s.d.Bills {
		if b.Table == table && b.Open() {
			return b
		}
	}
	return nil
}

func (s *Store) billByID(id int) *Bill {
	for _, b := range s.d.Bills {
		if b.ID == id {
			return b
		}
	}
	return nil
}

func (s *Store) ticketByID(id int) *Ticket {
	for _, t := range s.d.Tickets {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// OpenBill returns the table's open bill, and false if it has none.
func (s *Store) OpenBill(table int) (Bill, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.openBillAt(table); b != nil {
		return b.clone(), true
	}
	return Bill{}, false
}

// BillByID returns any bill.
func (s *Store) BillByID(id int) (Bill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.billByID(id); b != nil {
		return b.clone(), nil
	}
	return Bill{}, ErrNoBill
}

// OpenBills lists every table's open bill, by table.
func (s *Store) OpenBills() []Bill {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Bill
	for _, b := range s.d.Bills {
		if b.Open() {
			out = append(out, b.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Table < out[j].Table })
	return out
}

// PaidBillsOn lists the bills paid on a date.
func (s *Store) PaidBillsOn(date string) []Bill {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Bill
	for _, b := range s.d.Bills {
		if b.PaidAt != nil && schedule.FormatDate(*b.PaidAt) == date {
			out = append(out, b.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PaidAt.Before(*out[j].PaidAt) })
	return out
}

// PaidBillsBetween lists the bills paid from one date to another (both
// included), for the best-seller lists.
func (s *Store) PaidBillsBetween(from, to string) []Bill {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Bill
	for _, b := range s.d.Bills {
		if b.PaidAt == nil {
			continue
		}
		if d := schedule.FormatDate(*b.PaidAt); d >= from && d <= to {
			out = append(out, b.clone())
		}
	}
	return out
}

// BillsForTable is every bill a table ever had (paid, voided and the open
// one), oldest first: the table's history.
func (s *Store) BillsForTable(table int) []Bill {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Bill
	for _, b := range s.d.Bills {
		if b.Table == table {
			out = append(out, b.clone())
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].OpenedAt.Before(out[j].OpenedAt) })
	return out
}

// DepositUsed reports whether a booking's deposit is already on a bill.
// Callers hold s.mu.
func (s *Store) depositUsed(ref string) bool {
	for _, b := range s.d.Bills {
		if b.BookingRef == ref && !b.Voided {
			return true
		}
	}
	return false
}

// AddToBill puts lines on the table's open bill, opening one if the table
// has none (bookingRef and deposit: the online booking at the table, whose
// deposit comes off the bill; used only for a new bill, and only once per
// booking). Food lines go to the chef as one new ticket.
//
// expectBill is the bill the waiter's screen was showing (0 = none). If
// that bill has been paid in the meantime, nothing is added (ErrStale):
// the new order belongs to whoever sits there now.
func (s *Store) AddToBill(table, expectBill int, lines []NewLine, bookingRef string, deposit int, now time.Time) (Bill, *Ticket, error) {
	var out Bill
	var ticket *Ticket
	err := s.changeOrders(func() (bool, error) {
		if !validTable(table) {
			return false, ErrNoTable
		}
		if len(lines) == 0 {
			return false, ErrNothingToAdd
		}
		b := s.openBillAt(table)
		if expectBill != 0 && (b == nil || b.ID != expectBill) {
			return false, ErrStale
		}
		if err := s.takeStock(lines); err != nil {
			return false, err
		}
		if b == nil {
			b = s.newBill(table, bookingRef, deposit, now)
			b.QROpen = true // the guests can join with the QR code from now on
		}
		ticket = s.addLines(b, lines, nil, false, now)
		b.BilledAt = nil // more was ordered after the bill was shown
		out = b.clone()
		return true, nil
	})
	return out, ticket, err
}

// addLines puts new lines on a bill. Food goes to the chef as one ticket.
// guest is whose they are (nil = nobody's yet): the lines are theirs from
// the start and the chef sees whose they are. phone: the guest ordered on
// their phone, so their drinks go on a "to bring" ticket for the waiters
// (staff bring what they type in themselves). Returns a copy of the kitchen
// ticket, if there is one. Callers hold s.mu.
func (s *Store) addLines(b *Bill, lines []NewLine, guest *Share, phone bool, now time.Time) *Ticket {
	var food, drinks, heldMains, heldDesserts []TicketItem
	starters, otherFood := false, false
	for _, nl := range lines {
		if nl.Drink || nl.NoKitchen {
			continue
		}
		starters = starters || nl.Course == CourseStarter
		otherFood = otherFood || nl.Course != CourseDessert
	}
	for _, nl := range lines {
		b.NextLineID++
		l := BillLine{
			ID: b.NextLineID, MenuItemID: nl.MenuItemID, Name: nl.Name,
			PriceCents: nl.PriceCents, ListCents: nl.ListCents, Qty: nl.Qty,
			Drink: nl.Drink, Note: nl.Note, AddedAt: now, VAT: nl.VAT, Course: nl.Course,
		}
		it := TicketItem{LineID: l.ID, Name: l.Name, Qty: l.Qty, Note: l.Note}
		if guest != nil {
			l.Guests = []int{guest.Guest}
			it.Guest, it.Who = guest.Guest, guest.Name
		}
		switch {
		case l.Drink && phone:
			drinks = append(drinks, it)
		case l.Drink, nl.NoKitchen:
		case nl.Hold && l.Course == CourseMain && starters:
			heldMains = append(heldMains, it)
		case nl.Hold && l.Course == CourseDessert && otherFood:
			heldDesserts = append(heldDesserts, it)
		default:
			food = append(food, it)
		}
		b.Lines = append(b.Lines, l)
	}
	var kitchen, held *Ticket
	for _, group := range []struct {
		items  []TicketItem
		bar    bool
		course int
		held   bool
	}{{food, false, 0, false}, {drinks, true, 0, false}, {heldMains, false, CourseMain, true}, {heldDesserts, false, CourseDessert, true}} {
		if len(group.items) == 0 {
			continue
		}
		s.d.NextTicketID++
		t := &Ticket{ID: s.d.NextTicketID, Table: b.Table, BillID: b.ID, At: now, Items: group.items, Bar: group.bar,
			Course: group.course, Held: group.held}
		s.d.Tickets = append(s.d.Tickets, t)
		for i := range b.Lines {
			for _, it := range group.items {
				if b.Lines[i].ID == it.LineID {
					b.Lines[i].TicketID = t.ID
				}
			}
		}
		if !group.bar && group.held && held == nil {
			c := t.clone()
			held = &c
		} else if !group.bar && !group.held {
			c := t.clone()
			kitchen = &c
		}
	}
	b.BilledAt = nil // more was ordered after the bill was shown
	if kitchen == nil {
		return held // everything waits for the go
	}
	return kitchen
}

// newBill opens a bill on a table. The online deposit of the booking seated
// there comes off it (once: a deposit is never used on two bills).
// Callers hold s.mu.
func (s *Store) newBill(table int, bookingRef string, deposit int, now time.Time) *Bill {
	s.d.NextBillID++
	b := &Bill{ID: s.d.NextBillID, Table: table, OpenedAt: now, Ref: newRef()}
	if bookingRef != "" && deposit > 0 && !s.depositUsed(bookingRef) {
		b.BookingRef, b.DepositCents = bookingRef, deposit
	}
	s.d.Bills = append(s.d.Bills, b)
	return b
}

// openBill finds an open bill by id. Callers hold s.mu.
func (s *Store) openBillByID(id int) (*Bill, error) {
	b := s.billByID(id)
	if b == nil {
		return nil, ErrNoBill
	}
	if !b.Open() {
		return nil, ErrBillClosed
	}
	return b, nil
}

// UpdateLine changes a line's price, and for lines the chef hasn't got
// (drinks) the quantity and note too.
func (s *Store) UpdateLine(billID, lineID, price, qty int, note string) error {
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid // take the part payments back first
		}
		_, l := b.line(lineID)
		if l == nil {
			return false, ErrNoLine
		}
		if b.touchesPaid(l.Guests) {
			return false, ErrSharePaid
		}
		if busy := s.holdOn(b, l.Guests, []int{l.ID}, s.clock()); busy != nil {
			return false, busy
		}
		if l.TicketID != 0 && !l.Drink && (qty != l.Qty || note != l.Note) {
			return false, ErrSentToKitchen
		}
		if qty > l.Qty {
			if err := s.takeStock([]NewLine{{MenuItemID: l.MenuItemID, Qty: qty - l.Qty}}); err != nil {
				return false, err
			}
		} else if qty < l.Qty {
			s.giveStock(l.MenuItemID, l.Qty-qty)
		}
		if l.Drink && l.TicketID != 0 && qty != l.Qty { // on the waiters' "to bring" list
			if t := s.ticketByID(l.TicketID); t != nil {
				for j := range t.Items {
					if t.Items[j].LineID == l.ID {
						t.Items[j].Qty = qty
					}
				}
			}
		}
		l.PriceCents, l.Qty, l.Note = price, qty, note
		return true, nil
	})
}

// ErrBadDiscount: a discount must be 0–100 %.
var ErrBadDiscount = errors.New("a discount is 0 to 100 percent")

// SetDiscount puts a discount ("Rabatt") on one line, in percent (0 = none,
// 100 = on the house).
func (s *Store) SetDiscount(billID, lineID, pct int) error {
	if pct < 0 || pct > 100 {
		return ErrBadDiscount
	}
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid // take the part payments back first
		}
		_, l := b.line(lineID)
		if l == nil {
			return false, ErrNoLine
		}
		if b.touchesPaid(l.Guests) {
			return false, ErrSharePaid
		}
		if busy := s.holdOn(b, l.Guests, []int{l.ID}, s.clock()); busy != nil {
			return false, busy
		}
		if l.DiscountPct == pct {
			return false, nil
		}
		l.DiscountPct = pct
		b.BilledAt = nil // the shown bill is out of date
		return true, nil
	})
}

// DiscountBill puts the same discount on every line of the bill that isn't
// paid yet. It returns how many lines got it.
func (s *Store) DiscountBill(billID, pct int) (int, error) {
	if pct < 0 || pct > 100 {
		return 0, ErrBadDiscount
	}
	n := 0
	err := s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid // take the part payments back first
		}
		if busy := s.anyHold(b, s.clock()); busy != nil {
			return false, busy
		}
		for i := range b.Lines {
			l := &b.Lines[i]
			if b.touchesPaid(l.Guests) || l.DiscountPct == pct || l.PriceCents == 0 {
				continue // (a dish in a set menu has no price of its own)
			}
			l.DiscountPct = pct
			n++
		}
		if n > 0 {
			b.BilledAt = nil
		}
		return n > 0, nil
	})
	return n, err
}

// RemoveLine takes a line off the bill. If the chef already has it, it's
// struck through on the kitchen screen. kitchen reports whether that happened.
func (s *Store) RemoveLine(billID, lineID int) (kitchen bool, err error) {
	err = s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid // take the part payments back first
		}
		i, l := b.line(lineID)
		if l == nil {
			return false, ErrNoLine
		}
		if b.touchesPaid(l.Guests) {
			return false, ErrSharePaid
		}
		if busy := s.holdOn(b, l.Guests, []int{l.ID}, s.clock()); busy != nil {
			return false, busy
		}
		made := false // cooked, or brought to the table: it's used up
		if l.TicketID != 0 {
			if t := s.ticketByID(l.TicketID); t != nil {
				made = t.DoneAt != nil
				for j := range t.Items {
					if t.Items[j].LineID == l.ID {
						t.Items[j].Cancelled = true
						kitchen = true
					}
				}
			}
		}
		if !made {
			s.giveStock(l.MenuItemID, l.Qty)
		}
		b.Lines = append(b.Lines[:i], b.Lines[i+1:]...)
		return true, nil
	})
	return kitchen, err
}

// MarkBilled records that the bill was shown (or printed) for the guests.
func (s *Store) MarkBilled(billID int, now time.Time) error {
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if b.BilledAt != nil {
			return false, nil
		}
		t := now
		b.BilledAt = &t
		return true, nil
	})
}

// PayBill closes the bill: the guests paid, the table is free for the next.
func (s *Store) PayBill(billID int, method string, now time.Time) (Bill, error) {
	var out Bill
	err := s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Lines) == 0 {
			return false, ErrEmptyBill
		}
		if b.IsSplit() {
			return false, ErrSplit
		}
		method = staffMethod(method)
		if len(b.Payments) > 0 {
			// Paid in parts so far: this pays what's left.
			if left := b.Left(); left > 0 {
				b.Payments = append(b.Payments, Payment{Method: method, Cents: left, At: now})
			}
			closeWithPayments(b, now)
			out = b.clone()
			return true, nil
		}
		t := now
		b.PaidAt, b.PaidBy = &t, method
		out = b.clone()
		return true, nil
	})
	return out, err
}

// VoidBill throws a bill away (opened on the wrong table and moved, or the
// guests left). Anything the chef still has from it is struck through.
func (s *Store) VoidBill(billID int) error {
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid // take the part payments back first
		}
		if b.AnySharePaid() {
			return false, ErrSharePaid
		}
		if busy := s.anyHold(b, s.clock()); busy != nil {
			return false, busy
		}
		b.Voided = true
		for _, t := range s.d.Tickets {
			if t.BillID == b.ID && t.DoneAt == nil {
				for j := range t.Items {
					t.Items[j].Cancelled = true
				}
			}
		}
		return true, nil
	})
}

// MoveBill moves an open bill to another table (the guests moved, or it was
// opened on the wrong table). The chef's tickets follow it.
func (s *Store) MoveBill(billID, toTable int) error {
	return s.changeOrders(func() (bool, error) {
		if !validTable(toTable) {
			return false, ErrNoTable
		}
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if b.Table == toTable {
			return false, nil
		}
		if s.openBillAt(toTable) != nil {
			return false, ErrTableTaken
		}
		b.Table = toTable
		for _, t := range s.d.Tickets {
			if t.BillID == b.ID {
				t.Table = toTable
			}
		}
		return true, nil
	})
}

// ---------- splitting a bill ----------

// SplitBill divides a bill between guests (2 to MaxGuests), who each pay
// for what they had. guests 0 or 1 undoes the split.
//
// Guests can be added at any time, even after some have paid (a fifth
// person was missed). Once someone has paid, a guest can only be taken away
// if they haven't paid and share nothing with someone who has.
func (s *Store) SplitBill(billID, guests int) error {
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid // take the part payments back first
		}
		if guests > MaxGuests || guests < 0 {
			return false, ErrBadGuest
		}
		for g := max(guests, 1) + 1; g <= len(b.Split); g++ {
			if sh := b.share(g); sh != nil && sh.Key != "" {
				return false, ErrQRGuests // they're on their phones: they stay
			}
		}
		if guests <= 1 && b.QRGuests() {
			return false, ErrQRGuests // phones joined: the bill stays split
		}
		if guests < len(b.Split) {
			var gone []int
			for g := max(guests, 1) + 1; g <= len(b.Split); g++ {
				gone = append(gone, g)
			}
			if guests <= 1 {
				gone = append(gone, 1)
			}
			if busy := s.holdOn(b, gone, nil, s.clock()); busy != nil {
				return false, busy
			}
		}
		if b.AnySharePaid() {
			if guests <= 1 {
				return false, ErrSharePaid
			}
			for g := guests + 1; g <= len(b.Split); g++ {
				if sh := b.share(g); sh != nil && sh.Started() {
					return false, ErrSharePaid
				}
				for _, l := range b.Lines {
					if l.Had(g) && b.touchesPaid(l.Guests) {
						return false, ErrSharePaid
					}
				}
			}
		}
		if guests <= 1 {
			if !b.IsSplit() {
				return false, nil
			}
			b.Split = nil
			for i := range b.Lines {
				b.Lines[i].Guests = nil
			}
			return true, nil
		}
		var shares []Share
		for g := 1; g <= guests; g++ {
			if old := b.share(g); old != nil {
				shares = append(shares, *old) // keep their receipt code
			} else {
				shares = append(shares, Share{Guest: g, Ref: newRef()})
			}
		}
		b.Split = shares
		for i := range b.Lines { // forget guests that no longer exist
			var keep []int
			for _, g := range b.Lines[i].Guests {
				if g <= guests {
					keep = append(keep, g)
				}
			}
			b.Lines[i].Guests = keep
		}
		return true, nil
	})
}

// AssignLine says who had a line: one guest, or several sharing it equally
// (none = nobody yet). Paid guests' parts can't change.
func (s *Store) AssignLine(billID, lineID int, guests []int) error {
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid // take the part payments back first
		}
		if !b.IsSplit() {
			return false, ErrNotSplit
		}
		_, l := b.line(lineID)
		if l == nil {
			return false, ErrNoLine
		}
		seen := map[int]bool{}
		var clean []int
		for _, g := range guests {
			if g < 1 || g > len(b.Split) {
				return false, ErrBadGuest
			}
			if !seen[g] {
				seen[g] = true
				clean = append(clean, g)
			}
		}
		sort.Ints(clean)
		if b.touchesPaid(l.Guests) || b.touchesPaid(clean) {
			return false, ErrSharePaid
		}
		if busy := s.holdOn(b, append(append([]int(nil), l.Guests...), clean...), []int{l.ID}, s.clock()); busy != nil {
			return false, busy
		}
		if equalInts(clean, l.Guests) {
			return false, nil
		}
		l.Guests = clean
		return true, nil
	})
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SplitEvenly shares every line between all the guests.
func (s *Store) SplitEvenly(billID int) error {
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid // take the part payments back first
		}
		if !b.IsSplit() {
			return false, ErrNotSplit
		}
		if b.AnySharePaid() {
			return false, ErrSharePaid
		}
		if busy := s.anyHold(b, s.clock()); busy != nil {
			return false, busy
		}
		all := make([]int, len(b.Split))
		for i := range all {
			all[i] = i + 1
		}
		for i := range b.Lines {
			b.Lines[i].Guests = append([]int(nil), all...)
		}
		return true, nil
	})
}

// SeparateLine turns "3× Läsk" into three lines of one, so each can go to a
// different guest.
func (s *Store) SeparateLine(billID, lineID int) error {
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid // take the part payments back first
		}
		i, l := b.line(lineID)
		if l == nil {
			return false, ErrNoLine
		}
		if l.Qty < 2 {
			return false, nil
		}
		if b.touchesPaid(l.Guests) {
			return false, ErrSharePaid
		}
		if busy := s.holdOn(b, l.Guests, []int{l.ID}, s.clock()); busy != nil {
			return false, busy
		}
		one := *l
		one.Qty, one.Guests = 1, nil
		parts := []BillLine{one}
		for n := 1; n < l.Qty; n++ {
			b.NextLineID++
			x := one
			x.ID = b.NextLineID
			parts = append(parts, x)
		}
		rest := append([]BillLine(nil), b.Lines[i+1:]...)
		b.Lines = append(append(b.Lines[:i], parts...), rest...)
		return true, nil
	})
}

// PayShare: guest g paid their part. When the last guest pays, the whole
// bill is paid and the table is free. closed reports that.
func (s *Store) PayShare(billID, g int, method string, now time.Time) (closed bool, err error) {
	return s.PayShares(billID, []int{g}, method, now)
}

// PayShares: one payment for several guests' parts (one of them pays for
// the others, or "the rest pay together"). The lowest-numbered of them is
// noted as the one who paid. When nobody is left, the table is free.
func (s *Store) PayShares(billID int, guests []int, method string, now time.Time) (closed bool, err error) {
	err = s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if !b.IsSplit() {
			return false, ErrNotSplit
		}
		if len(b.Unassigned()) > 0 {
			return false, ErrUnassigned
		}
		seen := map[int]bool{}
		var who []int
		for _, g := range guests {
			sh := b.share(g)
			if sh == nil {
				return false, ErrBadGuest
			}
			if sh.PaidAt == nil && !seen[g] {
				seen[g] = true
				who = append(who, g)
			}
		}
		if len(who) == 0 {
			return false, nil
		}
		sort.Ints(who)
		method = staffMethod(method)
		if busy := s.holdOn(b, who, nil, now); busy != nil {
			return false, busy
		}
		if s.ordersWaiting(b.ID, who, false) {
			return false, ErrOrderPending
		}
		if len(who) > 1 {
			for _, g := range who {
				if len(b.share(g).Payments) > 0 {
					return false, ErrPartlyPaid // they pay the rest of theirs on their own
				}
			}
		}
		t := now
		for _, g := range who {
			if left := b.ShareLeft(g); len(b.share(g).Payments) > 0 && left > 0 {
				// Paid some already (a gift card): this pays the rest.
				b.share(g).Payments = append(b.share(g).Payments, Payment{Method: method, Cents: left, At: now})
			}
			sh := b.share(g)
			sh.PaidAt, sh.PaidBy, sh.Payer = &t, method, 0
			if len(sh.Payments) > 0 {
				sh.PaidBy = methodOf(sh.Payments)
			}
			if len(who) > 1 {
				sh.Payer = who[0]
			}
		}
		closed = closeIfDone(b, now)
		return true, nil
	})
	return closed, err
}

// PaidTogether lists the guests whose parts guest g paid for (g included),
// or nil if g paid only for themselves.
func (b Bill) PaidTogether(g int) []int {
	var out []int
	for _, sh := range b.Split {
		if sh.Payer == g && sh.PaidAt != nil {
			out = append(out, sh.Guest)
		}
	}
	return out
}

// UndoShare takes back a payment pressed by mistake (only while the bill is
// still open). A payment for several guests is taken back for all of them.
func (s *Store) UndoShare(billID, g int) error {
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		sh := b.share(g)
		if sh == nil {
			return false, ErrBadGuest
		}
		if sh.PaidAt == nil {
			return false, nil
		}
		if sh.PaidBy == PaidOnline {
			return false, ErrPaidOnline
		}
		payer := sh.Payer
		for i := range b.Split {
			x := &b.Split[i]
			if x.Guest == g || (payer != 0 && x.Payer == payer) {
				x.PaidAt, x.PaidBy, x.Payer = nil, "", 0
				// Paid in parts: the last part (what closed it) is taken back;
				// the earlier ones (a gift card) stay.
				if n := len(x.Payments); n > 0 {
					s.giveBackPayment(b.ID, x.Payments[n-1])
					x.Payments = x.Payments[:n-1]
				}
			}
		}
		return true, nil
	})
}

// ReceiptByRef finds the bill behind an online receipt code: the whole bill
// (guest 0) or one guest's part of a split bill.
func (s *Store) ReceiptByRef(ref string) (Bill, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ref) != 32 {
		return Bill{}, 0, false
	}
	for _, b := range s.d.Bills {
		if b.Voided {
			continue
		}
		if b.Ref == ref {
			return b.clone(), 0, true
		}
		for _, sh := range b.Split {
			if sh.Ref == ref {
				return b.clone(), sh.Guest, true
			}
		}
	}
	return Bill{}, 0, false
}

// ensureBillRefs gives bills from before online receipts existed their code.
// Callers hold s.mu.
func (s *Store) ensureBillRefs() bool {
	changed := false
	for _, b := range s.d.Bills {
		if b.Ref == "" {
			b.Ref = newRef()
			changed = true
		}
	}
	return changed
}

// ---------- kitchen ----------

// OpenTickets is the chef's queue, oldest first.
func (s *Store) OpenTickets() []Ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Ticket
	for _, t := range s.d.Tickets {
		if t.DoneAt == nil && !t.Bar {
			out = append(out, t.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// DrinksToBring is what QR guests ordered to drink and the waiters haven't
// brought yet, oldest first.
func (s *Store) DrinksToBring() []Ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Ticket
	for _, t := range s.d.Tickets {
		if t.DoneAt == nil && t.Bar && t.Live() {
			out = append(out, t.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// RecentDone is the last few tickets the chef finished, newest first (to
// undo a Done pressed by mistake).
func (s *Store) RecentDone(n int) []Ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Ticket
	for _, t := range s.d.Tickets {
		if t.DoneAt != nil && !t.Bar && !t.ReadyToServe() {
			out = append(out, t.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DoneAt.After(*out[j].DoneAt) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// TicketsForBill lists a bill's tickets (to show staff what's ready).
func (s *Store) TicketsForBill(billID int) map[int]Ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int]Ticket{}
	for _, t := range s.d.Tickets {
		if t.BillID == billID {
			out[t.ID] = t.clone()
		}
	}
	return out
}

// TicketDone: the chef finished it. undo puts it back on the screen.
func (s *Store) TicketDone(id int, undo bool, now time.Time) error {
	return s.changeOrders(func() (bool, error) {
		t := s.ticketByID(id)
		if t == nil {
			return false, ErrNoTicket
		}
		switch {
		case undo && t.ServedAt != nil:
			t.ServedAt = nil // back to "ready to pick up"
		case undo && t.DoneAt != nil:
			t.DoneAt = nil
		case !undo && t.DoneAt == nil:
			d := now
			t.DoneAt = &d
		default:
			return false, nil
		}
		return true, nil
	})
}

// TidyKitchen clears tickets left on the screen from an earlier day.
func (s *Store) TidyKitchen(now time.Time) error {
	today := schedule.FormatDate(schedule.Today(now))
	return s.changeOrders(func() (bool, error) {
		changed := false
		for _, t := range s.d.Tickets {
			day := t.At
			if t.Pickup != nil {
				day = *t.Pickup // takeaway for a later day stays
			}
			if t.DoneAt == nil && schedule.FormatDate(day) < today {
				d := now
				t.DoneAt = &d
				changed = true
			}
			if t.DoneAt != nil && t.ServedAt == nil && t.ForTable() && schedule.FormatDate(*t.DoneAt) < today {
				d := now
				t.ServedAt = &d // left over from an earlier day
				changed = true
			}
		}
		return changed, nil
	})
}

// TicketServed: the food was taken from the kitchen to the table (undo:
// it's back to "ready to pick up").
func (s *Store) TicketServed(id int, undo bool, now time.Time) error {
	return s.changeDeal(func() (bool, error) {
		t := s.ticketByID(id)
		if t == nil || !t.ForTable() {
			return false, ErrNoTicket
		}
		switch {
		case undo && t.ServedAt != nil:
			t.ServedAt = nil
		case !undo && t.DoneAt != nil && t.ServedAt == nil:
			d := now
			t.ServedAt = &d
			s.dealMealServed(t, now)
		case !undo && t.DoneAt == nil:
			return false, ErrNotReady
		default:
			return false, nil
		}
		return true, nil
	})
}

// ErrNotReady: the kitchen hasn't marked it done yet.
var ErrNotReady = errors.New("the kitchen hasn't finished it yet")

// ReadyToServe lists the food done in the kitchen and waiting to be taken
// to its table (oldest first), at tables that are still open, from the
// last 12 hours.
func (s *Store) ReadyToServe(now time.Time) []Ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Ticket
	for _, t := range s.d.Tickets {
		if !t.ReadyToServe() || now.Sub(*t.DoneAt) > 12*time.Hour {
			continue
		}
		if b := s.billByID(t.BillID); b == nil || !b.Open() {
			continue
		}
		out = append(out, t.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DoneAt.Before(*out[j].DoneAt) })
	return out
}
