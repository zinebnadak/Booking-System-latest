// Package db stores bookings, the menu and closed days.
//
// Everything lives in one JSON file on disk. For a restaurant with 6 tables
// that is plenty: a few hundred bookings a month is a tiny file. A mutex makes
// sure two guests can never grab the last table at the same moment, and every
// change is written to a temporary file first and then renamed, so the data
// file is never left half-written if the server crashes.
package db

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"emeal-booking/schedule"
)

var (
	ErrNotFound = errors.New("not found")
	ErrFull     = errors.New("no tables left at that time")

	// ErrNotChangeable: the booking is cancelled, already happened, or the
	// guests are checked in, so it can't be changed any more.
	ErrNotChangeable = errors.New("this booking can't be changed")

	// ErrTooManyPending: this guest already has MaxPendingPerGuest unpaid
	// bookings waiting for payment. Stops one person from holding many tables
	// without paying, or from making the site email one address over and over.
	ErrTooManyPending = errors.New("too many unpaid bookings for this guest")
)

// MaxPendingPerGuest is how many unpaid bookings (tables and takeaway
// together) one guest may have waiting for payment at the same time.
const MaxPendingPerGuest = 3

type data struct {
	NextBookingID int           `json:"next_booking_id"`
	NextMenuID    int           `json:"next_menu_id"`
	MenuVersion   int           `json:"menu_version,omitempty"` // see upgradeMenu
	Bookings      []*Booking    `json:"bookings"`
	Menu          []*MenuItem   `json:"menu"`
	ClosedDates   []*ClosedDate `json:"closed_dates"`
	NextWaitID    int           `json:"next_wait_id"`
	Waitlist      []*WaitEntry  `json:"waitlist"`

	// Table buttons and the "3 meals" deal (see tables.go).
	Buttons   []*Button  `json:"buttons"`
	MealRuns  []*MealRun `json:"meal_runs"`
	Calls     []*Call    `json:"calls"`
	NextRunID int        `json:"next_run_id"`

	// Saved orders of the 3 meals ("Ris, fisk, kött"), and the menu item
	// that puts the deal's price on the bill (see mealplans.go).
	MealPlans      []*MealPlan `json:"meal_plans,omitempty"`
	NextMealPlanID int         `json:"next_meal_plan_id,omitempty"`
	DealItemID     int         `json:"deal_item_id,omitempty"`
	Removals       []*Removal  `json:"removals,omitempty"`
	NextRemovalID  int         `json:"next_removal_id,omitempty"`

	// Table bills and the chef's tickets (see orders.go).
	Bills        []*Bill   `json:"bills"`
	Tickets      []*Ticket `json:"tickets"`
	NextBillID   int       `json:"next_bill_id"`
	NextTicketID int       `json:"next_ticket_id"`

	// QR ordering and paying on the phone (see guests.go).
	TableQRs    []*TableQR    `json:"table_qrs"`
	PayAttempts []*PayAttempt `json:"pay_attempts"`

	// Phone orders waiting for a waiter (see phoneorders.go), and settings
	// staff change on their screens.
	PhoneOrders      []*PhoneOrder `json:"phone_orders"`
	NextPhoneOrderID int           `json:"next_phone_order_id"`
	Settings         Settings      `json:"settings"`

	// Guests' feedback (see reviews.go).
	Reviews      []*Review `json:"reviews"`
	NextReviewID int       `json:"next_review_id"`

	// Waiters' logins and what staff did with money (see staffaccounts.go).
	Staff       []*StaffMember `json:"staff"`
	NextStaffID int            `json:"next_staff_id"`
	Activity    []*Activity    `json:"activity"`

	// Gift cards and lunch booklets (see vouchers.go).
	Vouchers []*Voucher `json:"vouchers,omitempty"`

	// The cash drawer counted at closing (see sales.go), and the phones
	// that get push notifications (see push.go).
	CashCounts []*CashCount `json:"cash_counts,omitempty"`
	PushSubs   []*PushSub   `json:"push_subs,omitempty"`

	// The staff schedule the manager plans (see rota.go).
	Rota       []*RotaShift `json:"rota,omitempty"`
	NextRotaID int          `json:"next_rota_id,omitempty"`

	// The clock card (see shifts.go).
	Shifts      []*Shift `json:"shifts,omitempty"`
	NextShiftID int      `json:"next_shift_id,omitempty"`

	// Ingredients in stock (spaghetti, cheese…) that dishes use: see stock.go.
	Ingredients      []*Ingredient `json:"ingredients,omitempty"`
	NextIngredientID int           `json:"next_ingredient_id,omitempty"`
}

