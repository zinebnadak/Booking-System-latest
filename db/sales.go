package db

import (
	"errors"
	"sort"
	"time"

	"emeal-booking/schedule"
)

// ---------- Money at the tables, the cash drawer and Wolt's cut ----------

// TillDay is the money the restaurant took on one day at the tables (card,
// cash, lunch cards, phones) and for gift cards sold. Wolt orders aren't
// in it (see WoltByDate).
type TillDay struct {
	Bills        int            // table bills paid
	Cents        int            // money in: everything paid, except gift cards and lunch coupons used (paid earlier)
	Prepaid      int            // paid with gift cards and lunch coupons sold earlier
	Tips         int            // tips guests added on their phones
	Methods      map[string]int // what came in, by way of paying (prepaid ones included)
	Vouchers     int            // gift cards and lunch booklets sold
	VoucherCents int            // ...and what they were sold for
	Cash         int            // cash in: bills, gift cards and takeaway pickups paid in cash
	PickupCash   int            // ...of which takeaway paid in cash at pickup
	PickupCard   int            // takeaway paid by card at pickup
}

// Total is the money that came in: the bills and the gift cards sold.
func (t TillDay) Total() int { return t.Cents + t.VoucherCents }

// TillByDate adds up the table bills paid and the gift cards sold from one
// date to another (both included), per day. A bill counts on the day it
// was paid.
func (s *Store) TillByDate(from, to string) map[string]*TillDay {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]*TillDay{}
	day := func(d string) *TillDay {
		t := out[d]
		if t == nil {
			t = &TillDay{Methods: map[string]int{}}
			out[d] = t
		}
		return t
	}
	for _, b := range s.d.Bills {
		if b.PaidAt == nil || b.Voided || b.Wolt != "" {
			continue
		}
		d := schedule.FormatDate(*b.PaidAt)
		if d < from || d > to {
			continue
		}
		t := day(d)
		t.Bills++
		t.Tips += b.Tips()
		for m, c := range b.Takings() {
			if m == PaidNothing || c == 0 {
				continue
			}
			t.Methods[m] += c
			switch {
			case PrepaidMethod(m):
				t.Prepaid += c
			default:
				t.Cents += c
			}
			if m == PaidCash {
				t.Cash += c
			}
		}
	}
	for _, b := range s.d.Bookings { // takeaway: the rest paid at pickup
		if b.Kind != KindTakeaway || b.PickedUpAt == nil || b.PickupPaidBy == "" {
			continue
		}
		d := schedule.FormatDate(*b.PickedUpAt)
		if d < from || d > to {
			continue
		}
		left := b.TotalCents - b.DepositCents
		if left <= 0 {
			continue
		}
		t := day(d)
		if b.PickupPaidBy == PaidCash {
			t.PickupCash += left
			t.Cash += left
		} else {
			t.PickupCard += left
		}
	}
	for _, v := range s.d.Vouchers {
		if v.Void {
			continue
		}
		d := schedule.FormatDate(v.SoldAt)
		if d < from || d > to {
			continue
		}
		t := day(d)
		t.Vouchers++
		t.VoucherCents += v.SoldCents
		if v.SoldWith == PaidCash {
			t.Cash += v.SoldCents
		}
	}
	return out
}

// ---------- Wolt's cut ----------

// MaxWoltPct is the highest cut Wolt can be set to take.
const MaxWoltPct = 60

var ErrWoltPct = errors.New("Wolt's cut is a whole number from 0 to 60 %")

// SetWoltPct sets how many percent of each Wolt order Wolt keeps.
func (s *Store) SetWoltPct(pct int) error {
	if pct < 0 || pct > MaxWoltPct {
		return ErrWoltPct
	}
	return s.changeOrders(func() (bool, error) {
		changed := s.d.Settings.WoltPct != pct || !s.d.Settings.WoltPctSet
		s.d.Settings.WoltPct, s.d.Settings.WoltPctSet = pct, true
		return changed, nil
	})
}

// WoltPct is Wolt's cut in percent, and whether the manager has set it.
func (s *Store) WoltPct() (pct int, set bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Settings.WoltPct, s.d.Settings.WoltPctSet
}

// ---------- The cash drawer at closing ----------

// CashCount is the manager counting the cash drawer at the end of a day.
type CashCount struct {
	Date     string    `json:"date"`
	Float    int       `json:"float"`   // change put in the drawer in the morning (växelkassa)
	Taken    int       `json:"taken"`   // cash the system says came in that day
	Counted  int       `json:"counted"` // what was in the drawer
	Note     string    `json:"note,omitempty"`
	By       string    `json:"by,omitempty"`
	At       time.Time `json:"at"`
	Previous int       `json:"previous,omitempty"` // how many times it was counted before (recounts)
}

// Expected is what should be in the drawer: the float and the cash taken.
func (c CashCount) Expected() int { return c.Float + c.Taken }

// Diff is counted minus expected: below 0 money is missing, above 0 there's
// too much.
func (c CashCount) Diff() int { return c.Counted - c.Expected() }

// MaxCash is the most a drawer count can be (a typo guard): 100 000 €.
const MaxCash = 100_000_00

var ErrBadCash = errors.New("check the amounts")

// SaveCashCount saves a day's count (a recount replaces it, and says so).
// The float is remembered for the next day.
func (s *Store) SaveCashCount(c CashCount) (CashCount, error) {
	if c.Float < 0 || c.Counted < 0 || c.Float > MaxCash || c.Counted > MaxCash {
		return CashCount{}, ErrBadCash
	}
	if _, err := schedule.ParseDate(c.Date); err != nil {
		return CashCount{}, ErrBadCash
	}
	err := s.changeOrders(func() (bool, error) {
		kept := make([]*CashCount, 0, len(s.d.CashCounts)+1) // a new slice: the old one stays for a failed save
		for _, o := range s.d.CashCounts {
			if o.Date == c.Date {
				c.Previous = o.Previous + 1
				continue
			}
			kept = append(kept, o)
		}
		s.d.CashCounts = append(kept, &c)
		s.d.Settings.CashFloat = c.Float
		return true, nil
	})
	return c, err
}

// CashCountOn is the day's count, if the drawer was counted.
func (s *Store) CashCountOn(date string) (CashCount, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.d.CashCounts {
		if c.Date == date {
			return *c, true
		}
	}
	return CashCount{}, false
}

// RecentCashCounts are the last n counts up to a date, newest first.
func (s *Store) RecentCashCounts(upTo string, n int) []CashCount {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []CashCount
	for _, c := range s.d.CashCounts {
		if c.Date <= upTo {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// CashFloat is the float the drawer started with last time.
func (s *Store) CashFloat() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Settings.CashFloat
}
