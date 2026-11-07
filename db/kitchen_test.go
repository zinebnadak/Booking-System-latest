package db

import (
	"errors"
	"testing"
	"time"
)

func TestTableCodeUnlocksTheBookedTable(t *testing.T) {
	s := newStore(t)
	b, err := s.CreateTableBooking(Booking{Date: "2026-10-31", Time: "19:00", PartySize: 2, Email: "a@b.fi", Name: "Anna"}, at("10:00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.TableCode) != 4 {
		t.Fatalf("table code %q", b.TableCode)
	}
	if _, _, err := s.MarkPaid(b.Ref, "pi", at("10:01")); err != nil {
		t.Fatal(err)
	}
	// Too early: an hour before, the table isn't theirs yet.
	if _, ok := s.BookedNow(b.TableNo, at("18:00")); ok {
		t.Fatal("booked an hour early")
	}
	if _, ok := s.BookedNow(b.TableNo, at("18:35")); !ok {
		t.Fatal("not booked 25 minutes before")
	}
	wrong := "0000"
	if b.TableCode == wrong {
		wrong = "1111"
	}
	if _, err := s.UnlockTable(b.TableNo, wrong, at("18:55")); !errors.Is(err, ErrBadCode) {
		t.Fatalf("wrong code: %v", err)
	}
	got, err := s.UnlockTable(b.TableNo, b.TableCode, at("18:56"))
	if err != nil || got.ArrivedAt == nil {
		t.Fatalf("right code: %+v %v", got, err)
	}
	if _, ok := s.BookedNow(b.TableNo, at("18:57")); ok {
		t.Fatal("still waiting for them after they arrived")
	}

	// Someone who never comes: 20 minutes late, the table is free again.
	c, _ := s.CreateTableBooking(Booking{Date: "2026-10-31", Time: "20:00", PartySize: 2, Email: "c@d.fi", Name: "Carl"}, at("10:00"))
	_, _, _ = s.MarkPaid(c.Ref, "pi2", at("10:01"))
	if _, ok := s.BookedNow(c.TableNo, at("20:19")); !ok {
		t.Fatal("not booked 19 minutes late")
	}
	if _, ok := s.BookedNow(c.TableNo, at("20:20")); ok {
		t.Fatal("still booked after the no-show limit")
	}
	if _, err := s.UnlockTable(c.TableNo, c.TableCode, at("20:21")); !errors.Is(err, ErrNoBooking) {
		t.Fatalf("code after the no-show limit: %v", err)
	}
}

func TestTakeawayOnTheKitchenScreen(t *testing.T) {
	s := newStore(t)
	b, err := s.CreateTakeaway(Booking{Date: "2026-10-31", Time: "18:00", Name: "Erik", Email: "e@x.fi", Notes: "extra napkins",
		Items: []OrderItem{{Name: "E-Meal-burgare", Quantity: 2, UnitPriceCents: 1990}}, TotalCents: 3980}, at("10:00"))
	if err != nil {
		t.Fatal(err)
	}
	if added, err := s.AddTakeawayTicket(b, at("10:01")); err != nil || !added {
		t.Fatalf("added=%v err=%v", added, err)
	}
	if added, _ := s.AddTakeawayTicket(b, at("10:02")); added {
		t.Fatal("added twice")
	}
	// New: on the screen until the chef taps Seen.
	active, later := s.KitchenNow(at("10:05"))
	if len(active) != 1 || len(later) != 0 || active[0].OrderNo != b.ID || active[0].Customer != "Erik" || active[0].Note != "extra napkins" {
		t.Fatalf("new: %+v / %+v", active, later)
	}
	if err := s.TicketSeen(active[0].ID, at("10:06")); err != nil {
		t.Fatal(err)
	}
	// Seen: off the screen until an hour before pickup.
	active, later = s.KitchenNow(at("16:59"))
	if len(active) != 0 || len(later) != 1 {
		t.Fatalf("seen: %d / %d", len(active), len(later))
	}
	active, _ = s.KitchenNow(at("17:00"))
	if len(active) != 1 {
		t.Fatal("not back an hour before pickup")
	}
	// Tidying overnight doesn't touch an order for a later day.
	next, _ := s.CreateTakeaway(Booking{Date: "2026-11-02", Time: "12:00", Name: "Ida", Email: "i@x.fi",
		Items: []OrderItem{{Name: "Sorbet", Quantity: 1, UnitPriceCents: 750}}, TotalCents: 750}, at("10:00"))
	_, _ = s.AddTakeawayTicket(next, at("10:10"))
	if err := s.TidyKitchen(at("10:00").Add(24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, later := s.KitchenNow(at("10:00").Add(24 * time.Hour)); len(later) != 0 {
		t.Fatalf("later %+v", later)
	}
	if active, _ := s.KitchenNow(at("10:00").Add(24 * time.Hour)); len(active) != 1 || active[0].Customer != "Ida" {
		t.Fatalf("Monday's order was tidied away: %+v", active)
	}
	// Cancelled: struck through. Picked up: off the screen.
	_ = s.TakeawayCancelled(next.Ref)
	if active, _ := s.KitchenNow(at("10:00").Add(24 * time.Hour)); !active[0].Items[0].Cancelled {
		t.Fatal("not struck through")
	}
	_ = s.TakeawayCollected(next.Ref, at("10:00").Add(25*time.Hour))
	if active, _ := s.KitchenNow(at("10:00").Add(25 * time.Hour)); len(active) != 0 {
		t.Fatal("still on the screen after pickup")
	}
}

func TestSoldOutForTheRestOfTheDay(t *testing.T) {
	s := newStore(t)
	day := at("18:00")
	s.Clock = func() time.Time { return day }
	var lax MenuItem
	for _, m := range s.Menu(true) {
		if m.Name == "Laxfilé" {
			lax = m
		}
	}
	if _, err := s.SetSoldOut(lax.ID, true, day); err != nil {
		t.Fatal(err)
	}
	has := func() bool {
		for _, m := range s.Menu(true) {
			if m.ID == lax.ID {
				return true
			}
		}
		return false
	}
	if has() {
		t.Fatal("sold out but still on the menu")
	}
	// Editing the dish on the Menu page doesn't undo "sold out".
	got, _ := s.MenuItem(lax.ID)
	got.PriceCents = 2790
	_, _ = s.SaveMenuItem(got)
	if has() {
		t.Fatal("editing the dish put it back")
	}
	// The next morning it's back by itself.
	day = day.Add(16 * time.Hour)
	if !has() {
		t.Fatal("not back the next day")
	}
}
