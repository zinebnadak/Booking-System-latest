package db

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func pizza(qty int) NewLine {
	return NewLine{MenuItemID: 1, Name: "Pizza", PriceCents: 1000, ListCents: 1000, Qty: qty, Note: "no onions"}
}

func soda() NewLine {
	return NewLine{MenuItemID: 2, Name: "Soda", PriceCents: 500, ListCents: 500, Qty: 1, Drink: true}
}

// The example from the restaurant: table 5 orders a pizza (10 €), later a
// soda (5 €). The chef only ever sees the pizza; the bill is 15 €.
func TestTableFiveBill(t *testing.T) {
	s := newStore(t)
	if _, ok := s.OpenBill(5); ok {
		t.Fatal("a new table has no bill")
	}
	b, tk, err := s.AddToBill(5, 0, []NewLine{pizza(1)}, "", 0, at("18:00"))
	if err != nil || tk == nil || b.Total() != 1000 || b.Table != 5 {
		t.Fatalf("first order: %+v %+v %v", b, tk, err)
	}
	if len(tk.Items) != 1 || tk.Items[0].Name != "Pizza" || tk.Items[0].Note != "no onions" || tk.Table != 5 {
		t.Fatalf("ticket: %+v", tk)
	}

	// A soda while eating: on the same bill, nothing for the chef.
	b2, tk2, err := s.AddToBill(5, b.ID, []NewLine{soda()}, "", 0, at("18:20"))
	if err != nil || tk2 != nil || b2.ID != b.ID || b2.Total() != 1500 || b2.Count() != 2 {
		t.Fatalf("soda: %+v %+v %v", b2, tk2, err)
	}
	if q := s.OpenTickets(); len(q) != 1 || q[0].Items[0].Name != "Pizza" {
		t.Fatalf("kitchen queue: %+v", q)
	}

	// The chef finishes the pizza; a mistaken Done can be undone.
	if err := s.TicketDone(tk.ID, false, at("18:15")); err != nil || len(s.OpenTickets()) != 0 {
		t.Fatalf("done: %v", err)
	}
	s.TicketDone(tk.ID, true, at("18:16"))
	if len(s.OpenTickets()) != 1 {
		t.Fatal("undo didn't put it back")
	}
	s.TicketDone(tk.ID, false, at("18:17"))

	// Pay 15 € by card: the table is free again.
	paid, err := s.PayBill(b.ID, PaidCard, at("19:00"))
	if err != nil || paid.ToPay() != 1500 || paid.PaidBy != PaidCard {
		t.Fatalf("pay: %+v %v", paid, err)
	}
	if _, ok := s.OpenBill(5); ok {
		t.Fatal("bill still open after paying")
	}
	if _, err := s.PayBill(b.ID, PaidCash, at("19:01")); !errors.Is(err, ErrBillClosed) {
		t.Fatalf("paid twice: %v", err)
	}
	if day := s.PaidBillsOn("2026-10-31"); len(day) != 1 || day[0].Total() != 1500 {
		t.Fatalf("paid today: %+v", day)
	}

	// A waiter whose screen still shows the old bill can't add to it: the
	// next guests at table 5 get their own bill.
	if _, _, err := s.AddToBill(5, b.ID, []NewLine{soda()}, "", 0, at("19:05")); !errors.Is(err, ErrStale) {
		t.Fatalf("added to a paid bill: %v", err)
	}
	nb, _, _ := s.AddToBill(5, 0, []NewLine{soda()}, "", 0, at("19:10"))
	if nb.ID == b.ID || nb.Total() != 500 {
		t.Fatalf("next guests: %+v", nb)
	}
}

