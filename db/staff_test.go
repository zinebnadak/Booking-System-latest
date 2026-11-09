package db

import (
	"emeal-booking/schedule"
	"errors"
	"testing"
	"time"
)

// A fixed "now": Thursday 22 October 2026, 12:00 in Mariehamn.
var now0 = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

func paidBooking(t *testing.T, s *Store, date, clock, email, phone string) Booking {
	t.Helper()
	b, err := s.CreateTableBooking(Booking{Date: date, Time: clock, PartySize: 2, Email: email, Phone: phone, DepositCents: 1000}, now0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.MarkPaid(b.Ref, "pi_1", now0); err != nil {
		t.Fatal(err)
	}
	b, _ = s.Get(b.Ref)
	return b
}

func TestStaffBookingsAndWalkIns(t *testing.T) {
	s := newStore(t)
	phone, err := s.CreateStaffBooking(Booking{Source: SourcePhone, Name: "Anna", Phone: "040 123 4567", Date: "2026-10-30", Time: "18:00", PartySize: 4, DepositCents: 999}, 0, now0)
	if err != nil {
		t.Fatal(err)
	}
	if phone.Status != StatusPaid || phone.DepositCents != 0 || phone.TableNo != 1 || phone.ArrivedAt != nil {
		t.Fatalf("phone booking = %+v", phone)
	}
	walk, err := s.CreateStaffBooking(Booking{Source: SourceWalkIn, Name: "Walk-in", Date: "2026-10-30", Time: "18:10", PartySize: 2}, 0, now0)
	if err != nil {
		t.Fatal(err)
	}
	if walk.ArrivedAt == nil || walk.TableNo != 2 || walk.EffectiveStatus(now0) != StatusArrived {
		t.Fatalf("walk-in = %+v", walk)
	}
	// Asking for a table that's taken fails; a free one works.
	if _, err := s.CreateStaffBooking(Booking{Source: SourcePhone, Name: "B", Date: "2026-10-30", Time: "19:00", PartySize: 2}, 1, now0); !errors.Is(err, ErrFull) {
		t.Fatalf("taken table: err = %v", err)
	}
	if b, err := s.CreateStaffBooking(Booking{Source: SourcePhone, Name: "B", Date: "2026-10-30", Time: "19:00", PartySize: 2}, 5, now0); err != nil || b.TableNo != 5 {
		t.Fatalf("free table: %+v %v", b, err)
	}
	// Same phone number at an overlapping time is a duplicate.
	_, err = s.CreateStaffBooking(Booking{Source: SourcePhone, Name: "Anna", Phone: "0401234567", Date: "2026-10-30", Time: "19:00", PartySize: 2}, 0, now0)
	var dup ErrDuplicate
	if !errors.As(err, &dup) {
		t.Fatalf("duplicate: err = %v", err)
	}
}

func TestPickedUp(t *testing.T) {
	s := newStore(t)
	b, _ := s.CreateTakeaway(Booking{Date: "2026-10-23", Time: "12:00", TotalCents: 4000, DepositCents: 1000}, now0)
	if _, changed, _ := s.MarkPickedUp(b.Ref, now0); changed {
		t.Fatal("unpaid order can't be picked up")
	}
	s.MarkPaid(b.Ref, "pi", now0)
	got, changed, err := s.MarkPickedUp(b.Ref, now0)
	if err != nil || !changed || got.EffectiveStatus(now0) != StatusPickedUp {
		t.Fatalf("picked up: %+v %v %v", got, changed, err)
	}
	if _, changed, _ := s.MarkPickedUp(b.Ref, now0); changed {
		t.Fatal("picked up twice")
	}
}

func TestMoveBooking(t *testing.T) {
	s := newStore(t)
	b := paidBooking(t, s, "2026-10-30", "18:00", "a@example.com", "0401")
	// Fill every other table at 20:00.
	for i := 0; i < 5; i++ {
		paidBooking(t, s, "2026-10-30", "20:00", "", "")
	}
	// 19:00 overlaps the 20:00 bookings on tables 1–5, so it moves to table 6.
	moved, err := s.MoveBooking(b.Ref, "2026-10-30", "19:00", 3, now0)
	if err != nil || moved.Time != "19:00" || moved.PartySize != 3 || moved.TableNo != 6 {
		t.Fatalf("move: %+v %v", moved, err)
	}
	// 20:00 has one table left, the one this booking already holds.
	if moved, err = s.MoveBooking(b.Ref, "2026-10-30", "20:00", 2, now0); err != nil || moved.TableNo != 6 {
		t.Fatalf("move to 20:00: %v", err)
	}
	// Another day, a different free table is fine too.
	if moved, err = s.MoveBooking(b.Ref, "2026-10-31", "18:00", 2, now0); err != nil || moved.Date != "2026-10-31" {
		t.Fatalf("move day: %+v %v", moved, err)
	}
	// A cancelled booking can't be moved.
	s.Cancel(b.Ref, true, "guest", now0)
	if _, err := s.MoveBooking(b.Ref, "2026-10-30", "12:00", 2, now0); !errors.Is(err, ErrNotChangeable) {
		t.Fatalf("cancelled: err = %v", err)
	}
	if got, _ := s.Get(b.Ref); got.CancelledBy != "guest" {
		t.Fatalf("CancelledBy = %q", got.CancelledBy)
	}
}

func TestMoveBookingFull(t *testing.T) {
	s := newStore(t)
	b := paidBooking(t, s, "2026-10-30", "12:00", "a@example.com", "")
	for i := 0; i < 6; i++ {
		paidBooking(t, s, "2026-10-30", "18:00", "", "")
	}
	if _, err := s.MoveBooking(b.Ref, "2026-10-30", "18:30", 2, now0); !errors.Is(err, ErrFull) {
		t.Fatalf("err = %v, want ErrFull", err)
	}
	if got, _ := s.Get(b.Ref); got.Time != "12:00" {
		t.Fatal("a failed move changed the booking")
	}
}

func TestRemindersHistoryAnonymize(t *testing.T) {
	s := newStore(t)
	old := paidBooking(t, s, "2026-10-01", "18:00", "regular@example.com", "")
	s.CheckIn(old.Ref, old.Start())
	paidBooking(t, s, "2026-10-08", "18:00", "regular@example.com", "") // no-show
	today := paidBooking(t, s, "2026-10-23", "18:00", "Regular@Example.com", "")

	h := s.HistoryFor([]Booking{today}, now0)[today.Ref]
	if h.Visits != 1 || h.NoShows != 1 {
		t.Fatalf("history = %+v", h)
	}

	up := s.Upcoming(now0, now0.Add(48*time.Hour))
	if len(up) != 1 || up[0].Ref != today.Ref {
		t.Fatalf("upcoming = %v", up)
	}
	if _, changed, _ := s.MarkReminded(today.Ref, now0); !changed {
		t.Fatal("first reminder not recorded")
	}
	if _, changed, _ := s.MarkReminded(today.Ref, now0); changed {
		t.Fatal("reminder recorded twice")
	}

	n, err := s.Anonymize("2026-10-10")
	if err != nil || n != 2 {
		t.Fatalf("anonymized %d, %v", n, err)
	}
	got, _ := s.Get(old.Ref)
	if got.Email != "" || got.Name != "Guest" || got.DepositCents != 1000 || !got.Anonymized {
		t.Fatalf("anonymized booking = %+v", got)
	}
	if h := s.HistoryFor([]Booking{today}, now0)[today.Ref]; h.Visits != 0 {
		t.Fatal("anonymized bookings still count as history")
	}
}

func TestWaitlist(t *testing.T) {
	s := newStore(t)
	a, err := s.AddWait(WaitEntry{Date: "2026-10-30", Time: "18:00", PartySize: 4, Name: "A", Email: "a@example.com"}, now0)
	if err != nil {
		t.Fatal(err)
	}
	// Same guest again: updated, not added twice.
	a2, _ := s.AddWait(WaitEntry{Date: "2026-10-30", Time: "19:00", PartySize: 2, Name: "A", Email: "A@example.com"}, now0)
	if a2.ID != a.ID || len(s.Waitlist("2026-10-30")) != 1 || a2.Time != "19:00" {
		t.Fatalf("duplicate wait entry: %+v", s.Waitlist("2026-10-30"))
	}
	s.AddWait(WaitEntry{Date: "2026-10-30", PartySize: 2, Name: "B", Phone: "040 555 1234"}, now0.Add(time.Minute))
	if d := s.WaitDates("2026-10-22"); len(d) != 1 || d[0] != "2026-10-30" {
		t.Fatalf("wait dates = %v", d)
	}
	if ok, _ := s.MarkWaitNotified(a.Ref, now0); !ok {
		t.Fatal("not marked notified")
	}
	if ok, _ := s.MarkWaitNotified(a.Ref, now0); ok {
		t.Fatal("notified twice")
	}
	s.RemoveWaitFor("2026-10-30", "", "0405551234")
	if w := s.Waitlist("2026-10-30"); len(w) != 1 || w[0].Name != "A" {
		t.Fatalf("after booking, waitlist = %+v", w)
	}
	s.RemoveWait(a.Ref)
	if w := s.Waitlist("2026-10-30"); len(w) != 0 {
		t.Fatalf("after remove, waitlist = %+v", w)
	}
}

// Card or cash at pickup: cash goes in the drawer count.
func TestPickupPaidInCash(t *testing.T) {
	s := newStore(t)
	cash, _ := s.CreateTakeaway(Booking{Date: "2026-10-23", Time: "12:00", TotalCents: 4000, DepositCents: 1000}, now0)
	card, _ := s.CreateTakeaway(Booking{Date: "2026-10-23", Time: "12:30", TotalCents: 2000, DepositCents: 500, Email: "b@example.com", Phone: "0402"}, now0)
	s.MarkPaid(cash.Ref, "pi1", now0)
	s.MarkPaid(card.Ref, "pi2", now0)
	if b, _, err := s.MarkPickedUpWith(cash.Ref, PaidCash, now0); err != nil || b.PickupPaidBy != PaidCash {
		t.Fatalf("cash pickup: %+v %v", b, err)
	}
	s.MarkPickedUpWith(card.Ref, PaidCard, now0)
	d := schedule.FormatDate(now0)
	day := s.TillByDate(d, d)[d]
	if day == nil || day.Cash != 3000 || day.PickupCash != 3000 || day.PickupCard != 1500 {
		t.Fatalf("till %+v", day)
	}
}
