package db

import (
	"testing"
	"time"

	"emeal-booking/schedule"
)

func TestHoldMainsAndDessert(t *testing.T) {
	s := newStore(t)
	at := func(c string) time.Time {
		d, _ := schedule.At(time.Date(2026, 10, 30, 0, 0, 0, 0, schedule.Location), c)
		return d
	}
	b, ticket, err := s.AddToBill(5, 0, []NewLine{
		{Name: "Råbiff", PriceCents: 1790, Qty: 2, Course: CourseStarter, Hold: true},
		{Name: "Oxfilé", PriceCents: 3990, Qty: 2, Course: CourseMain, Hold: true},
		{Name: "Chokladmousse", PriceCents: 890, Qty: 1, Course: CourseDessert, Hold: true},
		{Name: "Aperol spritz", PriceCents: 990, Qty: 2, Drink: true, Hold: true},
	}, "", 0, at("18:00"))
	if err != nil {
		t.Fatal(err)
	}
	if ticket == nil || ticket.Held || len(ticket.Items) != 1 || ticket.Items[0].Name != "Råbiff" {
		t.Fatalf("kitchen gets %+v, want only the starter", ticket)
	}
	active, _ := s.KitchenNow(at("18:01"))
	if len(active) != 1 {
		t.Fatalf("%d tickets on the screen, want 1", len(active))
	}
	if held := s.HeldTickets(); len(held) != 2 || held[0].Course != CourseMain || held[1].Course != CourseDessert {
		t.Fatalf("held: %+v", held)
	}
	if c := s.HeldCourses(b.ID); len(c) != 2 {
		t.Fatalf("held courses %v", c)
	}
	// "Varma fram": the mains go to the chef, timed from now.
	if n, err := s.FireCourse(b.ID, CourseMain, at("18:25")); err != nil || n != 1 {
		t.Fatalf("fire mains: %d %v", n, err)
	}
	active, _ = s.KitchenNow(at("18:26"))
	if len(active) != 2 || active[1].Since() != at("18:25") || active[1].Items[0].Name != "Oxfilé" {
		t.Fatalf("after Varma fram: %+v", active)
	}
	if _, err := s.FireCourse(b.ID, CourseMain, at("18:26")); err != ErrNothingHeld {
		t.Fatalf("second Varma fram: %v", err)
	}
	// The chef can start the dessert himself.
	if err := s.FireTicket(s.HeldTickets()[0].ID, at("19:00")); err != nil || len(s.HeldTickets()) != 0 {
		t.Fatalf("fire ticket: %v", err)
	}
	// Without starters, mains go straight away even with Hold.
	_, tk, _ := s.AddToBill(6, 0, []NewLine{{Name: "Burgare", PriceCents: 1990, Qty: 1, Course: CourseMain, Hold: true}}, "", 0, at("18:00"))
	if tk == nil || tk.Held {
		t.Fatal("mains alone should go straight to the chef")
	}
}

func TestKitchenMessage(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 10, 30, 18, 0, 0, 0, schedule.Location)
	if _, err := s.KitchenMessage(5, "  ", now); err == nil {
		t.Fatal("empty message accepted")
	}
	m, err := s.KitchenMessage(5, "Gäst 2 är allergisk mot nötter", now)
	if err != nil || !m.Live() {
		t.Fatalf("message: %+v %v", m, err)
	}
	active, _ := s.KitchenNow(now)
	if len(active) != 1 || active[0].Message == "" {
		t.Fatalf("screen: %+v", active)
	}
	if err := s.TicketDone(m.ID, false, now); err != nil {
		t.Fatal(err)
	}
}

func TestDiscounts(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 10, 30, 18, 0, 0, 0, schedule.Location)
	b, _, _ := s.AddToBill(5, 0, []NewLine{
		{Name: "Råbiff", PriceCents: 1790, Qty: 1, VAT: schedule.VATFood},
		{Name: "Aperol spritz", PriceCents: 990, Qty: 2, Drink: true, VAT: schedule.VATAlcohol},
	}, "", 0, now)
	if err := s.SetDiscount(b.ID, b.Lines[0].ID, 20); err != nil {
		t.Fatal(err)
	}
	b, _ = s.BillByID(b.ID)
	// 17,90 - 20 % = 14,32
	if b.Lines[0].Total() != 1432 || b.Discounts() != 358 || b.Total() != 1432+1980 {
		t.Fatalf("after 20 %%: line %d, discounts %d, total %d", b.Lines[0].Total(), b.Discounts(), b.Total())
	}
	if got := b.VATByRate()[schedule.VATFood]; got != 1432 {
		t.Fatalf("VAT base after discount %d", got)
	}
	if err := s.SetDiscount(b.ID, b.Lines[0].ID, 120); err != ErrBadDiscount {
		t.Fatalf("120 %%: %v", err)
	}
	if n, err := s.DiscountBill(b.ID, 100); err != nil || n != 2 {
		t.Fatalf("on the house: %d %v", n, err)
	}
	b, _ = s.BillByID(b.ID)
	if b.Total() != 0 || b.ToPay() != 0 {
		t.Fatalf("on the house total %d", b.Total())
	}
}
