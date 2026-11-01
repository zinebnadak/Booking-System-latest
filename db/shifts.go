package db

import (
	"errors"
	"sort"
	"time"
)

// The clock card ("Klockkort"): waiters clock in and out at the till with
// their number and PIN, and the manager sees the hours.

var (
	ErrClockedIn  = errors.New("already clocked in")
	ErrClockedOut = errors.New("not clocked in")
	ErrBadShift   = errors.New("a shift ends after it starts, and lasts at most 16 hours")
)

// MaxShift is the longest a shift can be (a forgotten clock-out is fixed by
// the manager, not counted as days of work).
const MaxShift = 16 * time.Hour

// Shift is one stretch of work.
type Shift struct {
	ID      int        `json:"id"`
	StaffID int        `json:"staff_id"`
	Name    string     `json:"name"`
	In      time.Time  `json:"in"`
	Out     *time.Time `json:"out,omitempty"`
	Fixed   bool       `json:"fixed,omitempty"` // the manager changed the times
}

// Length is how long the shift lasted (or has lasted until now).
func (sh Shift) Length(now time.Time) time.Duration {
	end := now
	if sh.Out != nil {
		end = *sh.Out
	}
	if end.Before(sh.In) {
		return 0
	}
	return end.Sub(sh.In)
}

func (s *Store) openShift(staffID int) *Shift {
	for i := len(s.d.Shifts) - 1; i >= 0; i-- {
		if sh := s.d.Shifts[i]; sh.StaffID == staffID && sh.Out == nil {
			return sh
		}
	}
	return nil
}

// ClockIn starts a waiter's shift.
func (s *Store) ClockIn(m StaffMember, now time.Time) (Shift, error) {
	var out Shift
	err := s.changeOrders(func() (bool, error) {
		if s.openShift(m.ID) != nil {
			return false, ErrClockedIn
		}
		s.d.NextShiftID++
		sh := &Shift{ID: s.d.NextShiftID, StaffID: m.ID, Name: m.Name, In: now}
		s.d.Shifts = append(s.d.Shifts, sh)
		out = *sh
		return true, nil
	})
	return out, err
}

// ClockOut ends a waiter's shift.
func (s *Store) ClockOut(m StaffMember, now time.Time) (Shift, error) {
	var out Shift
	err := s.changeOrders(func() (bool, error) {
		sh := s.openShift(m.ID)
		if sh == nil {
			return false, ErrClockedOut
		}
		t := now
		sh.Out = &t
		out = *sh
		return true, nil
	})
	return out, err
}

// ClockedIn reports whether a waiter is on a shift now.
func (s *Store) ClockedIn(staffID int) (Shift, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sh := s.openShift(staffID); sh != nil {
		return *sh, true
	}
	return Shift{}, false
}

// ShiftsBetween lists shifts that started in [from, to), oldest first.
func (s *Store) ShiftsBetween(from, to time.Time) []Shift {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Shift
	for _, sh := range s.d.Shifts {
		if !sh.In.Before(from) && sh.In.Before(to) {
			c := *sh
			if sh.Out != nil {
				t := *sh.Out
				c.Out = &t
			}
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].In.Before(out[j].In) })
	return out
}

// FixShift sets a shift's times (a forgotten clock-out, a wrong tap).
func (s *Store) FixShift(id int, in, out time.Time) error {
	if !out.After(in) || out.Sub(in) > MaxShift {
		return ErrBadShift
	}
	return s.changeOrders(func() (bool, error) {
		for _, sh := range s.d.Shifts {
			if sh.ID == id {
				o := out
				sh.In, sh.Out, sh.Fixed = in, &o, true
				return true, nil
			}
		}
		return false, ErrNotFound
	})
}
