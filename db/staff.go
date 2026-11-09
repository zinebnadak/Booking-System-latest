package db

import (
	"sort"
	"time"

	"emeal-booking/schedule"
)

// ---------- Bookings made by staff ----------

// CreateStaffBooking saves a booking the staff took by phone, or guests who
// walked in. There is no deposit, so it's confirmed straight away. A walk-in
// is checked in at once. table 0 means "the first free table".
func (s *Store) CreateStaffBooking(b Booking, table int, now time.Time) (Booking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b.Kind = KindTable
	if b.Email != "" || b.Phone != "" {
		for _, o := range s.d.Bookings {
			if o.Status == StatusPaid && o.Date == b.Date && o.HoldsTable(now) &&
				schedule.OverlapsFor(o.Time, o.Minutes, b.Time, b.Minutes) && sameGuest(o, &b) {
				return Booking{}, ErrDuplicate{Time: o.Time}
			}
		}
	}
	// Staff may seat a party at any free table they pick (with an extra
	// chair, say); picked for them, it's the smallest table that fits.
	need := tableNeed{size: b.PartySize}
	if table != 0 {
		need = tableNeed{}
	}
	free := s.freeTablesFor(b.Date, b.Time, b.Minutes, need, now, nil)
	switch {
	case len(free) == 0:
		return Booking{}, ErrFull
	case table == 0:
		table = free[0]
	default:
		ok := false
		for _, t := range free {
			ok = ok || t == table
		}
		if !ok {
			return Booking{}, ErrFull
		}
	}
	t := now
	b.TableNo = table
	b.DepositCents = 0
	b.Status = StatusPaid
	b.PaidAt = &t
	if b.Source == SourceWalkIn {
		b.ArrivedAt = &t
	}
	return s.insert(b, now)
}

// TableWindow is a table that is free right now, and for how long.
type TableWindow struct {
	No      int
	Room    string
	Seats   int
	Minutes int    // free for this long (at most 2 hours)
	Until   string // "19:30"
}

// FreeNow lists the tables free from clock onwards for at least minMinutes,
// and how long each stays free (up to 2 hours). Used to seat walk-ins, also
// between two bookings.
func (s *Store) FreeNow(date, clock string, minMinutes int, now time.Time) []TableWindow {
	s.mu.Lock()
	defer s.mu.Unlock()
	start, err := schedule.ParseClock(clock)
	if err != nil {
		return nil
	}
	full := int(schedule.BookingDuration / time.Minute)
	var out []TableWindow
	for _, t := range schedule.TableNos() {
		free := full
		for _, b := range s.d.Bookings {
			if b.TableNo != t || b.Date != date || !b.HoldsTable(now) {
				continue
			}
			bs, err := schedule.ParseClock(b.Time)
			if err != nil {
				continue
			}
			be := bs + int(b.Duration()/time.Minute)
			switch {
			case bs <= start && start < be: // someone is (or should be) there now
				free = 0
			case bs > start && bs-start < free: // the next booking on this table
				free = bs - start
			}
		}
		if free >= minMinutes {
			info, _ := schedule.TableInfo(t)
			out = append(out, TableWindow{No: t, Room: info.Room, Seats: info.Seats, Minutes: free, Until: schedule.Clock(start + free)})
		}
	}
	return out
}

// MarkPickedUp records that a takeaway order was collected (and the rest paid).
func (s *Store) MarkPickedUp(ref string, now time.Time) (Booking, bool, error) {
	return s.MarkPickedUpWith(ref, "", now)
}

// MarkPickedUpWith is MarkPickedUp, saying how the rest was paid (PaidCard
// or PaidCash; "" if unknown), so cash at pickup is in the drawer count.
func (s *Store) MarkPickedUpWith(ref, method string, now time.Time) (Booking, bool, error) {
	if method != PaidCard && method != PaidCash {
		method = ""
	}
	return s.update(ref, func(b *Booking) bool {
		if b.Kind != KindTakeaway || b.Status != StatusPaid || b.PickedUpAt != nil {
			return false
		}
		t := now
		b.PickedUpAt = &t
		if b.RemainingCents() > 0 {
			b.PickupPaidBy = method
		}
		return true
	})
}

// ---------- Guests changing their booking ----------

