package db

import "testing"

// Two tables pushed together: the second bill goes onto the first, and the
// kitchen's tickets follow. A dish can move on its own, or part of it.
func TestJoinAndMoveDish(t *testing.T) {
	s := newStore(t)
	add := func(table int, name string, qty int) Bill {
		t.Helper()
		b, _, err := s.AddToBill(table, 0, []NewLine{{Name: name, PriceCents: 1000, ListCents: 1000, Qty: qty}}, "", 0, now0)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	b1 := add(1, "Burger", 2)
	b2 := add(2, "Pasta", 1)
	b2, _ = s.OpenBill(2)
	b1, _ = s.OpenBill(1)

	joined, err := s.JoinBills(b2.ID, 1, now0)
	if err != nil {
		t.Fatal(err)
	}
	if joined.ID != b1.ID || len(joined.Lines) != 2 || joined.Total() != 3000 {
		t.Fatalf("joined %+v", joined)
	}
	if _, open := s.OpenBill(2); open {
		t.Fatal("table 2 should be free")
	}
	old, _ := s.BillByID(b2.ID)
	if old.MergedInto != b1.ID || !old.Voided || len(old.Lines) != 0 {
		t.Fatalf("old bill %+v", old)
	}
	tickets := s.TicketsForBill(b1.ID)
	if len(tickets) != 2 {
		t.Fatalf("tickets on table 1: %d", len(tickets))
	}
	for _, tk := range tickets {
		if tk.Table != 1 {
			t.Fatalf("ticket still on table %d", tk.Table)
		}
	}
	if _, err := s.JoinBills(b1.ID, 1, now0); err != ErrJoinSelf {
		t.Fatalf("join with itself: %v", err)
	}

	// One of the two burgers goes to table 3 (a new bill there).
	var burger BillLine
	for _, l := range joined.Lines {
		if l.Name == "Burger" {
			burger = l
		}
	}
	b3, err := s.MoveLine(b1.ID, burger.ID, 1, 3, now0)
	if err != nil {
		t.Fatal(err)
	}
	if b3.Table != 3 || len(b3.Lines) != 1 || b3.Lines[0].Qty != 1 || b3.Lines[0].TicketID == 0 {
		t.Fatalf("table 3: %+v", b3)
	}
	b1, _ = s.OpenBill(1)
	if b1.Total() != 2000 {
		t.Fatalf("table 1 now %d", b1.Total())
	}
	tk := s.TicketsForBill(b3.ID)[b3.Lines[0].TicketID]
	if tk.Table != 3 || len(tk.Items) != 1 || tk.Items[0].Qty != 1 || tk.Items[0].LineID != b3.Lines[0].ID {
		t.Fatalf("moved ticket %+v", tk)
	}
	left := s.TicketsForBill(b1.ID)[burger.TicketID]
	if len(left.Items) != 1 || left.Items[0].Qty != 1 {
		t.Fatalf("ticket left on table 1: %+v", left)
	}
	// The whole bill still adds up: 3 dishes, 30 €, on two tables.
	if b1.Total()+b3.Total() != 3000 {
		t.Fatal("money got lost moving")
	}
}