// Settings are switches staff flip on their screens.
type Settings struct {
	// CheckPhoneOrders: phone orders wait for a waiter to accept them
	// (or are accepted by themselves after AutoAcceptAfter).
	CheckPhoneOrders bool `json:"check_phone_orders,omitempty"`
	// DailyReportSent: the last day whose end-of-day email went out.
	DailyReportSent string `json:"daily_report_sent,omitempty"`
	// RoomsOffline: rooms guests can't book online right now (e.g. the
	// terrace in winter). Staff can still seat people there.
	RoomsOffline []string `json:"rooms_offline,omitempty"`
	// ManagerPIN lets the manager approve on a waiter's device: a slow
	// fingerprint of the PIN, never the PIN (see removals.go).
	ManagerPIN string `json:"manager_pin,omitempty"`
	// WoltPct is how many percent of each Wolt order Wolt keeps (see sales.go).
	WoltPct    int  `json:"wolt_pct,omitempty"`
	WoltPctSet bool `json:"wolt_pct_set,omitempty"`
	// CashFloat is the change the cash drawer starts the day with.
	CashFloat int `json:"cash_float,omitempty"`
	// Plan is the manager's table plan (see tableplan.go); nil: the default.
	Plan *PlanSetting `json:"table_plan,omitempty"`
	// BusinessID is the Y-tunnus / FO-nummer printed on receipts (see business.go).
	BusinessID string `json:"business_id,omitempty"`
	// VAPID is this server's key for push notifications (see push.go).
	VAPID string `json:"vapid,omitempty"`
}

// Store is the database. Create it with Open.
type Store struct {
	mu   sync.Mutex
	path string
	d    data

	// Clock tells the time for checks that don't get it passed in (whether
	// a phone payment's hold ran out). The server sets it to its own clock.
	Clock func() time.Time
}

func (s *Store) clock() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