// MoveBooking changes the day, time and number of guests of a confirmed
// table booking. It keeps the same table if that one is free at the new
// time, otherwise it takes the first free table. Everything happens under
// one lock, so nobody can grab the table in between.
func (s *Store) MoveBooking(ref, date, clock string, size int, now time.Time) (Booking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.find(ref)
	if b == nil || b.Kind != KindTable {
		return Booking{}, ErrNotFound
	}
	if b.Status != StatusPaid || b.ArrivedAt != nil || b.isNoShow(now) {
		return Booking{}, ErrNotChangeable
	}
	for _, o := range s.d.Bookings {
		if o != b && o.Status == StatusPaid && o.Date == date && o.HoldsTable(now) &&
			schedule.OverlapsFor(o.Time, o.Minutes, clock, b.Minutes) && sameGuest(o, b) {
			return Booking{}, ErrDuplicate{Time: o.Time}
		}
	}
	free := s.freeTablesFor(date, clock, b.Minutes, tableNeed{size: size, online: true}, now, b)
	if len(free) == 0 {
		return Booking{}, ErrFull
	}
	table := free[0]
	for _, t := range free {
		if t == b.TableNo {
			table = t
		}
	}
	before := b.clone()
	b.Date, b.Time, b.PartySize, b.TableNo = date, clock, size, table
	b.RemindedAt = nil // new time, so a new reminder
	if err := s.save(); err != nil {
		*b = before
		return Booking{}, err
	}
	return b.clone(), nil
}

// ---------- Reminders ----------

// Upcoming returns confirmed bookings (tables and takeaway) that start
// between from and to.
func (s *Store) Upcoming(from, to time.Time) []Booking {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Booking
	for _, b := range s.d.Bookings {
		if b.Status != StatusPaid || b.Anonymized {
			continue
		}
		if st := b.Start(); !st.Before(from) && st.Before(to) {
			out = append(out, b.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start().Before(out[j].Start()) })
	return out
}

// MarkReminded remembers that the reminder email was sent. changed is false
// if it had already been sent, so a reminder never goes out twice.
func (s *Store) MarkReminded(ref string, now time.Time) (Booking, bool, error) {
	return s.update(ref, func(b *Booking) bool {
		if b.RemindedAt != nil {
			return false
		}
		t := now
		b.RemindedAt = &t
		return true
	})
}

// ---------- Guest history ----------

// GuestHistory is what earlier table bookings say about a guest.
type GuestHistory struct {
	Visits  int // earlier bookings they came to
	NoShows int // earlier bookings they didn't come to
}

// HistoryFor looks up, for each booking, the same guest's earlier table
// bookings (same email or phone). The result is keyed by booking ref.
func (s *Store) HistoryFor(bs []Booking, now time.Time) map[string]GuestHistory {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]GuestHistory{}
	for i := range bs {
		b := &bs[i]
		if b.Email == "" && b.Phone == "" {
			continue
		}
		start := b.Start()
		var h GuestHistory
		for _, o := range s.d.Bookings {
			if o.Ref == b.Ref || o.Kind != KindTable || o.Anonymized || !o.Start().Before(start) || !sameGuest(o, b) {
				continue
			}
			switch o.EffectiveStatus(now) {
			case StatusArrived:
				h.Visits++
			case StatusNoShow:
				h.NoShows++
			}
		}
		if h.Visits > 0 || h.NoShows > 0 {
			out[b.Ref] = h
		}
	}
	return out
}

// ---------- Privacy ----------

// Anonymize removes names, emails, phone numbers and notes from bookings
// for days before cutoff ("2025-10-23"), and deletes old waiting-list
// entries. Amounts, dates and dishes stay, so the reports still add up.
// It returns how many bookings were cleaned.
func (s *Store) Anonymize(cutoff string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.d.Bookings {
		if b.Anonymized || b.Date >= cutoff {
			continue
		}
		b.Name, b.Email, b.Phone, b.Notes = "Guest", "", "", ""
		b.CheckoutURL = ""
		b.Anonymized = true
		n++
	}
	kept := s.d.Waitlist[:0]
	removed := 0
	for _, e := range s.d.Waitlist {
		if e.Date < cutoff {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	s.d.Waitlist = kept
	// Bills: the first names guests typed on their phones, the keys that
	// opened their pages, and the payment pages of paying at the table.
	cleared := 0
	for _, b := range s.d.Bills {
		if b.Open() || schedule.FormatDate(b.OpenedAt) >= cutoff {
			continue
		}
		for i := range b.Split {
			if b.Split[i].Name != "" || b.Split[i].Key != "" {
				b.Split[i].Name, b.Split[i].Key = "", ""
				cleared++
			}
		}
	}
	for _, t := range s.d.Tickets {
		if schedule.FormatDate(t.At) >= cutoff {
			continue
		}
		for i := range t.Items {
			if t.Items[i].Who != "" {
				t.Items[i].Who = ""
				cleared++
			}
		}
		if t.Customer != "" || t.Note != "" {
			t.Customer, t.Note = "", ""
			cleared++
		}
	}
	for _, r := range s.d.Reviews { // an email given for a reply
		if r.Email != "" && schedule.FormatDate(r.At) < cutoff {
			r.Email = ""
			cleared++
		}
	}
	keptA := s.d.PayAttempts[:0]
	for _, a := range s.d.PayAttempts {
		if !a.Pending() && schedule.FormatDate(a.Created) < cutoff {
			cleared++
			continue
		}
		keptA = append(keptA, a)
	}
	s.d.PayAttempts = keptA
	if n == 0 && removed == 0 && cleared == 0 {
		return 0, nil
	}
	return n, s.save()
}
