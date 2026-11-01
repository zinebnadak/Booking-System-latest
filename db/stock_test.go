package db

import (
	"errors"
	"testing"
	"time"
)

// menuItemNamed finds a demo menu item by name.
func menuItemNamed(t *testing.T, s *Store, name string) MenuItem {
	t.Helper()
	for _, m := range s.Menu(false) {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no %q on the menu", name)
	return MenuItem{}
}

func lineOf(m MenuItem, qty int) NewLine {
	return NewLine{MenuItemID: m.ID, Name: m.Name, PriceCents: m.PriceCents, ListCents: m.PriceCents, Qty: qty, Drink: m.Drink}
}

func onMenu(s *Store, id int) bool {
	for _, m := range s.Menu(true) {
		if m.ID == id {
			return true
		}
	}
	return false
}

func TestStockCountsDownAndRunsOut(t *testing.T) {
	s := newStore(t)
	now := at("19:00")
	s.Clock = func() time.Time { return now }
	wine := menuItemNamed(t, s, "Husets vin, glas")
	soda := menuItemNamed(t, s, "Läsk 33 cl")

	if _, err := s.SetStock(wine.ID, 3); err != nil {
		t.Fatal(err)
	}
	b, _, err := s.AddToBill(1, 0, []NewLine{lineOf(wine, 2), lineOf(soda, 5)}, "", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if m := menuItemNamed(t, s, "Husets vin, glas"); !m.Counted || m.Left != 1 || !m.RunningLow() {
		t.Fatalf("after 2 glasses: %+v", m)
	}
	if m := menuItemNamed(t, s, "Läsk 33 cl"); m.Counted {
		t.Fatalf("soda isn't counted: %+v", m)
	}

	// Two more is too many: nothing is added, the soda isn't touched either.
	_, _, err = s.AddToBill(1, b.ID, []NewLine{lineOf(soda, 1), lineOf(wine, 2)}, "", 0, now)
	var so *SoldOutError
	if !errors.As(err, &so) || so.Left != 1 || so.Name != wine.Name || !errors.Is(err, ErrSoldOut) {
		t.Fatalf("too many: %v", err)
	}
	if bill, _ := s.OpenBill(1); len(bill.Lines) != 2 {
		t.Fatalf("lines after the failed add: %d", len(bill.Lines))
	}

	// The last glass: now it's out, and off every menu guests or staff order from.
	b, _, err = s.AddToBill(1, b.ID, []NewLine{lineOf(wine, 1)}, "", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if m := menuItemNamed(t, s, "Husets vin, glas"); !m.OutOfStock() || !m.Gone("2026-10-31") {
		t.Fatalf("after the last glass: %+v", m)
	}
	if onMenu(s, wine.ID) {
		t.Fatal("out of stock but still on the menu")
	}
	staffSees := false
	for _, m := range s.StaffMenu() {
		staffSees = staffSees || m.ID == wine.ID
	}
	if !staffSees {
		t.Fatal("waiters should still see it (greyed out)")
	}
	out, low := s.StockLists(now)
	if len(out) != 1 || out[0].ID != wine.ID || len(low) != 0 {
		t.Fatalf("lists: out %v low %v", out, low)
	}

	// Taking the glass off the bill (typed by mistake) gives it back.
	last := b.Lines[len(b.Lines)-1]
	if _, err := s.RemoveLine(b.ID, last.ID); err != nil {
		t.Fatal(err)
	}
	if m := menuItemNamed(t, s, "Husets vin, glas"); m.Left != 1 || !onMenu(s, wine.ID) {
		t.Fatalf("after taking it back: %+v", m)
	}

	// Changing 2 glasses to 3 takes the last one; back to 1 gives two back.
	first := b.Lines[0]
	if err := s.UpdateLine(b.ID, first.ID, first.PriceCents, 3, ""); err != nil {
		t.Fatal(err)
	}
	if m := menuItemNamed(t, s, "Husets vin, glas"); m.Left != 0 {
		t.Fatalf("after 2 → 3: %d left", m.Left)
	}
	if err := s.UpdateLine(b.ID, first.ID, first.PriceCents, 4, ""); !errors.Is(err, ErrSoldOut) {
		t.Fatalf("3 → 4 with none left: %v", err)
	}
	if err := s.UpdateLine(b.ID, first.ID, first.PriceCents, 1, ""); err != nil {
		t.Fatal(err)
	}
	if m := menuItemNamed(t, s, "Husets vin, glas"); m.Left != 2 {
		t.Fatalf("after 3 → 1: %d left", m.Left)
	}
}

func TestStockButtons(t *testing.T) {
	s := newStore(t)
	now := at("19:00")
	s.Clock = func() time.Time { return now }
	fish := menuItemNamed(t, s, "Fisksoppa")

	// Out of stock, then back: on the menu again, not counted.
	if m, err := s.SetStock(fish.ID, 0); err != nil || !m.OutOfStock() {
		t.Fatalf("out: %+v %v", m, err)
	}
	if onMenu(s, fish.ID) {
		t.Fatal("still on the menu")
	}
	if _, _, err := s.AddToBill(2, 0, []NewLine{lineOf(fish, 1)}, "", 0, now); !errors.Is(err, ErrSoldOut) {
		t.Fatalf("ordering it anyway: %v", err)
	}
	if m, err := s.SetStock(fish.ID, NotCounted); err != nil || m.Counted || !onMenu(s, fish.ID) {
		t.Fatalf("back: %+v %v", m, err)
	}

	// The kitchen's "sold out today" also goes when someone counts new stock.
	if _, err := s.SetSoldOut(fish.ID, true, now); err != nil {
		t.Fatal(err)
	}
	if out, _ := s.StockLists(now); len(out) != 1 {
		t.Fatalf("sold out today should be on the out list: %v", out)
	}
	if m, _ := s.SetStock(fish.ID, 8); m.SoldOutOn != "" || !onMenu(s, fish.ID) {
		t.Fatalf("counted 8: %+v", m)
	}

	// The kitchen's "back on" ends out of stock too.
	s.SetStock(fish.ID, 0)
	if m, _ := s.SetSoldOut(fish.ID, false, now); m.Counted || !onMenu(s, fish.ID) {
		t.Fatalf("kitchen back on: %+v", m)
	}

	// Bad numbers.
	for _, n := range []int{-2, MaxStock + 1} {
		if _, err := s.SetStock(fish.ID, n); !errors.Is(err, ErrBadStock) {
			t.Fatalf("SetStock(%d): %v", n, err)
		}
	}
	if _, err := s.SetStock(9999, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown item: %v", err)
	}

	// Saving the dish on the menu page keeps the count.
	s.SetStock(fish.ID, 5)
	m := menuItemNamed(t, s, "Fisksoppa")
	m.Counted, m.Left, m.Description = false, 0, "Ny beskrivning"
	if _, err := s.SaveMenuItem(m); err != nil {
		t.Fatal(err)
	}
	if m := menuItemNamed(t, s, "Fisksoppa"); !m.Counted || m.Left != 5 {
		t.Fatalf("after saving on the menu page: %+v", m)
	}

	// And it all survives a restart.
	s2, err := Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if m := menuItemNamed(t, s2, "Fisksoppa"); !m.Counted || m.Left != 5 {
		t.Fatalf("after reopening: %+v", m)
	}
}

func TestStockPhoneOrders(t *testing.T) {
	s := newStore(t)
	s.Clock = func() time.Time { return at("19:00") }
	soda := menuItemNamed(t, s, "Läsk 33 cl")
	s.SetStock(soda.ID, 2)
	if _, err := s.OpenForGuests(5, true, "", 0, at("18:00")); err != nil {
		t.Fatal(err)
	}
	_, g, err := s.JoinTable(qrCode(t, s, 5), at("18:01"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GuestOrder(g.Key, []NewLine{lineOf(soda, 3)}, at("18:02")); !errors.Is(err, ErrSoldOut) {
		t.Fatalf("3 when 2 are left: %v", err)
	}

	// A waiter checks phone orders: the sodas are counted at once, and come
	// back when the order is rejected.
	if err := s.SetCheckPhoneOrders(true, at("18:02")); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.GuestOrder(g.Key, []NewLine{lineOf(soda, 2)}, at("18:03"))
	if err != nil || !waiting {
		t.Fatalf("waiting order: %v %v", waiting, err)
	}
	if m := menuItemNamed(t, s, "Läsk 33 cl"); m.Left != 0 {
		t.Fatalf("while it waits: %d left", m.Left)
	}
	orders := s.PendingPhoneOrders()
	if len(orders) != 1 {
		t.Fatalf("pending: %d", len(orders))
	}
	if _, err := s.DecidePhoneOrder(orders[0].ID, false, at("18:04")); err != nil {
		t.Fatal(err)
	}
	if m := menuItemNamed(t, s, "Läsk 33 cl"); m.Left != 2 {
		t.Fatalf("after rejecting: %d left", m.Left)
	}

	// Staff adding for this guest count too.
	b, _ := s.OpenBill(5)
	if _, _, err := s.AddForGuest(b.ID, g.Guest, []NewLine{lineOf(soda, 3)}, at("18:05")); !errors.Is(err, ErrSoldOut) {
		t.Fatalf("staff for the guest: %v", err)
	}
}