// Open loads the data file, creating it (with the starting menu) if missing.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.d = data{NextBookingID: 1, NextMenuID: 1, NextWaitID: 1, MenuVersion: menuVersion}
		for _, it := range startMenu() {
			item := it
			item.ID = s.d.NextMenuID
			s.d.NextMenuID++
			s.d.Menu = append(s.d.Menu, &item)
		}
		if err := s.save(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(raw, &s.d); err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		if s.d.NextWaitID == 0 { // data file from before the waiting list existed
			s.d.NextWaitID = 1
		}
	}
	if err := s.usePlan(); err != nil { // the manager's table plan
		return nil, fmt.Errorf("table plan in %s: %w", path, err)
	}
	addedButtons := s.ensureButtons()  // every table gets a secret button code
	addedRefs := s.ensureBillRefs()    // and every bill a code for its online copy
	addedQRs := s.ensureTableQRs()     // and every table a code for its QR sticker
	addedCodes := s.ensureTableCodes() // and every table booking its 4-digit code
	newMenu := s.upgradeMenu()         // the restaurant's menu instead of the demo one
	if addedButtons || addedRefs || addedQRs || addedCodes || newMenu {
		if err := s.save(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// save writes everything to disk safely. Callers must hold s.mu.
func (s *Store) save() error {
	raw, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newRef() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// ---------- Bookings ----------

// tableNeed is what a booking needs from a table.
type tableNeed struct {
	size   int  // at least this many seats (0 = any table)
	online bool // booked on the website: not in rooms closed for online bookings
}

// freeTablesFor lists the tables that are free for a whole booking starting
// at clock and lasting minutes (0 = the normal 2 hours), and fit need: the
// best fit first (fewest seats that are enough), then in plan order. skip is
// a booking to ignore (when moving it). Callers hold s.mu.
func (s *Store) freeTablesFor(date, clock string, minutes int, need tableNeed, now time.Time, skip *Booking) []int {
	used := map[int]bool{}
	unnumbered := 0 // bookings from before table numbers existed
	for _, b := range s.d.Bookings {
		if b == skip || b.Date != date || !b.HoldsTable(now) || !schedule.OverlapsFor(b.Time, b.Minutes, clock, minutes) {
			continue
		}
		if schedule.ValidTable(b.TableNo) {
			used[b.TableNo] = true
		} else {
			unnumbered++
		}
	}
	// Guests seated on Orders (an open bill) hold their table too, booked or
	// not: from when it opened for the normal stay, and while the bill is
	// still open at least the next half hour.
	if date == schedule.FormatDate(now) {
		if start, err := schedule.ParseClock(clock); err == nil {
			end := start + schedule.StayMinutes(minutes)
			nowM := minuteOfDay(now)
			for _, b := range s.d.Bills {
				if !b.Open() || b.SkippedAt != nil || b.Wolt != "" || !schedule.ValidTable(b.Table) {
					continue
				}
				opened := 0 // opened on an earlier day and still open
				if schedule.FormatDate(b.OpenedAt) == date {
					opened = minuteOfDay(b.OpenedAt)
				}
				until := max(opened+schedule.StayMinutes(0), nowM+30)
				if start < until && opened < end {
					used[b.Table] = true
				}
			}
		}
	}
	offline := map[string]bool{}
	if need.online {
		for _, r := range s.d.Settings.RoomsOffline {
			offline[r] = true
		}
	}
	var free []schedule.Table
	for _, t := range schedule.Tables() {
		if !used[t.No] && t.Seats >= need.size && !offline[t.Room] {
			free = append(free, t)
		}
	}
	sort.SliceStable(free, func(i, j int) bool { return free[i].Seats < free[j].Seats })
	if unnumbered >= len(free) {
		return nil
	}
	out := make([]int, 0, len(free)-unnumbered)
	for _, t := range free[:len(free)-unnumbered] {
		out = append(out, t.No)
	}
	return out
}

// minuteOfDay is the time of t in minutes after midnight, restaurant time.
func minuteOfDay(t time.Time) int {
	t = t.In(schedule.Location)
	return t.Hour()*60 + t.Minute()
}

// FreeTables returns, for each start time, how many tables are still free
// for size guests (0 = any table). online: only rooms open for online bookings.
func (s *Store) FreeTables(date string, clocks []string, size int, online bool, now time.Time) map[string]int {
	return s.FreeTablesSkip(date, clocks, size, online, now, "")
}

// FreeTablesSkip is FreeTables, but pretends the booking skipRef doesn't
// exist. A guest moving their booking can then pick a time that overlaps
// their own old time.
func (s *Store) FreeTablesSkip(date string, clocks []string, size int, online bool, now time.Time, skipRef string) map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var skip *Booking
	if skipRef != "" {
		skip = s.find(skipRef)
	}
	need := tableNeed{size: size, online: online}
	out := make(map[string]int, len(clocks))
	for _, c := range clocks {
		out[c] = len(s.freeTablesFor(date, c, 0, need, now, skip))
	}
	return out
}

// RoomsOffline lists the rooms guests can't book online right now.
func (s *Store) RoomsOffline() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.d.Settings.RoomsOffline...)
}

