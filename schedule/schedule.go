// Package schedule holds the restaurant's business rules:
// opening hours, booking slots, pickup slots and deposits.
//
// Everything that the owner might want to change one day (hours, deposit
// amounts, how long a booking lasts) lives in this one file.
package schedule

import (
	"fmt"
	"time"
	_ "time/tzdata" // embeds time zone data so "Europe/Mariehamn" works on any server
)

// ---- Rules agreed with the restaurant ----

const (
	MaxPartySize    = 6                // one online booking = one table; bigger groups call
	BookingDuration = 2 * time.Hour    // every table booking lasts 2 hours
	TableSlotStep   = 30 * time.Minute // table bookings start every 30 min

	PickupSlotStep = 15 * time.Minute // takeaway pickup every 15 min
	PickupLeadTime = 30 * time.Minute // earliest pickup = 30 min after ordering
	KitchenWarmup  = 30 * time.Minute // first pickup = 30 min after opening

	TableLeadTime         = 60 * time.Minute // table must be booked at least 1h ahead (assumption, easy to change)
	TableBookingDaysAhead = 60               // how far ahead tables can be booked (assumption)
	TakeawayDaysAhead     = 7                // how far ahead takeaway can be ordered (assumption)

	// HoldDuration is how long an unpaid booking keeps its table while the
	// guest is on the payment page. Stripe's checkout link expires a few
	// minutes before this, so a guest can never pay for a released table.
	HoldDuration = 35 * time.Minute

	SmallPartyMax       = 2    // 1–2 people pay the small deposit
	SmallPartyDeposit   = 1000 // €10 in cents
	LargePartyDeposit   = 2000 // €20 in cents
	TakeawayDepositPct  = 25   // takeaway deposit = 25% of the food total
	MinimumDepositCents = 50   // Stripe cannot charge less than €0.50

	RefundWindow = 24 * time.Hour // table deposit refunded if cancelled 24h+ before

	// NoShowAfter: if the manager hasn't pressed "Arrived" this long after
	// the booking starts, it becomes a no-show (deposit kept, table freed).
	NoShowAfter = 20 * time.Minute

	// TableCodeEarly: guests who booked can unlock their table's QR code
	// with their table code from this long before their booking starts
	// (until it turns into a no-show).
	TableCodeEarly = 30 * time.Minute

	// TakeawayKitchenHeadsUp: a takeaway order the chef has seen comes back
	// on the kitchen screen this long before pickup, to start cooking.
	TakeawayKitchenHeadsUp = time.Hour
)

// ---- Rooms and tables ----
//
// Copied from the old till's table map: three rooms, with the same table
// numbers the staff already know (151 and 161 are the small tables next to
// 15 and 16). The seats are a first guess from the shapes on that map: check
// them with the owner. An online booking gets the smallest free table with
// enough seats, so a couple doesn't take a table for eight.

// Table is one table in the restaurant.
type Table struct {
	No    int
	Room  string
	Seats int
}

// Rooms in the order the staff screens show them.
var Rooms = []string{"Utrymme 1", "Utrymme 2", "Terrassen"}

// TablePlan is every table, room by room.
var TablePlan = []Table{
	// Utrymme 1: the dining room with the bar counter
	{1, "Utrymme 1", 2}, {2, "Utrymme 1", 4}, {3, "Utrymme 1", 2}, {4, "Utrymme 1", 2},
	{5, "Utrymme 1", 4}, {6, "Utrymme 1", 6}, {7, "Utrymme 1", 8},
	{8, "Utrymme 1", 4}, {9, "Utrymme 1", 4}, {10, "Utrymme 1", 4}, {11, "Utrymme 1", 4},
	{12, "Utrymme 1", 4}, {13, "Utrymme 1", 4}, {14, "Utrymme 1", 4},
	{15, "Utrymme 1", 4}, {151, "Utrymme 1", 2}, {16, "Utrymme 1", 4}, {161, "Utrymme 1", 2},
	{17, "Utrymme 1", 4}, {18, "Utrymme 1", 2},

	// Utrymme 2
	{19, "Utrymme 2", 8}, {20, "Utrymme 2", 6},
	{21, "Utrymme 2", 4}, {22, "Utrymme 2", 4}, {23, "Utrymme 2", 4}, {24, "Utrymme 2", 4},
	{25, "Utrymme 2", 4}, {26, "Utrymme 2", 4}, {27, "Utrymme 2", 4}, {28, "Utrymme 2", 4},
	{29, "Utrymme 2", 4}, {30, "Utrymme 2", 4}, {31, "Utrymme 2", 4},

	// Terrassen
	{50, "Terrassen", 4}, {51, "Terrassen", 4}, {52, "Terrassen", 2}, {53, "Terrassen", 4},
	{54, "Terrassen", 4}, {55, "Terrassen", 2}, {56, "Terrassen", 4}, {57, "Terrassen", 4},
	{58, "Terrassen", 2}, {59, "Terrassen", 4}, {60, "Terrassen", 4}, {61, "Terrassen", 4},
	{62, "Terrassen", 6}, {63, "Terrassen", 4}, {64, "Terrassen", 2}, {65, "Terrassen", 4},
	{66, "Terrassen", 2},
}

