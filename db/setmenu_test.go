package db

import (
	"testing"
	"time"

	"emeal-booking/schedule"
)

// afterWorkMenu is a set menu like the restaurant's After Work menu (no
// longer on the printed menus, but the set-menu buttons still work).
func afterWorkMenu() []MenuItem {
	items := []MenuItem{
		{Name: "After Work · 3 rätter", PriceCents: 3000, Package: true},
		{Name: "AW Hummersoppa", Included: true, Course: CourseStarter},
		{Name: "AW Blue cheese smokey BBQ-burgare", Included: true, Course: CourseMain},
	}
	for i := range items {
		items[i].Category, items[i].StaffOnly, items[i].Available = "After Work", true, true
	}
	return append(seedMenu(), items...)
}

func TestAfterWorkSetMenu(t *testing.T) {
	defer UseStartMenu(afterWorkMenu)()
	s := newStore(t)
	byName := map[string]MenuItem{}
	for _, m := range s.Menu(true) {
		byName[m.Name] = m
	}
	three, soup, burger := byName["After Work · 3 rätter"], byName["AW Hummersoppa"], byName["AW Blue cheese smokey BBQ-burgare"]
	if !three.Package || three.PriceCents != 3000 || !soup.Included || soup.PriceMissing() || !burger.StaffOnly {
		t.Fatalf("set menu items: %+v %+v", three, soup)
	}
	for _, m := range s.GuestMenu() {
		if m.StaffOnly {
			t.Fatalf("guests can order %s", m.Name)
		}
	}
	// The waiter rings up the set menu and the dishes the guest picked: only
	// the dishes go to the chef, and the price is the set menu's.
	b, ticket, err := s.AddToBill(5, 0, []NewLine{
		{MenuItemID: three.ID, Name: three.Name, PriceCents: 3000, Qty: 1, NoKitchen: true, VAT: schedule.VATFood},
		{MenuItemID: soup.ID, Name: soup.Name, Qty: 1, Course: CourseStarter, Hold: true},
		{MenuItemID: burger.ID, Name: burger.Name, Qty: 1, Course: CourseMain, Hold: true},
	}, "", 0, time.Date(2026, 10, 30, 17, 0, 0, 0, schedule.Location))
	if err != nil {
		t.Fatal(err)
	}
	if b.Total() != 3000 || ticket == nil || len(ticket.Items) != 1 || ticket.Items[0].Name != soup.Name {
		t.Fatalf("total %d, kitchen %+v", b.Total(), ticket)
	}
	if held := s.HeldTickets(); len(held) != 1 || held[0].Items[0].Name != burger.Name {
		t.Fatalf("held %+v", held)
	}
}

func TestCocktailHourPrice(t *testing.T) {
	s := newStore(t)
	var wine MenuItem
	for _, m := range s.Menu(false) {
		if m.Name == "Husets vin, glas" {
			wine = m
		}
	}
	wine.HappyCents = 650
	if _, err := s.SaveMenuItem(wine); err != nil {
		t.Fatal(err)
	}
	price := func(clock string) (int, bool) {
		d := time.Date(2026, 11, 2, 0, 0, 0, 0, schedule.Location) // a Monday
		at, _ := schedule.At(d, clock)
		s.Clock = func() time.Time { return at }
		for _, m := range s.Menu(true) {
			if m.ID == wine.ID {
				return m.PriceCents, m.Happy
			}
		}
		return 0, false
	}
	if p, happy := price("16:30"); p != 650 || !happy {
		t.Fatalf("at 16:30: %d %v", p, happy)
	}
	if p, happy := price("18:00"); p != 850 || happy {
		t.Fatalf("at 18:00: %d %v", p, happy)
	}
	// The menu editor always sees the normal price.
	for _, m := range s.Menu(false) {
		if m.ID == wine.ID && (m.PriceCents != 850 || m.HappyCents != 650) {
			t.Fatalf("editor sees %d / %d", m.PriceCents, m.HappyCents)
		}
	}
}
