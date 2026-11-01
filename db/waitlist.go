package db

import (
	"sort"
	"strings"
	"time"
)

// ---------- Waiting list ----------
//
// When a day is fully booked, guests can leave their details. As soon as a
// table frees up (a cancellation, an unpaid booking running out), everyone
// waiting for that day gets an email, and the first to book gets the table.

// AddWait puts a guest on the waiting list. If the same guest is already
// waiting for that day, their entry is updated instead of added twice.
func (s *Store) AddWait(e WaitEntry, now time.Time) (WaitEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.d.Waitlist {
		if o.Date == e.Date && !o.Removed && sameWaiter(o, &e) {
			before := *o
			o.Time, o.PartySize, o.Name, o.Phone, o.Email, o.Lang = e.Time, e.PartySize, e.Name, e.Phone, e.Email, e.Lang
			o.NotifiedAt = nil
			if err := s.save(); err != nil {
				*o = before
				return WaitEntry{}, err
			}
			return *o, nil
		}
	}
	e.ID = s.d.NextWaitID
	e.Ref = newRef()
	e.CreatedAt = now
	e.NotifiedAt, e.Removed = nil, false
	stored := e
	s.d.Waitlist = append(s.d.Waitlist, &stored)
	s.d.NextWaitID++
	if err := s.save(); err != nil {
		s.d.Waitlist = s.d.Waitlist[:len(s.d.Waitlist)-1]
		s.d.NextWaitID--
		return WaitEntry{}, err
	}
	return stored, nil
}

func sameWaiter(a, b *WaitEntry) bool {
	if a.Email != "" && strings.EqualFold(a.Email, b.Email) {
		return true
	}
	pa, pb := digitsOnly(a.Phone), digitsOnly(b.Phone)
	return len(pa) >= 6 && pa == pb
}

// Waitlist returns everyone still waiting for a date, first come first.
func (s *Store) Waitlist(date string) []WaitEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []WaitEntry
	for _, e := range s.d.Waitlist {
		if e.Date == date && !e.Removed {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// WaitDates lists the dates from `from` onwards that still have someone
// waiting who hasn't been told about a free table yet.
func (s *Store) WaitDates(from string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, e := range s.d.Waitlist {
		if e.Date >= from && !e.Removed && e.NotifiedAt == nil && !seen[e.Date] {
			seen[e.Date] = true
			out = append(out, e.Date)
		}
	}
	sort.Strings(out)
	return out
}

// MarkWaitNotified remembers that a guest was emailed about a free table.
// changed is false if they had already been told.
func (s *Store) MarkWaitNotified(ref string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.d.Waitlist {
		if e.Ref == ref {
			if e.NotifiedAt != nil || e.Removed {
				return false, nil
			}
			t := now
			e.NotifiedAt = &t
			if err := s.save(); err != nil {
				e.NotifiedAt = nil
				return false, err
			}
			return true, nil
		}
	}
	return false, ErrNotFound
}

// GetWait returns a waiting-list entry by its reference code.
func (s *Store) GetWait(ref string) (WaitEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.d.Waitlist {
		if e.Ref == ref {
			return *e, nil
		}
	}
	return WaitEntry{}, ErrNotFound
}

// RemoveWait takes a guest off the waiting list.
func (s *Store) RemoveWait(ref string) (WaitEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.d.Waitlist {
		if e.Ref == ref {
			if e.Removed {
				return *e, nil
			}
			e.Removed = true
			if err := s.save(); err != nil {
				e.Removed = false
				return WaitEntry{}, err
			}
			return *e, nil
		}
	}
	return WaitEntry{}, ErrNotFound
}

// RemoveWaitFor takes a guest off a day's waiting list once they've booked.
func (s *Store) RemoveWaitFor(date, email, phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	probe := &WaitEntry{Email: email, Phone: phone}
	changed := false
	for _, e := range s.d.Waitlist {
		if e.Date == date && !e.Removed && sameWaiter(e, probe) {
			e.Removed = true
			changed = true
		}
	}
	if changed {
		_ = s.save()
	}
}
