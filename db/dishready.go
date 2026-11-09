package db

import (
	"errors"
	"time"
)

// One dish at a time: the chef ticks each dish on a ticket as it's ready
// (the soup now, the burgers later), and the waiter sees it on the table's
// page. When the last dish is ticked the ticket is done, as if the chef had
// tapped Done.

// ErrTicketChanged: the dish tapped isn't on the ticket any more (or not
// where the screen showed it).
var ErrTicketChanged = errors.New("that order changed in the meantime")

// DishReady ticks dish number i on ticket id as ready (undo: unticks it).
// name is the dish the screen showed, to be sure it's still the same one.
// done reports that it was the last dish, so the whole ticket is done now.
func (s *Store) DishReady(id, i int, name string, undo bool, now time.Time) (done bool, err error) {
	err = s.changeOrders(func() (bool, error) {
		done = false
		t := s.ticketByID(id)
		if t == nil || t.Bar || t.Message != "" || t.Held {
			return false, ErrNoTicket
		}
		if t.DoneAt != nil || i < 0 || i >= len(t.Items) || t.Items[i].Name != name || t.Items[i].Cancelled {
			return false, ErrTicketChanged
		}
		it := &t.Items[i]
		switch {
		case undo && it.ReadyAt != nil:
			it.ReadyAt = nil
		case !undo && it.ReadyAt == nil:
			d := now
			it.ReadyAt = &d
		default:
			return false, nil // tapped twice
		}
		if !undo && t.allReady() {
			d := now
			t.DoneAt = &d
			done = true
		}
		return true, nil
	})
	return done, err
}

// allReady: every dish still on the ticket is ticked.
func (t *Ticket) allReady() bool {
	some := false
	for _, it := range t.Items {
		if it.Cancelled {
			continue
		}
		if it.ReadyAt == nil {
			return false
		}
		some = true
	}
	return some
}

// PartReady: the chef has ticked some of the ticket's dishes but not all
// (so it isn't done yet).
func (t Ticket) PartReady() bool {
	if t.DoneAt != nil {
		return false
	}
	for _, it := range t.Items {
		if !it.Cancelled && it.ReadyAt != nil {
			return true
		}
	}
	return false
}

// ReadyDish finds when the chef ticked the bill line lineID on this ticket
// (nil: not ticked).
func (t Ticket) ReadyDish(lineID int) *time.Time {
	for _, it := range t.Items {
		if it.LineID == lineID && lineID != 0 && !it.Cancelled {
			return it.ReadyAt
		}
	}
	return nil
}
