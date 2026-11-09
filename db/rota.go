package db

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"emeal-booking/schedule"
)

// ---------- The staff schedule (who works when) ----------
//
// The manager plans the week: a shift is a person, a day and a time
// ("Lisa, Friday 16:00–23:00"). Staff see their own shifts on their phone.
// What they really worked is the clock card (shifts.go).

// RotaShift is one planned shift.
type RotaShift struct {
	ID      int    `json:"id"`
	StaffID int    `json:"staff_id"`
	Name    string `json:"name"`
	Date    string `json:"date"` // "2026-11-13"
	From    string `json:"from"` // "16:00"
	To      string `json:"to"`   // "23:00" (earlier than From: ends after midnight)
	Note    string `json:"note,omitempty"`
}

// Minutes is how long the shift is.
func (r RotaShift) Minutes() int {
	f, err1 := schedule.ParseClock(r.From)
	t, err2 := schedule.ParseClock(r.To)
	if err1 != nil || err2 != nil {
		return 0
	}
	if t <= f {
		t += 24 * 60
	}
	return t - f
}

var ErrBadRota = errors.New("pick a person, a day and the times")

// AddRota plans a shift. The person must be on the staff list.
func (s *Store) AddRota(r RotaShift) (RotaShift, error) {
	if _, err := schedule.ParseDate(r.Date); err != nil {
		return RotaShift{}, ErrBadRota
	}
	if _, err := schedule.ParseClock(r.From); err != nil {
		return RotaShift{}, ErrBadRota
	}
	if _, err := schedule.ParseClock(r.To); err != nil || r.From == r.To {
		return RotaShift{}, ErrBadRota
	}
	r.Note = strings.TrimSpace(r.Note)
	if len(r.Note) > 120 {
		r.Note = r.Note[:120]
	}
	err := s.changeOrders(func() (bool, error) {
		var who *StaffMember
		for _, m := range s.d.Staff {
			if m.ID == r.StaffID && m.Active {
				who = m
			}
		}
		if who == nil {
			return false, ErrBadRota
		}
		r.Name = who.Name
		for _, o := range s.d.Rota {
			if o.StaffID == r.StaffID && o.Date == r.Date && o.From == r.From && o.To == r.To {
				r = *o // already planned (tapped twice)
				return false, nil
			}
		}
		s.d.NextRotaID++
		r.ID = s.d.NextRotaID
		c := r
		s.d.Rota = append(append([]*RotaShift(nil), s.d.Rota...), &c) // a new slice: the old one stays for a failed save
		return true, nil
	})
	return r, err
}

// RemoveRota takes a planned shift away.
func (s *Store) RemoveRota(id int) error {
	return s.changeOrders(func() (bool, error) {
		kept := make([]*RotaShift, 0, len(s.d.Rota))
		for _, r := range s.d.Rota {
			if r.ID != id {
				kept = append(kept, r)
			}
		}
		changed := len(kept) != len(s.d.Rota)
		s.d.Rota = kept
		return changed, nil
	})
}

// RotaBetween lists the planned shifts from one date to another (both
// included), by day and start time.
func (s *Store) RotaBetween(from, to string) []RotaShift {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []RotaShift
	for _, r := range s.d.Rota {
		if r.Date >= from && r.Date <= to {
			out = append(out, *r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// CopyRotaWeek plans the week starting toMonday like the week starting
// fromMonday (same people, weekdays and times). Shifts already planned, and
// people no longer on the staff, are skipped. It returns how many it added.
func (s *Store) CopyRotaWeek(fromMonday, toMonday time.Time) (int, error) {
	added := 0
	err := s.changeOrders(func() (bool, error) {
		active := map[int]string{}
		for _, m := range s.d.Staff {
			if m.Active {
				active[m.ID] = m.Name
			}
		}
		have := map[string]bool{}
		key := func(r RotaShift) string {
			return r.Date + "|" + r.From + "|" + r.To + "|" + strconv.Itoa(r.StaffID)
		}
		for _, r := range s.d.Rota {
			have[key(*r)] = true
		}
		next := append([]*RotaShift(nil), s.d.Rota...)
		days := int(toMonday.Sub(fromMonday).Hours()/24 + 0.5)
		from, to := schedule.FormatDate(fromMonday), schedule.FormatDate(fromMonday.AddDate(0, 0, 6))
		for _, r := range s.d.Rota {
			if r.Date < from || r.Date > to {
				continue
			}
			name, ok := active[r.StaffID]
			if !ok {
				continue
			}
			d, err := schedule.ParseDate(r.Date)
			if err != nil {
				continue
			}
			c := *r
			c.Date, c.Name = schedule.FormatDate(d.AddDate(0, 0, days)), name
			if have[key(c)] {
				continue
			}
			s.d.NextRotaID++
			c.ID = s.d.NextRotaID
			have[key(c)] = true
			next = append(next, &c)
			added++
		}
		s.d.Rota = next
		return added > 0, nil
	})
	return added, err
}