func TestChangeAndRemoveLines(t *testing.T) {
	s := newStore(t)
	b, tk, _ := s.AddToBill(2, 0, []NewLine{pizza(2), soda()}, "", 0, at("18:00"))
	pz, sd := b.Lines[0], b.Lines[1]

	// Any price can change (e.g. a discount). Drinks can change quantity.
	if err := s.UpdateLine(b.ID, pz.ID, 800, 2, pz.Note); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateLine(b.ID, sd.ID, 500, 3, ""); err != nil {
		t.Fatal(err)
	}
	b, _ = s.OpenBill(2)
	if b.Total() != 2*800+3*500 || !b.Lines[0].PriceChanged() || b.Lines[1].PriceChanged() {
		t.Fatalf("after changes: %+v", b.Lines)
	}
	// Food the chef already has: only the price may change.
	if err := s.UpdateLine(b.ID, pz.ID, 800, 1, pz.Note); !errors.Is(err, ErrSentToKitchen) {
		t.Fatalf("changed quantity of sent food: %v", err)
	}

	// Taking the pizza off strikes it through on the chef's ticket.
	kitchen, err := s.RemoveLine(b.ID, pz.ID)
	if err != nil || !kitchen {
		t.Fatalf("remove: %v %v", kitchen, err)
	}
	q := s.OpenTickets()
	if len(q) != 1 || q[0].ID != tk.ID || !q[0].Items[0].Cancelled || q[0].Live() {
		t.Fatalf("ticket after removing: %+v", q)
	}
	if kitchen, _ := s.RemoveLine(b.ID, sd.ID); kitchen {
		t.Fatal("a drink touched the kitchen")
	}
	if _, err := s.PayBill(b.ID, PaidCash, at("19:00")); !errors.Is(err, ErrEmptyBill) {
		t.Fatalf("paid an empty bill: %v", err)
	}
}

func TestDepositComesOffOnce(t *testing.T) {
	s := newStore(t)
	b, _, _ := s.AddToBill(3, 0, []NewLine{pizza(3)}, "ref123", 2000, at("18:00"))
	if b.Deposit() != 2000 || b.ToPay() != 1000 {
		t.Fatalf("deposit: %+v", b)
	}
	s.PayBill(b.ID, PaidCard, at("19:00"))
	// The same booking can't take its deposit off a second bill.
	b2, _, _ := s.AddToBill(3, 0, []NewLine{soda()}, "ref123", 2000, at("19:30"))
	if b2.Deposit() != 0 || b2.ToPay() != 500 {
		t.Fatalf("deposit used twice: %+v", b2)
	}
	// A deposit bigger than the bill never makes it negative.
	b3, _, _ := s.AddToBill(4, 0, []NewLine{soda()}, "ref999", 2000, at("19:30"))
	if b3.ToPay() != 0 || b3.Deposit() != 500 {
		t.Fatalf("small bill: %+v", b3)
	}
}

func TestMoveAndVoid(t *testing.T) {
	s := newStore(t)
	b, tk, _ := s.AddToBill(1, 0, []NewLine{pizza(1)}, "", 0, at("18:00"))
	s.AddToBill(2, 0, []NewLine{soda()}, "", 0, at("18:00"))
	if err := s.MoveBill(b.ID, 2); !errors.Is(err, ErrTableTaken) {
		t.Fatalf("moved onto a busy table: %v", err)
	}
	if err := s.MoveBill(b.ID, 6); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.OpenBill(6); !ok {
		t.Fatal("bill not on table 6")
	}
	if q := s.OpenTickets(); q[0].ID != tk.ID || q[0].Table != 6 {
		t.Fatalf("the chef's ticket didn't follow: %+v", q)
	}
	if err := s.VoidBill(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.OpenBill(6); ok {
		t.Fatal("voided bill still open")
	}
	if q := s.OpenTickets(); q[0].Live() {
		t.Fatal("voided food still live for the chef")
	}
}

func TestOrdersSurviveRestartAndTidy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, _ := Open(path)
	b, _, _ := s.AddToBill(5, 0, []NewLine{pizza(1), soda()}, "", 0, at("21:00"))
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.OpenBill(5)
	if !ok || got.Total() != 1500 || len(s2.OpenTickets()) != 1 {
		t.Fatalf("after restart: %+v", got)
	}
	nb, _, _ := s2.AddToBill(1, 0, []NewLine{soda()}, "", 0, at("21:10"))
	if nb.ID == b.ID {
		t.Fatal("bill ids repeat after restart")
	}
	// Next morning, yesterday's tickets are off the chef's screen; the open
	// bill stays (it's money, staff close it).
	s2.TidyKitchen(at("21:00").Add(13 * time.Hour))
	if len(s2.OpenTickets()) != 0 {
		t.Fatal("old tickets left on the kitchen screen")
	}
	if _, ok := s2.OpenBill(5); !ok {
		t.Fatal("tidying closed a bill")
	}
}
