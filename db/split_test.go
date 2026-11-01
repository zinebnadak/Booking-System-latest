package db

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// fourDishes opens a bill on table 5: four mains and a shared bottle.
func fourDishes(t *testing.T, s *Store, deposit int) Bill {
	t.Helper()
	b, _, err := s.AddToBill(5, 0, []NewLine{
		{Name: "Burger", PriceCents: 1990, ListCents: 1990, Qty: 1},
		{Name: "Fisksoppa", PriceCents: 1550, ListCents: 1550, Qty: 1},
		{Name: "Sallad", PriceCents: 1150, ListCents: 1150, Qty: 1},
		{Name: "Risotto", PriceCents: 2190, ListCents: 2190, Qty: 1},
		{Name: "Vin, flaska", PriceCents: 3301, ListCents: 3301, Qty: 1, Drink: true},
	}, "ref-booking", deposit, at("18:00"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sum(xs []int) int {
	n := 0
	for _, x := range xs {
		n += x
	}
	return n
}

func TestSplitByWhatEachHad(t *testing.T) {
	s := newStore(t)
	b := fourDishes(t, s, 0)
	if b.Ref == "" || len(b.Ref) != 32 {
		t.Fatalf("bill has no receipt code: %q", b.Ref)
	}
	if err := s.SplitBill(b.ID, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PayShare(b.ID, 1, PaidCard, at("19:00")); !errors.Is(err, ErrUnassigned) {
		t.Fatalf("paying before everything is given out: %v", err)
	}
	// Each had their own main; guests 1 and 2 shared the wine.
	ids := map[string]int{}
	for _, l := range b.Lines {
		ids[l.Name] = l.ID
	}
	for name, who := range map[string][]int{"Burger": {1}, "Fisksoppa": {2}, "Sallad": {3}, "Risotto": {4}, "Vin, flaska": {2, 1, 2}} {
		if err := s.AssignLine(b.ID, ids[name], who); err != nil {
			t.Fatal(name, err)
		}
	}
	got, _ := s.BillByID(b.ID)
	pay, lines, _ := got.Parts()
	// The odd cent of the 33,01 bottle goes to guest 1.
	want := []int{1990 + 1651, 1550 + 1650, 1150, 2190}
	for i := range want {
		if pay[i] != want[i] {
			t.Fatalf("parts %v, want %v", pay, want)
		}
	}
	if sum(pay) != got.Total() {
		t.Fatalf("parts add up to %d, bill is %d", sum(pay), got.Total())
	}
	if len(lines[1]) != 2 || lines[1][1].Name != "Vin, flaska" || lines[1][1].SharedBy != 2 || lines[0][0].Qty != 1 {
		t.Fatalf("guest 2's receipt: %+v", lines[1])
	}

	// They pay one by one; the table closes with the last one.
	for g, m := range map[int]string{1: PaidCard, 2: PaidCash, 3: PaidCard} {
		closed, err := s.PayShare(b.ID, g, m, at("19:10"))
		if err != nil || closed {
			t.Fatalf("guest %d: %v %v", g, closed, err)
		}
	}
	// Guest 1 paid: their dishes are fixed now.
	if err := s.AssignLine(b.ID, ids["Burger"], []int{4}); !errors.Is(err, ErrSharePaid) {
		t.Fatalf("moving a paid guest's dish: %v", err)
	}
	if _, err := s.RemoveLine(b.ID, ids["Vin, flaska"]); !errors.Is(err, ErrSharePaid) {
		t.Fatalf("removing a paid guest's wine: %v", err)
	}
	if err := s.SplitBill(b.ID, 2); !errors.Is(err, ErrSharePaid) {
		t.Fatalf("taking away guest 3, who paid: %v", err)
	}
	if err := s.VoidBill(b.ID); !errors.Is(err, ErrSharePaid) {
		t.Fatalf("voiding after a payment: %v", err)
	}
	if _, err := s.PayBill(b.ID, PaidCard, at("19:11")); !errors.Is(err, ErrSplit) {
		t.Fatalf("paying a split bill in one go: %v", err)
	}
	// A mistaken tap can be undone while the bill is open.
	if err := s.UndoShare(b.ID, 3); err != nil {
		t.Fatal(err)
	}
	s.PayShare(b.ID, 3, PaidCard, at("19:12"))
	closed, err := s.PayShare(b.ID, 4, PaidCard, at("19:13"))
	if err != nil || !closed {
		t.Fatalf("last guest: %v %v", closed, err)
	}
	paid, _ := s.BillByID(b.ID)
	if paid.Open() || paid.PaidBy != PaidSplit || !paid.PaidAt.Equal(at("19:13")) {
		t.Fatalf("bill after everyone paid: %+v", paid)
	}
	card, cash, _ := paid.PaidWith()
	if cash != 1550+1650 || card+cash != paid.Total() {
		t.Fatalf("card %d cash %d", card, cash)
	}
	if _, ok := s.OpenBill(5); ok {
		t.Fatal("table 5 still has an open bill")
	}

	// Each guest's receipt (and the whole bill's) can be found by its code.
	for _, sh := range paid.Split {
		rb, g, ok := s.ReceiptByRef(sh.Ref)
		if !ok || g != sh.Guest || rb.ID != b.ID {
			t.Fatalf("receipt for guest %d: %v %d", sh.Guest, ok, g)
		}
	}
	if _, g, ok := s.ReceiptByRef(paid.Ref); !ok || g != 0 {
		t.Fatal("whole-bill receipt not found")
	}
	if _, _, ok := s.ReceiptByRef("0123456789abcdef0123456789abcdef"); ok {
		t.Fatal("made-up code found a receipt")
	}
}

func TestSplitEvenlyDepositAndSeparate(t *testing.T) {
	s := newStore(t)
	b := fourDishes(t, s, 2000) // they paid a 20 € deposit online
	s.SplitBill(b.ID, 3)
	if err := s.SplitEvenly(b.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.BillByID(b.ID)
	pay, _, dep := got.Parts()
	if sum(pay) != got.ToPay() || dep[0] != 2000 || dep[1] != 0 {
		t.Fatalf("parts %v deposit %v, to pay %d", pay, dep, got.ToPay())
	}
	// Evenly, before the deposit: 10181 / 3 = 3394, 3394, 3393.
	if pay[0] != 3394-2000 || pay[1] != 3394 || pay[2] != 3393 {
		t.Fatalf("even parts %v", pay)
	}

	// Three sodas on one line: separated, so each can go to someone else.
	b2, _, _ := s.AddToBill(5, b.ID, []NewLine{{Name: "Läsk", PriceCents: 350, ListCents: 350, Qty: 3, Drink: true}}, "", 0, at("18:30"))
	soda := b2.Lines[len(b2.Lines)-1]
	if err := s.SeparateLine(b.ID, soda.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.BillByID(b.ID)
	if len(got.Lines) != 8 || got.Total() != b.Total()+1050 {
		t.Fatalf("after separating: %d lines, %d", len(got.Lines), got.Total())
	}
	if n := len(got.Unassigned()); n != 3 {
		t.Fatalf("%d sodas unassigned, want 3", n)
	}
	seen := map[int]bool{}
	for _, l := range got.Lines {
		if seen[l.ID] {
			t.Fatal("line ids repeat")
		}
		seen[l.ID] = true
	}

	// Fewer guests: guest 3's receipt code goes, 1 and 2 keep theirs.
	keep := got.Split[0].Ref
	if err := s.SplitBill(b.ID, 2); err != nil {
		t.Fatal(err)
	}
	got, _ = s.BillByID(b.ID)
	if len(got.Split) != 2 || got.Split[0].Ref != keep {
		t.Fatalf("split after going to 2: %+v", got.Split)
	}
	for _, l := range got.Lines {
		for _, g := range l.Guests {
			if g > 2 {
				t.Fatal("line still given to guest 3")
			}
		}
	}
	// Not split after all.
	if err := s.SplitBill(b.ID, 1); err != nil {
		t.Fatal(err)
	}
	got, _ = s.BillByID(b.ID)
	if got.IsSplit() || len(got.Unassigned()) != 0 {
		t.Fatalf("still split: %+v", got.Split)
	}
	if _, err := s.PayBill(b.ID, PaidCard, at("19:00")); err != nil {
		t.Fatal(err)
	}
}

func TestOldBillsGetReceiptCodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, _ := Open(path)
	b, _, _ := s.AddToBill(1, 0, []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, "", 0, time.Now())
	s.mu.Lock()
	s.d.Bills[0].Ref = "" // as saved before online receipts existed
	s.save()
	s.mu.Unlock()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s2.BillByID(b.ID); len(got.Ref) != 32 {
		t.Fatalf("old bill got no code: %q", got.Ref)
	}
}