// SetRoomOnline opens or closes a room for online bookings.
func (s *Store) SetRoomOnline(room string, online bool) error {
	ok := false
	for _, r := range schedule.RoomNames() {
		ok = ok || r == room
	}
	if !ok {
		return ErrNotFound
	}
	return s.changeOrders(func() (bool, error) {
		var keep []string
		for _, r := range s.d.Settings.RoomsOffline {
			if r != room {
				keep = append(keep, r)
			}
		}
		if !online {
			keep = append(keep, room)
		}
		s.d.Settings.RoomsOffline = keep
		return true, nil
	})
}

// ErrDuplicate means this guest already has a paid booking at that time.
type ErrDuplicate struct{ Time string }

func (e ErrDuplicate) Error() string { return "guest already has a booking at " + e.Time }

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sameGuest: same email (any case) or same phone number (any spacing).
func sameGuest(a, b *Booking) bool {
	if a.Email != "" && strings.EqualFold(a.Email, b.Email) {
		return true
	}
	pa, pb := digitsOnly(a.Phone), digitsOnly(b.Phone)
	return len(pa) >= 6 && pa == pb
}

// CreateTableBooking saves a new table booking if a table is still free, and
// gives it a table number. Everything happens under one lock, so two guests
// can never get the same table.
//
// The same guest can't hold two bookings at overlapping times. If they have
// an unpaid one (they left the payment page and started again), that one is
// released and replaced; a paid one gives ErrDuplicate.
func (s *Store) CreateTableBooking(b Booking, now time.Time) (Booking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b.Kind = KindTable
	b.Status = StatusPending
	var replaced []*Booking
	for _, o := range s.d.Bookings {
		if o.Date != b.Date || !o.HoldsTable(now) || !schedule.OverlapsFor(o.Time, o.Minutes, b.Time, 0) || !sameGuest(o, &b) {
			continue
		}
		if o.Status == StatusPaid {
			return Booking{}, ErrDuplicate{Time: o.Time}
		}
		replaced = append(replaced, o)
	}
	if s.pendingFor(&b, now, replaced) >= MaxPendingPerGuest {
		return Booking{}, ErrTooManyPending
	}
	for _, o := range replaced {
		o.Status = StatusExpired
	}
	b.Minutes = 0
	free := s.freeTablesFor(b.Date, b.Time, 0, tableNeed{size: b.PartySize, online: true}, now, nil)
	if len(free) == 0 {
		for _, o := range replaced {
			o.Status = StatusPending
		}
		return Booking{}, ErrFull
	}
	b.TableNo = free[0]
	created, err := s.insert(b, now)
	if err != nil {
		for _, o := range replaced {
			o.Status = StatusPending
		}
	}
	return created, err
}

// MoveTable puts a table booking on another table, if that table is free
// for the whole booking.
func (s *Store) MoveTable(ref string, table int, now time.Time) (Booking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.find(ref)
	if b == nil || b.Kind != KindTable {
		return Booking{}, ErrNotFound
	}
	ok := false
	for _, t := range s.freeTablesFor(b.Date, b.Time, b.Minutes, tableNeed{}, now, b) {
		if t == table {
			ok = true
		}
	}
	if !ok {
		return Booking{}, ErrFull
	}
	old := b.TableNo
	b.TableNo = table
	if err := s.save(); err != nil {
		b.TableNo = old
		return Booking{}, err
	}
	return b.clone(), nil
}

// TablesFreeFor lists the other tables a booking could be moved to.
func (s *Store) TablesFreeFor(ref string, now time.Time) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.find(ref)
	if b == nil || b.Kind != KindTable {
		return nil
	}
	var out []int
	for _, t := range s.freeTablesFor(b.Date, b.Time, b.Minutes, tableNeed{}, now, b) {
		if t != b.TableNo {
			out = append(out, t)
		}
	}
	return out
}

// CheckIn marks a paid table booking as arrived. This is final: a check-in
// can't be undone, and an arrived booking can't be cancelled.
func (s *Store) CheckIn(ref string, now time.Time) (Booking, bool, error) {
	return s.update(ref, func(b *Booking) bool {
		if b.Kind != KindTable || b.Status != StatusPaid || b.ArrivedAt != nil {
			return false
		}
		t := now
		b.ArrivedAt = &t
		return true
	})
}

