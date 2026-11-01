package db

import (
	"errors"
	"testing"
	"time"
)

func TestIngredientsLimitDishes(t *testing.T) {
	s := newStore(t)
	now := at("19:00")
	s.Clock = func() time.Time { return now }
	spaghetti, _ := s.AddIngredient("Spaghetti", "g", 500, 200)
	cheese, _ := s.AddIngredient("Ost", "g", 100, 0)
	if _, err := s.AddIngredient("spaghetti", "g", 1, 0); !errors.Is(err, ErrIngredientName) {
		t.Fatalf("same name twice: %v", err)
	}
	if _, err := s.AddIngredient("", "g", 1, 0); !errors.Is(err, ErrBadIngredient) {
		t.Fatalf("no name: %v", err)
	}
	// Two dishes: carbonara (100 g spaghetti, 30 g cheese), and a dish
	// using only spaghetti.
	carb, _ := s.SaveMenuItem(MenuItem{Category: "Pasta", Name: "Carbonara", PriceCents: 1800, Available: true, Course: CourseMain,
		Uses: []IngredientUse{{ID: spaghetti.ID, Amount: 100}, {ID: cheese.ID, Amount: 30}, {ID: 999, Amount: 5}}})
	bol, _ := s.SaveMenuItem(MenuItem{Category: "Pasta", Name: "Bolognese", PriceCents: 1700, Available: true, Course: CourseMain,
		Uses: []IngredientUse{{ID: spaghetti.ID, Amount: 100}}})
	if m, _ := s.MenuItem(carb.ID); len(m.Uses) != 2 || !m.Limited || m.CanMake != 3 {
		t.Fatalf("carbonara: %+v", m) // an unknown ingredient is dropped; 100 g cheese makes 3
	}

	b, _, err := s.AddToBill(1, 0, []NewLine{lineOf(carb, 2)}, "", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := s.MenuItem(carb.ID); m.CanMake != 1 || !m.RunningLow() || m.LeftNow() != 1 {
		t.Fatalf("after 2: %+v", m)
	}
	// Two more carbonara: only cheese for one. Nothing changes.
	_, _, err = s.AddToBill(1, b.ID, []NewLine{lineOf(bol, 1), lineOf(carb, 2)}, "", 0, now)
	var so *SoldOutError
	if !errors.As(err, &so) || so.Short != "Ost" || so.Left != 1 {
		t.Fatalf("too much: %v %+v", err, so)
	}
	if ing := ingredientNamed(t, s, "Spaghetti"); ing.Left != 300 || ing.Low() {
		t.Fatalf("spaghetti after the failed order: %+v", ing)
	}
	// One more: the cheese is out, so carbonara is out; bolognese isn't.
	if _, _, err := s.AddToBill(1, b.ID, []NewLine{lineOf(carb, 1)}, "", 0, now); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.MenuItem(carb.ID); m.ShortOf != "Ost" || !m.Gone("2026-10-31") || onMenu(s, carb.ID) {
		t.Fatalf("carbonara should be out: %+v", m)
	}
	if m, _ := s.MenuItem(bol.ID); m.CanMake != 2 || !onMenu(s, bol.ID) {
		t.Fatalf("bolognese: %+v", m)
	}
	out, low := s.StockLists(now)
	if len(out) != 1 || out[0].ID != carb.ID || len(low) != 1 || low[0].ID != bol.ID {
		t.Fatalf("lists: %v / %v", out, low)
	}
	// Taking a carbonara off the bill gives its spaghetti and cheese back.
	b, _ = s.OpenBill(1)
	if _, err := s.RemoveLine(b.ID, b.Lines[1].ID); err != nil {
		t.Fatal(err)
	}
	if sp, ch := ingredientNamed(t, s, "Spaghetti"), ingredientNamed(t, s, "Ost"); sp.Left != 300 || ch.Left != 40 {
		t.Fatalf("given back: %d g spaghetti, %d g cheese", sp.Left, ch.Left)
	}
	// A delivery; a count; then the cheese is removed from stock altogether.
	if _, err := s.SetIngredient(cheese.ID, 2000, 300); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetIngredient(cheese.ID, -1, 0); !errors.Is(err, ErrBadIngredient) {
		t.Fatalf("negative: %v", err)
	}
	if using := s.DishesUsing()[cheese.ID]; len(using) != 1 || using[0] != "Carbonara" {
		t.Fatalf("using: %v", using)
	}
	if _, err := s.RemoveIngredient(cheese.ID); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.MenuItem(carb.ID); len(m.Uses) != 1 || m.CanMake != 3 {
		t.Fatalf("after removing the cheese: %+v", m)
	}
	// Saved, and still there after a restart.
	again, err := Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := again.MenuItem(carb.ID); len(m.Uses) != 1 || len(again.Ingredients()) != 1 {
		t.Fatalf("after reopening: %+v %v", m, again.Ingredients())
	}
}

func ingredientNamed(t *testing.T, s *Store, name string) Ingredient {
	t.Helper()
	for _, i := range s.Ingredients() {
		if i.Name == name {
			return i
		}
	}
	t.Fatalf("no ingredient %q", name)
	return Ingredient{}
}

func TestSpecialsEndByThemselves(t *testing.T) {
	s := newStore(t)
	now := at("12:00") // Saturday 31 October
	s.Clock = func() time.Time { return now }
	sp, err := s.SaveSpecial(MenuItem{Name: "Tisdagspasta", PriceCents: 1500, Until: "2026-11-03"})
	if err != nil || sp.Category != SpecialsCategory || !sp.StaffOnly || sp.Course != CourseMain {
		t.Fatalf("special: %+v %v", sp, err)
	}
	// The same name again changes it, it doesn't add another.
	sp2, _ := s.SaveSpecial(MenuItem{Name: "tisdagspasta", PriceCents: 1600, Until: "2026-11-03"})
	if sp2.ID != sp.ID || sp2.PriceCents != 1600 {
		t.Fatalf("again: %+v", sp2)
	}
	staffHas := func() bool {
		for _, m := range s.StaffMenu() {
			if m.ID == sp.ID {
				return true
			}
		}
		return false
	}
	if !staffHas() {
		t.Fatal("not on the order form")
	}
	for _, m := range s.GuestMenu() {
		if m.ID == sp.ID {
			t.Fatal("guests see it")
		}
	}
	now = at("12:00").AddDate(0, 0, 3) // Tuesday: last day
	if !staffHas() {
		t.Fatal("gone on its last day")
	}
	now = at("12:00").AddDate(0, 0, 4) // Wednesday
	if staffHas() {
		t.Fatal("still there after its last day")
	}
	if m, _ := s.MenuItem(sp.ID); !m.Ended("2026-11-04") {
		t.Fatal("should say it ended")
	}
	if _, err := s.SaveSpecial(MenuItem{Name: "X", PriceCents: 100}); !errors.Is(err, ErrBadSpecial) {
		t.Fatalf("no last day: %v", err)
	}
}