var tableIndex = indexTables(TablePlan)

func indexTables(plan []Table) map[int]int {
	m := make(map[int]int, len(plan))
	for i, t := range plan {
		m[t.No] = i
	}
	return m
}

// UseTables swaps in another table plan (for tests) and returns a function
// that puts the old one back.
func UseTables(rooms []string, plan []Table) (restore func()) {
	oldRooms, oldPlan, oldIndex := Rooms, TablePlan, tableIndex
	Rooms, TablePlan, tableIndex = rooms, plan, indexTables(plan)
	return func() { Rooms, TablePlan, tableIndex = oldRooms, oldPlan, oldIndex }
}

// TableCount is how many tables the restaurant has.
func TableCount() int { return len(TablePlan) }

// TableNos lists every table number, room by room.
func TableNos() []int {
	out := make([]int, len(TablePlan))
	for i, t := range TablePlan {
		out[i] = t.No
	}
	return out
}

// ValidTable reports whether n is one of the restaurant's tables.
func ValidTable(n int) bool { _, ok := tableIndex[n]; return ok }

// TableInfo returns a table's room and seats.
func TableInfo(n int) (Table, bool) {
	i, ok := tableIndex[n]
	if !ok {
		return Table{}, false
	}
	return TablePlan[i], true
}

// TableOrder is a table's place in the plan (for sorting), -1 if unknown.
func TableOrder(n int) int {
	if i, ok := tableIndex[n]; ok {
		return i
	}
	return -1
}

// VAT (moms) in hundredths of a percent, as on the old till's receipts.
const (
	VATFood    = 1350 // food and alcohol-free drinks in the restaurant: 13,5 %
	VATAlcohol = 2550 // alcoholic drinks (over 1,2 %): 25,5 %
)

// ---- Cocktail hour ----
//
// Drinks with a cocktail-hour price cost that price during these hours
// (from the old till's "Cocktail Hour" buttons). The times are a first
// guess: check them with the owner. A missing day means no cocktail hour.
var HappyHours = map[time.Weekday][2]int{
	time.Monday: {hm(16, 0), hm(18, 0)}, time.Tuesday: {hm(16, 0), hm(18, 0)},
	time.Wednesday: {hm(16, 0), hm(18, 0)}, time.Thursday: {hm(16, 0), hm(18, 0)},
	time.Friday: {hm(16, 0), hm(18, 0)}, time.Saturday: {hm(16, 0), hm(18, 0)},
	time.Sunday: {hm(16, 0), hm(18, 0)},
}

// HappyHourAt reports whether it's cocktail hour at t.
func HappyHourAt(t time.Time) bool {
	t = t.In(Location)
	w, ok := HappyHours[t.Weekday()]
	m := t.Hour()*60 + t.Minute()
	return ok && m >= w[0] && m < w[1]
}

// HappyHourText is today's cocktail hour, e.g. "16:00–18:00" ("" if none).
func HappyHourText(t time.Time) string {
	w, ok := HappyHours[t.In(Location).Weekday()]
	if !ok {
		return ""
	}
	return Clock(w[0]) + "–" + Clock(w[1])
}

// DayHours is one day's opening hours, in minutes after midnight.
type DayHours struct {
	Open         int // restaurant opens
	Close        int // restaurant closes
	KitchenClose int // after this only drinks are served
}

func hm(h, m int) int { return h*60 + m }

// Hours per weekday. A missing day means closed.
var Hours = map[time.Weekday]DayHours{
	time.Monday:    {Open: hm(10, 30), Close: hm(21, 0), KitchenClose: hm(20, 0)},
	time.Tuesday:   {Open: hm(10, 30), Close: hm(21, 0), KitchenClose: hm(20, 0)},
	time.Wednesday: {Open: hm(10, 30), Close: hm(21, 0), KitchenClose: hm(20, 0)},
	time.Thursday:  {Open: hm(10, 30), Close: hm(21, 0), KitchenClose: hm(20, 0)},
	time.Friday:    {Open: hm(10, 30), Close: hm(22, 0), KitchenClose: hm(21, 0)},
	time.Saturday:  {Open: hm(16, 0), Close: hm(22, 0), KitchenClose: hm(21, 0)},
	time.Sunday:    {Open: hm(12, 0), Close: hm(20, 0), KitchenClose: hm(19, 0)},
}

// ---- The "3 meals" weekend deal ----
//
// Guests get three meals, one after the other. When they finish one, they
// press the button on their table; staff see it on the Tables screen, clear
// the plates and bring the next meal.

// MealsInDeal is how many meals the deal has.
const MealsInDeal = 3

// MealDealDays are the days staff can start the deal on a table. The table
// buttons work every day: outside the deal a press simply calls a waiter.
var MealDealDays = map[time.Weekday]bool{time.Saturday: true, time.Sunday: true}

