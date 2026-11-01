package db

import (
	"errors"
	"testing"
	"time"
)

// Cooking → ready to pick up → served, and back with Undo.
func TestReadyThenServed(t *testing.T) {
	s := newStore(t)
	now := at("19:00")
	s.Clock = func() time.Time { return now }
	b, _, err := s.AddToBill(5, 0, []NewLine{{Name: "Burger", PriceCents: 1800, Qty: 1}}, "", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	var tk Ticket
	for _, x := range s.TicketsForBill(b.ID) {
		tk = x
	}
	if !tk.ForTable() || tk.ReadyToServe() {
		t.Fatalf("new ticket: %+v", tk)
	}
	// Not cooked yet: it can't be served.
	if err := s.TicketServed(tk.ID, false, now); !errors.Is(err, ErrNotReady) {
		t.Fatalf("served before done: %v", err)
	}

	// The chef taps Done: it waits at the pass.
	done := now.Add(12 * time.Minute)
	s.TicketDone(tk.ID, false, done)
	ready := s.ReadyToServe(done)
	if len(ready) != 1 || ready[0].ID != tk.ID {
		t.Fatalf("ready: %+v", ready)
	}
	if len(s.RecentDone(4)) != 0 {
		t.Fatal("waiting at the pass isn't 'just done' yet")
	}

	// A waiter takes it: served.
	served := done.Add(2 * time.Minute)
	if err := s.TicketServed(tk.ID, false, served); err != nil {
		t.Fatal(err)
	}
	if len(s.ReadyToServe(served)) != 0 || len(s.RecentDone(4)) != 1 {
		t.Fatal("served: not ready any more, it's 'just done'")
	}
	// Tapped twice (the chef and the waiter): nothing changes.
	if err := s.TicketServed(tk.ID, false, served.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := s.TicketsForBill(b.ID)[tk.ID]; !got.ServedAt.Equal(served) {
		t.Fatalf("served at %v", got.ServedAt)
	}

	// Undo: back at the pass; Undo again: back to cooking.
	s.TicketDone(tk.ID, true, served)
	if len(s.ReadyToServe(served)) != 1 {
		t.Fatal("undo served: ready again")
	}
	s.TicketDone(tk.ID, true, served)
	if got := s.TicketsForBill(b.ID)[tk.ID]; got.DoneAt != nil || got.ServedAt != nil {
		t.Fatalf("undo done: %+v", got)
	}

	// Back at the pass; a ticket that doesn't exist can't be served.
	s.TicketDone(tk.ID, false, served)
	if err := s.TicketServed(999, false, served); !errors.Is(err, ErrNoTicket) {
		t.Fatalf("no ticket: %v", err)
	}

	// The table pays and leaves before anyone tapped Served: off the list.
	if _, err := s.PayBill(b.ID, PaidCard, served); err != nil {
		t.Fatal(err)
	}
	if len(s.ReadyToServe(served)) != 0 {
		t.Fatal("paid table still waits for food")
	}
}

// Left at the pass from yesterday: tidied away the next day.
func TestTidyKitchenServesYesterday(t *testing.T) {
	s := newStore(t)
	now := at("21:30")
	s.Clock = func() time.Time { return now }
	b, _, _ := s.AddToBill(3, 0, []NewLine{{Name: "Pasta", PriceCents: 1500, Qty: 1}}, "", 0, now)
	for _, tk := range s.TicketsForBill(b.ID) {
		s.TicketDone(tk.ID, false, now)
	}
	next := now.Add(14 * time.Hour)
	if err := s.TidyKitchen(next); err != nil {
		t.Fatal(err)
	}
	for _, tk := range s.TicketsForBill(b.ID) {
		if tk.ServedAt == nil {
			t.Fatal("yesterday's food still waits at the pass")
		}
	}
}
