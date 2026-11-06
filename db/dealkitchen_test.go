package db

import (
	"testing"
	"time"
)

// 3 meals in the kitchen: meal 1 goes to the chef at the start. Once it's
// done, the chef sees "2 left: Fisk, Kött" until the guests press the
// button, which sends meal 2. Served from the kitchen counts as served at
// the table. A press by mistake takes the meal back; ending the deal early
// clears the kitchen.
func TestDealInKitchen(t *testing.T) {
	s := newStore(t)
	now := at("18:00")
	s.Clock = func() time.Time { return now }
	if _, err := s.StartDealWith(4, []string{"Ris", "Fisk", "Kött"}, 2, "", 0, now); err != nil {
		t.Fatal(err)
	}
	open := s.OpenTickets()
	if len(open) != 1 || open[0].Meal != 1 || open[0].Items[0].Name != "Ris" || open[0].Items[0].Qty != 2 {
		t.Fatalf("meal 1 to the chef: %+v", open)
	}
	if len(s.DealsWaiting()) != 0 {
		t.Fatal("meal 1 is still cooking: no waiting card")
	}
	m1 := open[0].ID

	// Done and served: the card says what's left.
	now = now.Add(10 * time.Minute)
	s.TicketDone(m1, false, now)
	s.TicketServed(m1, false, now)
	w := s.DealsWaiting()
	if len(w) != 1 || w[0].Table != 4 || w[0].Next != 2 || len(w[0].Left) != 2 || w[0].Left[0] != "Fisk" || w[0].Left[1] != "Kött" {
		t.Fatalf("waiting: %+v", w)
	}

	// The guests press the button: meal 2 to the chef, the card goes.
	button := ""
	for _, b := range s.Buttons() {
		if b.Table == 4 {
			button = b.Token
		}
	}
	now = now.Add(20 * time.Minute)
	if _, err := s.Press(button, 0, now); err != nil {
		t.Fatal(err)
	}
	open = s.OpenTickets()
	if len(open) != 1 || open[0].Meal != 2 || open[0].Items[0].Name != "Fisk" || len(s.DealsWaiting()) != 0 || !s.MealSent(4) {
		t.Fatalf("meal 2: %+v", open)
	}
	// It was a child playing with the button: Not yet takes it back.
	if err := s.NotYet(4); err != nil {
		t.Fatal(err)
	}
	if open := s.OpenTickets(); len(open) != 1 || open[0].Live() || s.MealSent(4) {
		t.Fatalf("not yet: %+v", open)
	}
	s.TicketDone(open[0].ID, false, now) // the chef clears it
	if w := s.DealsWaiting(); len(w) != 1 || w[0].Next != 2 {
		t.Fatalf("back to waiting: %+v", w)
	}

	// Later they tell the waiter, who sends it.
	now = now.Add(5 * time.Minute)
	if err := s.SendNextMeal(4, now); err != nil {
		t.Fatal(err)
	}
	open = s.OpenTickets()
	if len(open) != 1 || open[0].Meal != 2 {
		t.Fatalf("sent by the waiter: %+v", open)
	}
	if err := s.SendNextMeal(4, now); err != nil || len(s.OpenTickets()) != 1 {
		t.Fatalf("sent twice: %v", err)
	}
	// Served from the kitchen: meal 2 is on the table.
	m2 := open[0].ID
	s.TicketDone(m2, false, now.Add(8*time.Minute))
	s.TicketServed(m2, false, now.Add(9*time.Minute))
	if run := s.TablesNow()[3].Run; run == nil || run.Meal() != 2 {
		t.Fatalf("run after meal 2: %+v", run)
	}
	if w := s.DealsWaiting(); len(w) != 1 || w[0].Next != 3 || len(w[0].Left) != 1 || w[0].Left[0] != "Kött" {
		t.Fatalf("1 left: %+v", w)
	}

	// They had enough after two: the waiter ends the deal. The card goes.
	if err := s.EndDeal(4, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(s.DealsWaiting()) != 0 || len(s.OpenTickets()) != 0 || len(s.ReadyToServe(now)) != 0 {
		t.Fatal("still in the kitchen after the deal ended")
	}
}

// The waiter taps "Meal 2 served" on the table page while the ticket is
// still on the kitchen screen: it leaves the kitchen. And a paid bill
// takes the card away.
func TestDealServedByWaiter(t *testing.T) {
	s := newStore(t)
	now := at("18:00")
	s.Clock = func() time.Time { return now }
	b, err := s.StartDealWith(2, nil, 1, "", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if open := s.OpenTickets(); len(open) != 1 || open[0].Items[0].Name != "Rätt 1" {
		t.Fatalf("no dishes chosen: %+v", open)
	}
	if err := s.SendNextMeal(2, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ServeNext(2, 2, now); err != nil {
		t.Fatal(err)
	}
	for _, tk := range s.TicketsForBill(b.ID) {
		if tk.Meal == 2 && (tk.DoneAt == nil || tk.ServedAt == nil) {
			t.Fatalf("meal 2 still in the kitchen: %+v", tk)
		}
	}
	for _, tk := range s.OpenTickets() {
		s.TicketDone(tk.ID, false, now)
	}
	if w := s.DealsWaiting(); len(w) != 1 || w[0].Next != 3 {
		t.Fatalf("waiting: %+v", w)
	}
	if _, err := s.PayBill(b.ID, PaidCard, now); err != nil {
		t.Fatal(err)
	}
	if len(s.DealsWaiting()) != 0 {
		t.Fatal("paid and gone, still a card")
	}
}
