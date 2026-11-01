package db

import (
	"errors"
	"sort"
	"time"

	"ozmo-booking/schedule"
)

// Checking phone orders before the chef gets them.
//
// With the switch on (Settings.CheckPhoneOrders), what a guest orders on
// their phone first waits on the Orders screen for a waiter to tap Accept
// or Reject: a dish tapped by mistake, or a prank, never reaches the chef.
// If nobody decides within AutoAcceptAfter, it's accepted by itself, so a
// busy evening never leaves an order stuck.

// AutoAcceptAfter: a phone order nobody checked goes through by itself.
const AutoAcceptAfter = 5 * time.Minute

// ErrOrderPending: a phone order is still waiting for a waiter.
var ErrOrderPending = errors.New("a phone order is waiting for a waiter")

// PhoneOrder is an order from a guest's phone waiting for a waiter.
type PhoneOrder struct {
	ID        int        `json:"id"`
	BillID    int        `json:"bill_id"`
	Table     int        `json:"table"`
	Guest     int        `json:"guest"`
	Lines     []NewLine  `json:"lines"`
	At        time.Time  `json:"at"`
	State     string     `json:"state"` // "pending", "accepted" or "rejected"
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	Auto      bool       `json:"auto,omitempty"` // accepted by itself after AutoAcceptAfter
}

func (o *PhoneOrder) clone() PhoneOrder {
	c := *o
	c.Lines = append([]NewLine(nil), o.Lines...)
	return c
}

// AutoAt is when the order goes through by itself.
func (o PhoneOrder) AutoAt() time.Time { return o.At.Add(AutoAcceptAfter) }

// CheckPhoneOrders reports whether phone orders wait for a waiter.
func (s *Store) CheckPhoneOrders() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Settings.CheckPhoneOrders
}

// SetCheckPhoneOrders turns the check on or off. Turning it off lets
// everything still waiting through at once.
func (s *Store) SetCheckPhoneOrders(on bool, now time.Time) error {
	return s.changeOrders(func() (bool, error) {
		if s.d.Settings.CheckPhoneOrders == on {
			return false, nil
		}
		s.d.Settings.CheckPhoneOrders = on
		if !on {
			for _, o := range s.d.PhoneOrders {
				if o.State == "pending" {
					s.decide(o, true, false, now)
				}
			}
		}
		return true, nil
	})
}

// decide accepts or rejects a waiting order. Accepting puts it on the bill
// (and the food to the chef), unless the guest's bill closed meanwhile.
// Callers hold s.mu.
func (s *Store) decide(o *PhoneOrder, accept, auto bool, now time.Time) {
	t := now
	o.DecidedAt, o.Auto = &t, auto
	o.State = "rejected"
	if !accept {
		s.giveLines(o.Lines) // counted when the guest ordered
		return
	}
	b := s.billByID(o.BillID)
	if b == nil || !b.Open() {
		s.giveLines(o.Lines)
		return
	}
	sh := b.share(o.Guest)
	if sh == nil || sh.PaidAt != nil {
		s.giveLines(o.Lines)
		return
	}
	guest := *sh
	s.addLines(b, o.Lines, &guest, true, now)
	o.State = "accepted"
}

func (s *Store) phoneOrder(id int) *PhoneOrder {
	for _, o := range s.d.PhoneOrders {
		if o.ID == id {
			return o
		}
	}
	return nil
}

// DecidePhoneOrder: a waiter tapped Accept or Reject.
func (s *Store) DecidePhoneOrder(id int, accept bool, now time.Time) (PhoneOrder, error) {
	var out PhoneOrder
	err := s.changeOrders(func() (bool, error) {
		o := s.phoneOrder(id)
		if o == nil {
			return false, ErrNotFound
		}
		if o.State != "pending" {
			return false, ErrStale // another waiter (or the clock) was quicker
		}
		s.decide(o, accept, false, now)
		out = o.clone()
		return true, nil
	})
	return out, err
}

// AutoAcceptPhoneOrders lets through the orders nobody checked in time.
// It reports whether any went through, so the screens can be told.
func (s *Store) AutoAcceptPhoneOrders(now time.Time) bool {
	any := false
	_ = s.changeOrders(func() (bool, error) {
		for _, o := range s.d.PhoneOrders {
			if o.State == "pending" && !now.Before(o.AutoAt()) {
				s.decide(o, true, true, now)
				any = true
			}
		}
		return any, nil
	})
	return any
}

// PendingPhoneOrders lists the orders waiting for a waiter, oldest first.
func (s *Store) PendingPhoneOrders() []PhoneOrder {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PhoneOrder
	for _, o := range s.d.PhoneOrders {
		if o.State == "pending" {
			out = append(out, o.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// GuestPhoneOrders lists a guest's waiting orders and the ones a waiter
// rejected in the last 15 minutes (so the guest sees what happened).
func (s *Store) GuestPhoneOrders(billID, g int, now time.Time) (pending, rejected []PhoneOrder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.d.PhoneOrders {
		if o.BillID != billID || o.Guest != g {
			continue
		}
		switch {
		case o.State == "pending":
			pending = append(pending, o.clone())
		case o.State == "rejected" && o.DecidedAt != nil && now.Sub(*o.DecidedAt) < 15*time.Minute:
			rejected = append(rejected, o.clone())
		}
	}
	return pending, rejected
}

// ordersWaiting reports whether any of these guests on a bill has an order
// waiting for a waiter (all: anyone on the bill). Callers hold s.mu.
func (s *Store) ordersWaiting(billID int, guests []int, all bool) bool {
	for _, o := range s.d.PhoneOrders {
		if o.BillID != billID || o.State != "pending" {
			continue
		}
		if all {
			return true
		}
		for _, g := range guests {
			if g == o.Guest {
				return true
			}
		}
	}
	return false
}

// DailyReportDue reports whether the end-of-day email for this day hasn't
// gone out yet.
func (s *Store) DailyReportDue(date string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Settings.DailyReportSent < date
}

// DailyReportSent remembers that a day's end-of-day email went out.
func (s *Store) MarkDailyReportSent(date string) error {
	return s.changeOrders(func() (bool, error) {
		if s.d.Settings.DailyReportSent >= date {
			return false, nil
		}
		s.d.Settings.DailyReportSent = date
		return true, nil
	})
}

// BillsOpenedOn lists the bills opened on a day (paid, open or voided).
func (s *Store) BillsOpenedOn(date string) []Bill {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Bill
	for _, b := range s.d.Bills {
		if schedule.FormatDate(b.OpenedAt) == date {
			out = append(out, b.clone())
		}
	}
	return out
}
