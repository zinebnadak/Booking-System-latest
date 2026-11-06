package db

import (
	"errors"
	"testing"
)

func TestDealWithDishesAndPrice(t *testing.T) {
	s := newStore(t)
	now := at("18:00")
	// Rice first, then fish, then meat, for 2 guests: 2 × 20 € on the bill.
	b, err := s.StartDealWith(4, []string{"Ris", " Fisk ", "Kött"}, 2, "", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if b.Total() != 2*DealPriceCents || len(b.Lines) != 1 || b.Lines[0].Qty != 2 || b.Lines[0].Name != "3 rätter" {
		t.Fatalf("bill %d %+v", b.Total(), b.Lines)
	}
	run := s.TablesNow()[3].Run
	if run == nil || run.Dish(1) != "Ris" || run.Dish(2) != "Fisk" || run.Dish(3) != "Kött" || run.Guests != 2 {
		t.Fatalf("run %+v", run)
	}
	// Already running: refused, and nothing more on the bill.
	if _, err := s.StartDealWith(4, nil, 2, "", 0, now); !errors.Is(err, ErrDealRunning) {
		t.Fatalf("twice: %v", err)
	}
	if bb, _ := s.OpenBill(4); bb.Total() != 2*DealPriceCents {
		t.Fatalf("charged twice: %d", bb.Total())
	}
	// Checks: guests, and all three meals or none.
	if _, err := s.StartDealWith(5, nil, 0, "", 0, now); !errors.Is(err, ErrBadGuests) {
		t.Fatalf("0 guests: %v", err)
	}
	if _, err := s.StartDealWith(5, []string{"Ris", "", "Kött"}, 2, "", 0, now); !errors.Is(err, ErrPlanDishes) {
		t.Fatalf("missing meal 2: %v", err)
	}
	if _, open := s.OpenBill(5); open || s.TablesNow()[4].Run != nil {
		t.Fatal("a refused start left something behind")
	}
	// The manager changes the price on the Menu page: the next table pays that.
	item, _ := s.DealItem()
	item.PriceCents = 2500
	s.SaveMenuItem(item)
	if b, err := s.StartDealWith(6, nil, 3, "", 0, now); err != nil || b.Total() != 7500 {
		t.Fatalf("new price: %d %v", b.Total(), err)
	}
	if it, _ := s.DealItem(); it.ID != item.ID {
		t.Fatal("a second price item was made")
	}
}

func TestSavedMealOrders(t *testing.T) {
	s := newStore(t)
	p, err := s.SaveMealPlan("Ris först", []string{"Ris", "Fisk", "Kött"})
	if err != nil || p.ID == 0 {
		t.Fatal(err)
	}
	// The same name again replaces it.
	if _, err := s.SaveMealPlan("ris först", []string{"Ris", "Kött", "Fisk"}); err != nil {
		t.Fatal(err)
	}
	if ps := s.MealPlans(); len(ps) != 1 || ps[0].Dishes[1] != "Kött" {
		t.Fatalf("plans %+v", ps)
	}
	if _, err := s.SaveMealPlan("", []string{"a", "b", "c"}); !errors.Is(err, ErrPlanName) {
		t.Fatalf("no name: %v", err)
	}
	if _, err := s.SaveMealPlan("x", nil); !errors.Is(err, ErrPlanDishes) {
		t.Fatalf("no dishes: %v", err)
	}
	if err := s.RemoveMealPlan(p.ID); err != nil || len(s.MealPlans()) != 0 {
		t.Fatalf("remove: %v", err)
	}
}
