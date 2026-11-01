package db

import (
	"errors"
	"testing"
	"time"
)

func TestPhoneOrdersWaitForAWaiter(t *testing.T) {
	s := newStore(t)
	b, keys := threeGuests(t, s) // ordered before the check was on
	if err := s.SetCheckPhoneOrders(true, at("18:30")); err != nil {
		t.Fatal(err)
	}
	tickets := len(s.OpenTickets())
	// Guest 1 orders: it waits, nothing on the bill, nothing for the chef.
	waiting, err := s.GuestOrder(keys[0], []NewLine{{Name: "Sorbet", PriceCents: 750, Qty: 1}}, at("18:31"))
	if err != nil || !waiting {
		t.Fatalf("waiting=%v err=%v", waiting, err)
	}
	got, _ := s.BillByID(b.ID)
	if len(got.Lines) != len(b.Lines) || len(s.OpenTickets()) != tickets {
		t.Fatal("a waiting order reached the bill or the chef")
	}
	// Paying waits too: what they owe could still change.
	if _, err := s.StartPayment(keys[0], false, PayHoldFor, at("18:32")); !errors.Is(err, ErrOrderPending) {
		t.Fatalf("paying with an order waiting: %v", err)
	}
	if _, err := s.StartPayment(keys[1], true, PayHoldFor, at("18:32")); !errors.Is(err, ErrOrderPending) {
		t.Fatalf("pay-for-all with an order waiting: %v", err)
	}
	if _, err := s.StartPayment(keys[1], false, PayHoldFor, at("18:32")); err != nil {
		t.Fatalf("another guest paying their own part: %v", err) // not affected
	}
	_ = s.ReleaseAttempt(s.Holds(b.ID, at("18:32"))[0].Ref)
	if _, err := s.PayShare(b.ID, 1, PaidCard, at("18:32")); !errors.Is(err, ErrOrderPending) {
		t.Fatalf("staff taking payment with an order waiting: %v", err)
	}
	// A waiter accepts it: on the bill and to the chef.
	pending := s.PendingPhoneOrders()
	if len(pending) != 1 || pending[0].Table != 5 || pending[0].Guest != 1 {
		t.Fatalf("pending %+v", pending)
	}
	if _, err := s.DecidePhoneOrder(pending[0].ID, true, at("18:33")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecidePhoneOrder(pending[0].ID, false, at("18:33")); !errors.Is(err, ErrStale) {
		t.Fatalf("deciding twice: %v", err)
	}
	got, _ = s.BillByID(b.ID)
	if len(got.Lines) != len(b.Lines)+1 || len(s.OpenTickets()) != tickets+1 {
		t.Fatal("accepted order not on the bill / to the chef")
	}
	// Rejected: never on the bill; the guest sees it for a while.
	_, _ = s.GuestOrder(keys[1], []NewLine{{Name: "Sorbet", PriceCents: 750, Qty: 9}}, at("18:40"))
	_, _ = s.DecidePhoneOrder(s.PendingPhoneOrders()[0].ID, false, at("18:41"))
	if _, rejected := s.GuestPhoneOrders(b.ID, 2, at("18:42")); len(rejected) != 1 {
		t.Fatal("guest doesn't see the rejected order")
	}
	got2, _ := s.BillByID(b.ID)
	if len(got2.Lines) != len(got.Lines) {
		t.Fatal("rejected order on the bill")
	}
	// Nobody decides: through by itself after 5 minutes.
	_, _ = s.GuestOrder(keys[2], []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, at("18:50"))
	if s.AutoAcceptPhoneOrders(at("18:54")) {
		t.Fatal("accepted after 4 minutes")
	}
	if !s.AutoAcceptPhoneOrders(at("18:55")) || len(s.PendingPhoneOrders()) != 0 {
		t.Fatal("not accepted after 5 minutes")
	}
	// Turning the check off lets anything waiting through at once.
	_, _ = s.GuestOrder(keys[2], []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, at("19:00"))
	if err := s.SetCheckPhoneOrders(false, at("19:01")); err != nil {
		t.Fatal(err)
	}
	if len(s.PendingPhoneOrders()) != 0 {
		t.Fatal("still waiting after the check was turned off")
	}
	if waiting, _ := s.GuestOrder(keys[2], []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, at("19:02")); waiting {
		t.Fatal("waiting with the check off")
	}
	_ = time.Second
}
