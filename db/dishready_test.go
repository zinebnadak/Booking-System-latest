package db

import "testing"

// The chef ticks the soup first and the burgers later; the last tick makes
// the ticket done. Unticking works until then.
func TestDishReady(t *testing.T) {
	s := newStore(t)
	_, tk, err := s.AddToBill(1, 0, []NewLine{
		{Name: "Soup", PriceCents: 800, ListCents: 800, Qty: 1},
		{Name: "Burger", PriceCents: 1500, ListCents: 1500, Qty: 2},
	}, "", 0, now0)
	if err != nil || tk == nil {
		t.Fatal(err)
	}
	id := tk.ID

	if _, err := s.DishReady(id, 0, "Burger", false, now0); err != ErrTicketChanged {
		t.Fatalf("wrong name: %v", err)
	}
	if _, err := s.DishReady(id, 5, "Soup", false, now0); err != ErrTicketChanged {
		t.Fatalf("no such dish: %v", err)
	}
	done, err := s.DishReady(id, 0, "Soup", false, now0)
	if err != nil || done {
		t.Fatalf("soup: done=%v err=%v", done, err)
	}
	got, _ := s.TicketByID(id)
	if got.DoneAt != nil || got.Items[0].ReadyAt == nil || !got.PartReady() {
		t.Fatalf("after soup %+v", got)
	}
	b, _ := s.OpenBill(1)
	if got.ReadyDish(b.Lines[0].ID) == nil || got.ReadyDish(b.Lines[1].ID) != nil {
		t.Fatal("the table page should see the soup ready, not the burgers")
	}

	// Tapped twice: nothing changes. Undo, then tick again.
	if done, err := s.DishReady(id, 0, "Soup", false, now0); err != nil || done {
		t.Fatalf("twice: %v %v", done, err)
	}
	if _, err := s.DishReady(id, 0, "Soup", true, now0); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.TicketByID(id); got.Items[0].ReadyAt != nil || got.PartReady() {
		t.Fatal("undo should untick the soup")
	}
	s.DishReady(id, 0, "Soup", false, now0)

	// The burgers: the last dish, so the ticket is done.
	done, err = s.DishReady(id, 1, "Burger", false, now0)
	if err != nil || !done {
		t.Fatalf("burgers: done=%v err=%v", done, err)
	}
	if got, _ := s.TicketByID(id); got.DoneAt == nil || got.PartReady() {
		t.Fatalf("ticket should be done: %+v", got)
	}
	// Done tickets aren't on the screen to tick any more.
	if _, err := s.DishReady(id, 0, "Soup", true, now0); err != ErrTicketChanged {
		t.Fatalf("untick a done ticket: %v", err)
	}

	// Saved and loaded again, the ticks are still there.
	s2, err := Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s2.TicketByID(id); got.Items[1].ReadyAt == nil {
		t.Fatal("ticks weren't saved")
	}
}