// CreateTakeaway saves a new takeaway order.
func (s *Store) CreateTakeaway(b Booking, now time.Time) (Booking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b.Kind = KindTakeaway
	b.Status = StatusPending
	if s.pendingFor(&b, now, nil) >= MaxPendingPerGuest {
		return Booking{}, ErrTooManyPending
	}
	return s.insert(b, now)
}

// pendingFor counts this guest's unpaid bookings whose hold hasn't run out,
// leaving out the ones in skip (they are about to be replaced).
func (s *Store) pendingFor(b *Booking, now time.Time, skip []*Booking) int {
	n := 0
next:
	for _, o := range s.d.Bookings {
		if o.Status != StatusPending || !now.Before(o.HoldExpiresAt) || !sameGuest(o, b) {
			continue
		}
		for _, x := range skip {
			if x == o {
				continue next
			}
		}
		n++
	}
	return n
}

func (s *Store) insert(b Booking, now time.Time) (Booking, error) {
	b.ID = s.d.NextBookingID
	b.Ref = newRef()
	if b.Kind == KindTable && b.TableCode == "" {
		b.TableCode = newTableCode()
	}
	b.CreatedAt = now
	b.HoldExpiresAt = now.Add(schedule.HoldDuration)
	stored := b.clone()
	s.d.Bookings = append(s.d.Bookings, &stored)
	s.d.NextBookingID++
	if err := s.save(); err != nil {
		s.d.Bookings = s.d.Bookings[:len(s.d.Bookings)-1]
		s.d.NextBookingID--
		return Booking{}, err
	}
	return stored.clone(), nil
}

func (s *Store) find(ref string) *Booking {
	for _, b := range s.d.Bookings {
		if b.Ref == ref {
			return b
		}
	}
	return nil
}

// Get returns the booking with this reference code.
func (s *Store) Get(ref string) (Booking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.find(ref)
	if b == nil {
		return Booking{}, ErrNotFound
	}
	return b.clone(), nil
}

// update runs fn on a booking and saves. fn returns false to skip saving.
func (s *Store) update(ref string, fn func(b *Booking) bool) (Booking, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.find(ref)
	if b == nil {
		return Booking{}, false, ErrNotFound
	}
	before := b.clone()
	if !fn(b) {
		return b.clone(), false, nil
	}
	if err := s.save(); err != nil {
		*b = before // undo the change in memory
		return Booking{}, false, err
	}
	return b.clone(), true, nil
}

// SetCheckout remembers the Stripe checkout session for a booking.
func (s *Store) SetCheckout(ref, sessionID, url string) error {
	_, _, err := s.update(ref, func(b *Booking) bool {
		b.CheckoutSessionID, b.CheckoutURL = sessionID, url
		return true
	})
	return err
}

// MarkPaid confirms a booking. changed is true only the first time, so
// confirmation emails are sent exactly once even if Stripe tells us twice.
func (s *Store) MarkPaid(ref, paymentIntentID string, now time.Time) (Booking, bool, error) {
	return s.update(ref, func(b *Booking) bool {
		if b.Status != StatusPending && b.Status != StatusExpired {
			return false
		}
		b.Status = StatusPaid
		b.PaymentIntentID = paymentIntentID
		t := now
		b.PaidAt = &t
		return true
	})
}

// MarkExpired releases an unpaid booking.
func (s *Store) MarkExpired(ref string) error {
	_, _, err := s.update(ref, func(b *Booking) bool {
		if b.Status != StatusPending {
			return false
		}
		b.Status = StatusExpired
		return true
	})
	return err
}

