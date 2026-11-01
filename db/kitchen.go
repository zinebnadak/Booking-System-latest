package db

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"ozmo-booking/schedule"
)

// ---------- table codes: guests who booked unlock their table's QR code ----------

var (
	ErrNoBooking = errors.New("no booking at this table right now")
	ErrBadCode   = errors.New("wrong table code")
)

func newTableCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%04d", n.Int64())
}

// ensureTableCodes gives table bookings from before table codes existed
// their code. Callers hold s.mu.
func (s *Store) ensureTableCodes() bool {
	added := false
	for _, b := range s.d.Bookings {
		if b.Kind == KindTable && b.TableCode == "" && !b.Anonymized {
			b.TableCode = newTableCode()
			added = true
		}
	}
	return added
}

// bookingAtTableNow is the booking that has this table right now and hasn't
// arrived yet: from TableCodeEarly before it starts until it turns into a
// no-show. Walk-ins are seated by staff, so they don't count. Callers hold s.mu.
func (s *Store) bookingAtTableNow(table int, now time.Time) *Booking {
	today := schedule.FormatDate(schedule.Today(now))
	for _, b := range s.d.Bookings {
		if b.Kind != KindTable || b.TableNo != table || b.Date != today || b.ArrivedAt != nil ||
			b.Source == SourceWalkIn || b.EffectiveStatus(now) != StatusPaid {
			continue
		}
		start := b.Start()
		if now.Before(start.Add(-schedule.TableCodeEarly)) || !now.Before(start.Add(schedule.NoShowAfter)) {
			continue
		}
		return b
	}
	return nil
}

// BookedNow returns the booking waiting for this table right now, if any.
func (s *Store) BookedNow(table int, now time.Time) (Booking, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.bookingAtTableNow(table, now); b != nil {
		return b.clone(), true
	}
	return Booking{}, false
}

// UnlockTable: the guests who booked this table typed their table code on
// a phone. They're marked as arrived, like staff pressing "Arrived".
func (s *Store) UnlockTable(table int, code string, now time.Time) (Booking, error) {
	s.mu.Lock()
	b := s.bookingAtTableNow(table, now)
	if b == nil {
		s.mu.Unlock()
		return Booking{}, ErrNoBooking
	}
	ref, want := b.Ref, b.TableCode
	s.mu.Unlock()
	if len(code) != len(want) || subtle.ConstantTimeCompare([]byte(code), []byte(want)) != 1 {
		return Booking{}, ErrBadCode
	}
	out, _, err := s.CheckIn(ref, now)
	return out, err
}

// ---------- takeaway orders on the kitchen screen ----------

// IsTakeaway reports whether the ticket is a takeaway order paid online.
func (t Ticket) IsTakeaway() bool { return t.Takeaway != "" }

// DueAt is when a takeaway ticket comes back to start cooking.
func (t Ticket) DueAt() time.Time {
	if t.Pickup == nil {
		return t.At
	}
	return t.Pickup.Add(-schedule.TakeawayKitchenHeadsUp)
}

// AddTakeawayTicket puts a takeaway order whose deposit was just paid on the
// kitchen screen (once per order).
func (s *Store) AddTakeawayTicket(b Booking, now time.Time) (bool, error) {
	if b.Kind != KindTakeaway || len(b.Items) == 0 {
		return false, nil
	}
	added := false
	err := s.changeOrders(func() (bool, error) {
		for _, t := range s.d.Tickets {
			if t.Takeaway == b.Ref {
				return false, nil
			}
		}
		pickup := b.Start()
		s.d.NextTicketID++
		t := &Ticket{ID: s.d.NextTicketID, At: now, Takeaway: b.Ref, OrderNo: b.ID, Customer: b.Name,
			Pickup: &pickup, Note: b.Notes}
		for _, it := range b.Items {
			t.Items = append(t.Items, TicketItem{Name: it.Name, Qty: it.Quantity})
		}
		s.d.Tickets = append(s.d.Tickets, t)
		added = true
		return true, nil
	})
	return added, err
}

// KitchenNow splits the open tickets into what's on the screen now and the
// takeaway orders the chef has seen that come back later (soonest first).
// A takeaway order is on the screen until the chef taps Seen, and again
// from TakeawayKitchenHeadsUp before pickup.
func (s *Store) KitchenNow(now time.Time) (active, later []Ticket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.d.Tickets {
		if t.DoneAt != nil || t.Bar || t.Held {
			continue // held courses are in HeldTickets
		}
		if t.IsTakeaway() && t.SeenAt != nil && now.Before(t.DueAt()) {
			later = append(later, t.clone())
			continue
		}
		active = append(active, t.clone())
	}
	at := func(t Ticket) time.Time {
		if t.IsTakeaway() && t.SeenAt != nil {
			return t.DueAt()
		}
		return t.Since()
	}
	sort.SliceStable(active, func(i, j int) bool { return at(active[i]).Before(at(active[j])) })
	sort.Slice(later, func(i, j int) bool { return later[i].Pickup.Before(*later[j].Pickup) })
	return active, later
}

// TicketSeen: the chef tapped Seen on a new takeaway order.
func (s *Store) TicketSeen(id int, now time.Time) error {
	return s.changeOrders(func() (bool, error) {
		t := s.ticketByID(id)
		if t == nil {
			return false, ErrNoTicket
		}
		if t.SeenAt != nil {
			return false, nil
		}
		d := now
		t.SeenAt = &d
		return true, nil
	})
}

func (s *Store) takeawayTicket(ref string) *Ticket {
	for _, t := range s.d.Tickets {
		if t.Takeaway == ref {
			return t
		}
	}
	return nil
}

// TakeawayCancelled strikes a cancelled takeaway order through on the
// kitchen screen (the chef taps Clear).
func (s *Store) TakeawayCancelled(ref string) error {
	return s.changeOrders(func() (bool, error) {
		t := s.takeawayTicket(ref)
		if t == nil || t.DoneAt != nil {
			return false, nil
		}
		for i := range t.Items {
			t.Items[i].Cancelled = true
		}
		return true, nil
	})
}

// TakeawayCollected: the customer picked the order up, so it's off the
// kitchen screen (if the chef hadn't tapped Done).
func (s *Store) TakeawayCollected(ref string, now time.Time) error {
	return s.changeOrders(func() (bool, error) {
		t := s.takeawayTicket(ref)
		if t == nil || t.DoneAt != nil {
			return false, nil
		}
		d := now
		t.DoneAt = &d
		return true, nil
	})
}