// MealDealToday reports whether the deal runs on this date.
func MealDealToday(date time.Time) bool { return MealDealDays[date.Weekday()] }

// Location is the restaurant's time zone. All dates and times are local.
var Location = mustLoad("Europe/Mariehamn")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

const DateLayout = "2006-01-02"

// ParseDate turns "2026-10-24" into midnight of that day, local time.
func ParseDate(s string) (time.Time, error) {
	return time.ParseInLocation(DateLayout, s, Location)
}

// FormatDate is the opposite of ParseDate.
func FormatDate(t time.Time) string { return t.In(Location).Format(DateLayout) }

// Today returns today's date (midnight) in the restaurant's time zone.
func Today(now time.Time) time.Time {
	y, m, d := now.In(Location).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, Location)
}

// HoursFor returns the opening hours for a date, and false if closed.
func HoursFor(date time.Time) (DayHours, bool) {
	h, ok := Hours[date.Weekday()]
	return h, ok
}

// ParseClock turns "18:30" into minutes after midnight (1110).
func ParseClock(s string) (int, error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 || len(s) != 5 {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	return hm(h, m), nil
}

// Clock turns minutes after midnight into "18:30".
func Clock(minutes int) string { return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60) }

// At combines a date with a clock time like "18:30".
func At(date time.Time, clock string) (time.Time, error) {
	mins, err := ParseClock(clock)
	if err != nil {
		return time.Time{}, err
	}
	y, m, d := date.In(Location).Date()
	return time.Date(y, m, d, mins/60, mins%60, 0, 0, Location), nil
}

// slots lists times from first to last (inclusive) every step,
// keeping only those at or after notBefore.
func slots(date time.Time, first, last int, step time.Duration, notBefore time.Time) []string {
	var out []string
	stepMin := int(step / time.Minute)
	for t := first; t <= last; t += stepMin {
		c := Clock(t)
		start, _ := At(date, c)
		if start.Before(notBefore) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// TableSlots returns the start times a table can be booked on a date.
// The last booking starts 2 hours before closing.
// Slots that are too soon (less than TableLeadTime from now) are left out.
func TableSlots(date, now time.Time) []string {
	h, open := HoursFor(date)
	if !open {
		return nil
	}
	last := h.Close - int(BookingDuration/time.Minute)
	return slots(date, h.Open, last, TableSlotStep, now.Add(TableLeadTime))
}

// PickupSlots returns the takeaway pickup times on a date: from 30 min after
// opening until the kitchen closes, at least 30 min after now.
func PickupSlots(date, now time.Time) []string {
	h, open := HoursFor(date)
	if !open {
		return nil
	}
	first := h.Open + int(KitchenWarmup/time.Minute)
	return slots(date, first, h.KitchenClose, PickupSlotStep, now.Add(PickupLeadTime))
}

// Overlaps reports whether two table bookings starting at a and b share any
// time, given that each lasts BookingDuration.
func Overlaps(a, b string) bool { return OverlapsFor(a, 0, b, 0) }

// OverlapsFor is Overlaps for bookings of any length in minutes
// (0 means the normal 2 hours). A walk-in seated between two bookings can
// be shorter.
func OverlapsFor(a string, aMin int, b string, bMin int) bool {
	am, err1 := ParseClock(a)
	bm, err2 := ParseClock(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return am < bm+minutesOr(bMin) && bm < am+minutesOr(aMin)
}

func minutesOr(m int) int {
	if m <= 0 {
		return int(BookingDuration / time.Minute)
	}
	return m
}

// EndTime returns when a booking starting at clock ends, e.g. "18:00" -> "20:00".
func EndTime(clock string) string { return EndTimeFor(clock, 0) }

// EndTimeFor is EndTime for a booking of any length (0 = 2 hours).
func EndTimeFor(clock string, minutes int) string {
	m, err := ParseClock(clock)
	if err != nil {
		return ""
	}
	return Clock(m + minutesOr(minutes))
}

// TableDeposit: €10 for 1–2 people, €20 for 3–6 people (in cents).
func TableDeposit(partySize int) int {
	if partySize <= SmallPartyMax {
		return SmallPartyDeposit
	}
	return LargePartyDeposit
}

// TakeawayDeposit: 25% of the food total, rounded to the nearest cent,
// never below Stripe's minimum charge.
func TakeawayDeposit(totalCents int) int {
	d := (totalCents*TakeawayDepositPct + 50) / 100
	if d < MinimumDepositCents {
		d = MinimumDepositCents
	}
	return d
}

// RefundEligible reports whether a table booking cancelled at `now` gets its
// deposit back (24+ hours before the booking starts).
func RefundEligible(date, clock string, now time.Time) bool {
	d, err := ParseDate(date)
	if err != nil {
		return false
	}
	start, err := At(d, clock)
	if err != nil {
		return false
	}
	return start.Sub(now) >= RefundWindow
}