// Cancel cancels a booking; refunded says whether the deposit was paid back,
// by says who cancelled ("guest" or "staff").
func (s *Store) Cancel(ref string, refunded bool, by string, now time.Time) (Booking, bool, error) {
	return s.update(ref, func(b *Booking) bool {
		if b.Status == StatusCancelled || b.Status == StatusRefunded {
			return false
		}
		b.CancelledBy = by
		if refunded {
			b.Status = StatusRefunded
		} else {
			b.Status = StatusCancelled
		}
		t := now
		b.CancelledAt = &t
		return true
	})
}

// BookingsOn returns all bookings for a date, sorted by time.
// Unpaid bookings whose hold ran out are left out.
func (s *Store) BookingsOn(date string, now time.Time) []Booking {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Booking
	for _, b := range s.d.Bookings {
		if b.Date == date && b.EffectiveStatus(now) != StatusExpired {
			out = append(out, b.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// BookingsBetween returns every booking (any status) whose date is between
// from and to, inclusive. Used by the reports.
func (s *Store) BookingsBetween(from, to string) []Booking {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Booking
	for _, b := range s.d.Bookings {
		if b.Date >= from && b.Date <= to {
			out = append(out, b.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Time < out[j].Time
	})
	return out
}

// DayCount is how many confirmed bookings and orders a day has.
type DayCount struct {
	Tables   int
	Guests   int
	Takeaway int
}

// CountsBetween returns paid bookings per date between from and to (inclusive).
func (s *Store) CountsBetween(from, to string) map[string]DayCount {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]DayCount{}
	for _, b := range s.d.Bookings {
		if b.Status != StatusPaid || b.Date < from || b.Date > to {
			continue
		}
		c := out[b.Date]
		if b.Kind == KindTable {
			c.Tables++
			c.Guests += b.PartySize
		} else {
			c.Takeaway++
		}
		out[b.Date] = c
	}
	return out
}

// ---------- Menu ----------

// Menu returns the dishes, sorted. onlyAvailable hides switched-off dishes
// and what can't be ordered now (sold out today, out of stock).
func (s *Store) Menu(onlyAvailable bool) []MenuItem {
	return s.menu(onlyAvailable, onlyAvailable)
}

// StaffMenu is what waiters pick from: what's switched on, at the price
// now, including what's sold out or out of stock (shown greyed out, so they
// can tell a guest who asks for it straight away).
func (s *Store) StaffMenu() []MenuItem {
	return s.menu(true, false)
}

func (s *Store) menu(onlyAvailable, hideGone bool) []MenuItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []MenuItem
	now := s.clock()
	today := schedule.FormatDate(schedule.Today(now))
	happy := schedule.HappyHourAt(now)
	for _, m := range s.d.Menu {
		if onlyAvailable && (!m.Available || m.Ended(today)) {
			continue // switched off, or a special whose days are over
		}
		item := *m
		item.Allergens = append([]string(nil), m.Allergens...)
		item.Uses = append([]IngredientUse(nil), m.Uses...)
		s.fillCanMake(&item)
		if hideGone && item.Gone(today) {
			continue // sold out today, or out of stock
		}
		if onlyAvailable && happy && item.HappyCents > 0 {
			item.PriceCents, item.Happy = item.HappyCents, true // cocktail hour
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sort != out[j].Sort {
			return out[i].Sort < out[j].Sort
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// GuestMenu is what guests can order themselves (on their phone at the
// table): available, and with a price.
func (s *Store) GuestMenu() []MenuItem {
	var out []MenuItem
	for _, m := range s.Menu(true) {
		if m.PriceCents > 0 && !m.StaffOnly {
			out = append(out, m)
		}
	}
	return out
}

// TakeawayMenu is the food guests can order for pickup: available, priced,
// no drinks.
func (s *Store) TakeawayMenu() []MenuItem {
	var out []MenuItem
	for _, m := range s.GuestMenu() {
		if !m.Drink {
			out = append(out, m)
		}
	}
	return out
}

// MenuItem returns one dish.
func (s *Store) MenuItem(id int) (MenuItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.d.Menu {
		if m.ID == id {
			item := *m
			item.Allergens = append([]string(nil), m.Allergens...)
			item.Uses = append([]IngredientUse(nil), m.Uses...)
			s.fillCanMake(&item)
			return item, nil
		}
	}
	return MenuItem{}, ErrNotFound
}

// SaveMenuItem creates a dish (ID 0) or updates an existing one.
func (s *Store) SaveMenuItem(item MenuItem) (MenuItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item.ID == 0 {
		item.ID = s.d.NextMenuID
		s.d.NextMenuID++
		item.Allergens = CleanAllergens(item.Allergens)
		item.Uses = s.cleanUses(item.Uses)
		item.ShortOf, item.CanMake, item.Limited = "", 0, false
		stored := item
		s.d.Menu = append(s.d.Menu, &stored)
		return item, s.save()
	}
	for _, m := range s.d.Menu {
		if m.ID == item.ID {
			// The chef's "sold out", the stock count and the photo have their
			// own buttons.
			item.SoldOutOn, item.Photo = m.SoldOutOn, m.Photo
			item.Counted, item.Left = m.Counted, m.Left
			item.Allergens = CleanAllergens(item.Allergens)
			item.Uses = s.cleanUses(item.Uses)
			item.ShortOf, item.CanMake, item.Limited = "", 0, false
			*m = item
			return item, s.save()
		}
	}
	return MenuItem{}, ErrNotFound
}

// Path is where the data file is (photos live in a folder next to it).
func (s *Store) Path() string { return s.path }

// SetMenuPhoto sets a dish's photo ("" = none) and returns the old one, so
// its file can be removed.
func (s *Store) SetMenuPhoto(id int, photo string) (old string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.d.Menu {
		if m.ID == id {
			old, m.Photo = m.Photo, photo
			return old, s.save()
		}
	}
	return "", ErrNotFound
}

// SetSoldOut: the chef marks a dish sold out for the rest of today, or
// available again.
func (s *Store) SetSoldOut(id int, soldOut bool, now time.Time) (MenuItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.d.Menu {
		if m.ID == id {
			m.SoldOutOn = ""
			if soldOut {
				m.SoldOutOn = schedule.FormatDate(schedule.Today(now))
			} else if m.OutOfStock() {
				m.Counted, m.Left = false, 0 // "back on" from the kitchen: back in stock too
			}
			return *m, s.save()
		}
	}
	return MenuItem{}, ErrNotFound
}

// DeleteMenuItem removes a dish. Old orders keep their copy of name and price.
func (s *Store) DeleteMenuItem(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range s.d.Menu {
		if m.ID == id {
			s.d.Menu = append(s.d.Menu[:i], s.d.Menu[i+1:]...)
			return s.save()
		}
	}
	return ErrNotFound
}

// ---------- Closed dates ----------

// ClosedReason returns why a date is closed, and false if it's open.
func (s *Store) ClosedReason(date string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.d.ClosedDates {
		if c.Date == date {
			return c.Reason, true
		}
	}
	return "", false
}

// ClosedDates lists all closed dates, sorted.
func (s *Store) ClosedDates() []ClosedDate {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ClosedDate, 0, len(s.d.ClosedDates))
	for _, c := range s.d.ClosedDates {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// AddClosedDate marks a day as closed (or updates its reason).
func (s *Store) AddClosedDate(date, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.d.ClosedDates {
		if c.Date == date {
			c.Reason = reason
			return s.save()
		}
	}
	s.d.ClosedDates = append(s.d.ClosedDates, &ClosedDate{Date: date, Reason: reason})
	return s.save()
}

// RemoveClosedDate opens a day again.
func (s *Store) RemoveClosedDate(date string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.d.ClosedDates {
		if c.Date == date {
			s.d.ClosedDates = append(s.d.ClosedDates[:i], s.d.ClosedDates[i+1:]...)
			return s.save()
		}
	}
	return ErrNotFound
}
