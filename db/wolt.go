package db

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"emeal-booking/schedule"
)

// ---------- Wolt orders ----------

// A Wolt order is a bill of its own with the name on the order and a Wolt
// number (901, 902… see schedule.IsWolt) instead of a table. Staff add the
// dishes like at a table, the chef cooks them, and when the courier takes
// the food it's paid by Wolt (PaidWolt).

// PaidWolt: Wolt pays the restaurant for the order.
const PaidWolt = "wolt"

// MaxWoltName is how long the name on a Wolt order can be.
const MaxWoltName = 60

var (
	ErrWoltName = errors.New("write the name on the Wolt order")
	ErrWoltFull = errors.New("too many Wolt orders open at once")
	ErrNotWolt  = errors.New("not a Wolt order")
)

// OpenWolt starts a Wolt order with the name on it, on the first free Wolt
// number, and returns its bill.
func (s *Store) OpenWolt(name string, now time.Time) (Bill, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return Bill{}, ErrWoltName
	}
	if utf8.RuneCountInString(name) > MaxWoltName {
		name = string([]rune(name)[:MaxWoltName])
	}
	var out Bill
	err := s.changeOrders(func() (bool, error) {
		for n := schedule.WoltFirst; n <= schedule.WoltLast; n++ {
			if s.openBillAt(n) != nil {
				continue
			}
			b := s.newBill(n, "", 0, now)
			b.Wolt = name
			out = b.clone()
			return true, nil
		}
		return false, ErrWoltFull
	})
	return out, err
}

// WoltName is the name on the open Wolt order with this number ("" if none).
func (s *Store) WoltName(n int) string {
	if !schedule.IsWolt(n) {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.openBillAt(n); b != nil {
		return b.Wolt
	}
	return ""
}

// WoltPickedUp: the courier took the order. It's paid (by Wolt), and its
// food leaves the kitchen's pass.
func (s *Store) WoltPickedUp(billID int, now time.Time) (Bill, error) {
	var out Bill
	err := s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if b.Wolt == "" {
			return false, ErrNotWolt
		}
		if len(b.Lines) == 0 {
			return false, ErrEmptyBill
		}
		t := now
		b.PaidAt, b.PaidBy = &t, PaidWolt
		for _, tk := range s.d.Tickets {
			if tk.BillID == b.ID && tk.ServedAt == nil {
				if tk.DoneAt == nil {
					tk.DoneAt = &t // the courier took it, so it's done
				}
				tk.ServedAt = &t
			}
		}
		out = b.clone()
		return true, nil
	})
	return out, err
}

// CancelEmptyWolt takes away a Wolt order nothing was added to yet
// (started by mistake). One with dishes on it needs the manager (void).
func (s *Store) CancelEmptyWolt(billID int) error {
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if b.Wolt == "" {
			return false, ErrNotWolt
		}
		if len(b.Lines) > 0 {
			return false, ErrNotEmpty
		}
		b.Voided = true
		return true, nil
	})
}

// ErrNotEmpty: there are dishes on it, so only the manager can void it.
var ErrNotEmpty = errors.New("there is something on it: ask the manager")

// TicketByID returns any kitchen ticket.
func (s *Store) TicketByID(id int) (Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.ticketByID(id); t != nil {
		return t.clone(), true
	}
	return Ticket{}, false
}

// WoltTotal is the Wolt orders paid over some days.
type WoltTotal struct {
	Orders int
	Cents  int
}

// WoltByDate adds up the Wolt orders picked up from one date to another
// (both included), per day.
func (s *Store) WoltByDate(from, to string) map[string]WoltTotal {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]WoltTotal{}
	for _, b := range s.d.Bills {
		if b.Wolt == "" || b.PaidAt == nil || b.Voided {
			continue
		}
		d := schedule.FormatDate(*b.PaidAt)
		if d < from || d > to {
			continue
		}
		t := out[d]
		t.Orders++
		t.Cents += b.Total()
		out[d] = t
	}
	return out
}
