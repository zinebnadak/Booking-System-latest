package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"emeal-booking/schedule"
)

func TestRealMenu(t *testing.T) {
	defer UseStartMenu(seedMenu)()
	s := newStore(t)
	byName := map[string]MenuItem{}
	for _, m := range s.Menu(false) {
		byName[m.Name] = m
	}
	for name, want := range map[string]int{
		"Råbiff": 1790, "Jägarschnitzel": 2250, "Oxfilé": 3990, "Chipotle bacon burgare": 1990,
		"Aperol spritz": 990, "Cuma Malbec, flaska": 4200, "Heineken 0,5 l": 900, "Coca-Cola": 350,
	} {
		if got := byName[name].PriceCents; got != want {
			t.Errorf("%s: %d, want %d", name, got, want)
		}
	}
	if m := byName["Aperol spritz"]; !m.Drink || !m.Alcohol || m.VAT() != schedule.VATAlcohol {
		t.Errorf("Aperol spritz: drink %v alcohol %v vat %d", m.Drink, m.Alcohol, m.VAT())
	}
	if m := byName["Heineken 0,0 %"]; !m.Drink || m.Alcohol || m.VAT() != schedule.VATFood {
		t.Errorf("alcohol-free beer: alcohol %v vat %d", m.Alcohol, m.VAT())
	}
	if byName["Råbiff"].Course != CourseStarter || byName["Oxfilé"].Course != CourseMain || byName["Chokladmousse"].Course != CourseDessert {
		t.Error("courses wrong")
	}
	if byName["Cordon bleu"].Available || !byName["Jägarschnitzel"].Available {
		t.Error("only this week's schnitzel should be on")
	}
	// Dishes without a price: on the staff menu, never for guests.
	if !byName["Dagens lunch"].PriceMissing() {
		t.Fatal("Dagens lunch should have no price yet")
	}
	for _, m := range s.GuestMenu() {
		if m.PriceMissing() {
			t.Fatalf("guests can see %s without a price", m.Name)
		}
	}
	for _, m := range s.TakeawayMenu() {
		if m.Drink || m.PriceMissing() {
			t.Fatalf("takeaway has %s", m.Name)
		}
	}
}

func TestDemoMenuIsReplacedOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	// An old data file: the demo menu, no menu version.
	old := data{NextBookingID: 1, NextWaitID: 1, NextMenuID: 1}
	for _, it := range DemoMenu() {
		it := it
		it.ID = old.NextMenuID
		old.NextMenuID++
		old.Menu = append(old.Menu, &it)
	}
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	menu := s.Menu(false)
	if len(menu) != len(seedMenu()) || menu[0].ID <= len(DemoMenu()) {
		t.Fatalf("menu after upgrade: %d items, first id %d", len(menu), menu[0].ID)
	}
	// A menu the manager made is never replaced.
	path2 := filepath.Join(dir, "own.json")
	own := data{NextBookingID: 1, NextWaitID: 1, NextMenuID: 2, Menu: []*MenuItem{{ID: 1, Name: "Husets pizza", PriceCents: 1500, Available: true}}}
	raw, _ = json.Marshal(own)
	_ = os.WriteFile(path2, raw, 0o600)
	s2, err := Open(path2)
	if err != nil {
		t.Fatal(err)
	}
	if m := s2.Menu(false); len(m) != 1 || m[0].Name != "Husets pizza" {
		t.Fatalf("own menu touched: %+v", m)
	}
}

func TestLinesKeepVATAndCourse(t *testing.T) {
	s := newStore(t)
	b, _, err := s.AddToBill(5, 0, []NewLine{
		{Name: "Råbiff", PriceCents: 1790, Qty: 1, VAT: schedule.VATFood, Course: CourseStarter},
		{Name: "Aperol spritz", PriceCents: 990, Qty: 2, Drink: true, VAT: schedule.VATAlcohol},
		{Name: "Gammal rad", PriceCents: 500, Qty: 1},
	}, "", 0, time.Date(2026, 10, 30, 18, 0, 0, 0, schedule.Location))
	if err != nil {
		t.Fatal(err)
	}
	if b.Lines[0].Course != CourseStarter || b.Lines[1].VATRate() != schedule.VATAlcohol || b.Lines[2].VATRate() != schedule.VATFood {
		t.Fatalf("lines: %+v", b.Lines)
	}
}
