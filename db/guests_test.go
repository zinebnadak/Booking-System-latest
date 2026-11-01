package db

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func qrCode(t *testing.T, s *Store, table int) string {
	t.Helper()
	for _, q := range s.TableQRs() {
		if q.Table == table {
			return q.Code
		}
	}
	t.Fatalf("table %d has no QR code", table)
	return ""
}

// threeGuests opens table 5 for QR orders; three phones scan and order.
func threeGuests(t *testing.T, s *Store) (Bill, []string) {
	t.Helper()
	s.Clock = func() time.Time { return at("19:00") }
	code := qrCode(t, s, 5)
	if _, _, err := s.JoinTable(code, at("18:00")); !errors.Is(err, ErrTableClosed) {
		t.Fatalf("joining before staff open the table: %v", err)
	}
	if _, err := s.OpenForGuests(5, true, "", 0, at("18:00")); err != nil {
		t.Fatal(err)
	}
	var keys []string
	orders := [][]NewLine{
		{{Name: "Burger", PriceCents: 1990, Qty: 1}, {Name: "Läsk", PriceCents: 350, Qty: 1, Drink: true}},
		{{Name: "Pizza", PriceCents: 1450, Qty: 1}},
		{{Name: "Sallad", PriceCents: 1150, Qty: 1}},
	}
	for i, lines := range orders {
		_, g, err := s.JoinTable(code, at("18:01"))
		if err != nil {
			t.Fatal(err)
		}
		if g.Guest != i+1 || len(g.Key) != 32 {
			t.Fatalf("guest %d: %+v", i+1, g)
		}
		if _, err := s.GuestOrder(g.Key, lines, at("18:02")); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, g.Key)
	}
	b, _ := s.OpenBill(5)
	return b, keys
}

func TestQRGuestsOrderTheirOwn(t *testing.T) {
	s := newStore(t)
	b, keys := threeGuests(t, s)
	if !b.IsSplit() || len(b.Split) != 3 {
		t.Fatalf("bill isn't split between the 3 phones: %+v", b.Split)
	}
	pay, _, _ := b.Parts()
	if pay[0] != 2340 || pay[1] != 1450 || pay[2] != 1150 {
		t.Fatalf("parts %v", pay)
	}
	if len(b.Unassigned()) != 0 {
		t.Fatal("QR orders should belong to whoever ordered them")
	}
	// The chef gets the food (3 tickets), the waiters the soda.
	if n := len(s.OpenTickets()); n != 3 {
		t.Fatalf("%d kitchen tickets, want 3", n)
	}
	drinks := s.DrinksToBring()
	if len(drinks) != 1 || drinks[0].Items[0].Name != "Läsk" || drinks[0].Items[0].Guest != 1 {
		t.Fatalf("drinks to bring: %+v", drinks)
	}
	if err := s.SetGuestName(keys[1], "  Anna  "); err != nil {
		t.Fatal(err)
	}
	got, _ := s.BillByID(b.ID)
	if got.Split[1].Label() != "Guest 2 · Anna" {
		t.Fatalf("label %q", got.Split[1].Label())
	}
	// Phones can't be taken off the bill, and it can't be un-split.
	if err := s.SplitBill(b.ID, 2); !errors.Is(err, ErrQRGuests) {
		t.Fatalf("removing a phone guest: %v", err)
	}
	if err := s.SplitBill(b.ID, 1); !errors.Is(err, ErrQRGuests) {
		t.Fatalf("un-splitting a QR table: %v", err)
	}
	// Closing the table for QR stops new guests and new orders.
	if _, err := s.OpenForGuests(5, false, "", 0, at("18:30")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTable(qrCode(t, s, 5), at("18:31")); !errors.Is(err, ErrTableClosed) {
		t.Fatalf("joining a closed table: %v", err)
	}
	if _, err := s.GuestOrder(keys[0], []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, at("18:31")); !errors.Is(err, ErrTableClosed) {
		t.Fatalf("ordering at a closed table: %v", err)
	}
	// A wrong code finds nothing.
	if _, _, err := s.JoinTable("0123456789abcdef0123456789abcdef", at("18:31")); !errors.Is(err, ErrNoTable) {
		t.Fatalf("made-up code: %v", err)
	}
}

func TestPayForAllHoldsEveryone(t *testing.T) {
	s := newStore(t)
	b, keys := threeGuests(t, s)
	all, err := s.StartPayment(keys[1], true, PayHoldFor, at("19:00"))
	if err != nil {
		t.Fatal(err)
	}
	if all.Cents != 2340+1450+1150 || !all.All || all.Payer != 2 {
		t.Fatalf("attempt %+v", all)
	}
	// Nobody else can pay anything, the staff can't either, and nothing
	// held can change.
	var busy *BusyError
	if _, err := s.StartPayment(keys[0], false, PayHoldFor, at("19:00")); !errors.As(err, &busy) || !busy.All || busy.Payer != 2 {
		t.Fatalf("guest 1 paying their part during pay-for-all: %v", err)
	}
	if _, err := s.StartPayment(keys[2], true, PayHoldFor, at("19:00")); !errors.Is(err, ErrBusy) {
		t.Fatalf("second pay-for-all: %v", err)
	}
	if _, err := s.PayShare(b.ID, 3, PaidCard, at("19:00")); !errors.Is(err, ErrBusy) {
		t.Fatalf("staff taking a card payment for a held part: %v", err)
	}
	if err := s.UpdateLine(b.ID, b.Lines[0].ID, 100, 1, ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("changing a held price: %v", err)
	}
	if _, err := s.GuestOrder(keys[0], []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, at("19:00")); !errors.Is(err, ErrBusy) {
		t.Fatalf("ordering while the table is being paid: %v", err)
	}
	// It goes through: everyone is paid, guest 2 paid for the others, the
	// table is free. A second webhook for the same payment changes nothing.
	closed, err := s.AttemptPaid(all.Ref, "pi_1", at("19:01"))
	if err != nil || !closed {
		t.Fatalf("paid: closed=%v err=%v", closed, err)
	}
	if _, err := s.AttemptPaid(all.Ref, "pi_1", at("19:02")); err != nil {
		t.Fatal(err)
	}
	got, _ := s.BillByID(b.ID)
	if got.Open() || got.QROpen {
		t.Fatal("table should be free")
	}
	for _, sh := range got.Split {
		if sh.PaidAt == nil || sh.PaidBy != PaidOnline || sh.Payer != 2 {
			t.Fatalf("guest %d: %+v", sh.Guest, sh)
		}
	}
	card, cash, online := got.PaidWith()
	if card != 0 || cash != 0 || online != all.Cents {
		t.Fatalf("paid with %d/%d/%d", card, cash, online)
	}
	if err := s.UndoShare(b.ID, 1); !errors.Is(err, ErrBillClosed) {
		t.Fatalf("undo after closing: %v", err)
	}
}

func TestPayMyPartBlocksPayForAll(t *testing.T) {
	s := newStore(t)
	b, keys := threeGuests(t, s)
	mine, err := s.StartPayment(keys[0], false, PayHoldFor, at("19:00"))
	if err != nil || mine.Cents != 2340 {
		t.Fatalf("%+v %v", mine, err)
	}
	if _, err := s.StartPayment(keys[1], true, PayHoldFor, at("19:00")); !errors.Is(err, ErrBusy) {
		t.Fatalf("pay-for-all while guest 1 pays: %v", err)
	}
	// Guest 3 can pay their own part at the same time.
	third, err := s.StartPayment(keys[2], false, PayHoldFor, at("19:00"))
	if err != nil {
		t.Fatal(err)
	}
	// Guest 1 gives up: the hold lifts at once.
	if err := s.ReleaseAttempt(mine.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttemptPaid(third.Ref, "pi_3", at("19:01")); err != nil {
		t.Fatal(err)
	}
	// Now pay-for-all covers guests 1 and 2 only.
	rest, err := s.StartPayment(keys[1], true, PayHoldFor, at("19:02"))
	if err != nil {
		t.Fatal(err)
	}
	if rest.Cents != 2340+1450 || len(rest.Guests) != 2 {
		t.Fatalf("rest %+v", rest)
	}
	closed, err := s.AttemptPaid(rest.Ref, "pi_2", at("19:03"))
	if err != nil || !closed {
		t.Fatalf("closed=%v err=%v", closed, err)
	}
	got, _ := s.BillByID(b.ID)
	if got.Split[2].Payer != 0 || got.Split[0].Payer != 2 {
		t.Fatalf("payers %+v", got.Split)
	}
	// Online payments aren't undone with a tap (the money is at Stripe).
	_ = got
}

func TestHoldRunsOut(t *testing.T) {
	s := newStore(t)
	_, keys := threeGuests(t, s)
	all, err := s.StartPayment(keys[0], true, PayHoldFor, at("19:00"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPayment(keys[1], false, PayHoldFor, at("19:02")); !errors.Is(err, ErrBusy) {
		t.Fatalf("still held after 2 minutes: %v", err)
	}
	// No payment page (demo): after 3 minutes the hold just ends.
	if _, err := s.StartPayment(keys[1], false, PayHoldFor, at("19:03")); err != nil {
		t.Fatalf("hold should have ended: %v", err)
	}
	a, _ := s.AttemptByRef(all.Ref)
	if a.Pending() {
		t.Fatal("old hold still pending")
	}
}

func TestRunOutHoldWithStripePageMustBeCancelledFirst(t *testing.T) {
	s := newStore(t)
	_, keys := threeGuests(t, s)
	all, _ := s.StartPayment(keys[0], true, PayHoldFor, at("19:00"))
	if err := s.SetAttemptSession(all.Ref, "cs_test_1", "https://checkout.stripe.com/x"); err != nil {
		t.Fatal(err)
	}
	// Its time ran out, but the Stripe page could still be paid: the new
	// payment has to wait until that page is cancelled.
	var stale *StaleHoldError
	if _, err := s.StartPayment(keys[1], false, PayHoldFor, at("19:04")); !errors.As(err, &stale) || stale.Refs[0] != all.Ref {
		t.Fatalf("want the old page cancelled first: %v", err)
	}
	if got := s.AttemptsToCancel(at("19:04")); len(got) != 1 || got[0].SessionID != "cs_test_1" {
		t.Fatalf("to cancel: %+v", got)
	}
	if err := s.ReleaseAttempt(all.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPayment(keys[1], false, PayHoldFor, at("19:04")); err != nil {
		t.Fatal(err)
	}
}

// Every phone at the table presses "Pay for all" at the same moment: exactly
// one of them gets to pay.
func TestOnlyOnePayForAllAtOnce(t *testing.T) {
	s := newStore(t)
	s.Clock = func() time.Time { return at("19:00") }
	code := qrCode(t, s, 5)
	if _, err := s.OpenForGuests(5, true, "", 0, at("18:00")); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for i := 0; i < MaxGuests; i++ {
		_, g, err := s.JoinTable(code, at("18:01"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.GuestOrder(g.Key, []NewLine{{Name: "Pizza", PriceCents: 1450, Qty: 1}}, at("18:02")); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, g.Key)
	}
	if _, _, err := s.JoinTable(code, at("18:03")); !errors.Is(err, ErrTableFull) {
		t.Fatalf("13th guest: %v", err)
	}
	for round := 0; round < 20; round++ {
		var wg sync.WaitGroup
		var mu sync.Mutex
		won, busy := 0, 0
		var refs []string
		start := make(chan struct{})
		for i, k := range keys {
			wg.Add(1)
			go func(k string, all bool) {
				defer wg.Done()
				<-start
				a, err := s.StartPayment(k, all, PayHoldFor, at("19:00"))
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					won++
					refs = append(refs, a.Ref)
				case errors.Is(err, ErrBusy):
					busy++
				default:
					t.Errorf("unexpected: %v", err)
				}
			}(k, i%2 == 0) // half pay for all, half for themselves
		}
		close(start)
		wg.Wait()
		// Either one pay-for-all won (and all 11 others are busy), or only
		// pay-my-part ones won: never two holds on the same part.
		held := map[int]int{}
		for _, ref := range refs {
			a, _ := s.AttemptByRef(ref)
			for _, g := range a.Guests {
				held[g]++
			}
		}
		for g, n := range held {
			if n > 1 {
				t.Fatalf("round %d: guest %d held %d times", round, g, n)
			}
		}
		if won+busy != len(keys) {
			t.Fatalf("round %d: %d won + %d busy", round, won, busy)
		}
		for _, ref := range refs {
			_ = s.ReleaseAttempt(ref)
		}
	}
}

func TestGuestWhoHadNothingDoesntKeepTableOpen(t *testing.T) {
	s := newStore(t)
	b, keys := threeGuests(t, s)
	_, idle, err := s.JoinTable(qrCode(t, s, 5), at("18:10")) // scanned, ordered nothing
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPayment(idle.Key, false, PayHoldFor, at("19:00")); !errors.Is(err, ErrNothingToPay) {
		t.Fatalf("paying nothing: %v", err)
	}
	all, err := s.StartPayment(keys[2], true, PayHoldFor, at("19:00"))
	if err != nil {
		t.Fatal(err)
	}
	if closed, err := s.AttemptPaid(all.Ref, "pi", at("19:01")); err != nil || !closed {
		t.Fatalf("closed=%v err=%v", closed, err)
	}
	got, _ := s.BillByID(b.ID)
	if sh := got.Split[3]; sh.PaidBy != PaidNothing {
		t.Fatalf("idle guest: %+v", sh)
	}
}

func TestPayForAllTakesWhatNobodyHad(t *testing.T) {
	s := newStore(t)
	b, keys := threeGuests(t, s)
	// The waiter adds a bottle of water for the table: nobody's yet.
	if _, _, err := s.AddToBill(5, b.ID, []NewLine{{Name: "Vatten", PriceCents: 400, Qty: 1, Drink: true}}, "", 0, at("18:20")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPayment(keys[0], false, PayHoldFor, at("19:00")); err != nil {
		t.Fatal(err) // paying your own part is fine meanwhile
	}
	b2, _ := s.OpenBill(5)
	for _, a := range s.Holds(b2.ID, at("19:00")) {
		_ = s.ReleaseAttempt(a.Ref)
	}
	all, err := s.StartPayment(keys[1], true, PayHoldFor, at("19:00"))
	if err != nil {
		t.Fatal(err)
	}
	if all.Cents != 2340+1450+1150+400 || len(all.Lines) != 1 {
		t.Fatalf("pay-for-all %+v", all)
	}
	if err := s.AssignLine(b.ID, all.Lines[0], []int{1}); !errors.Is(err, ErrBusy) {
		t.Fatalf("giving away a held line: %v", err)
	}
	closed, err := s.AttemptPaid(all.Ref, "pi", at("19:01"))
	if err != nil || !closed {
		t.Fatalf("closed=%v err=%v", closed, err)
	}
	got, _ := s.BillByID(b.ID)
	pay, _, _ := got.Parts()
	if sum(pay) != got.ToPay() || pay[1] != 1450+400 {
		t.Fatalf("parts %v (bill %d)", pay, got.ToPay())
	}
}

func TestSmallAmountsArePaidAtTheCounter(t *testing.T) {
	s := newStore(t)
	s.Clock = func() time.Time { return at("19:00") }
	if _, err := s.OpenForGuests(5, true, "", 0, at("18:00")); err != nil {
		t.Fatal(err)
	}
	_, g, _ := s.JoinTable(qrCode(t, s, 5), at("18:01"))
	_, _ = s.GuestOrder(g.Key, []NewLine{{Name: "Godis", PriceCents: 40, Qty: 1, Drink: true}}, at("18:02"))
	if _, err := s.StartPayment(g.Key, false, PayHoldFor, at("19:00")); !errors.Is(err, ErrTooSmall) {
		t.Fatalf("40 cents online: %v", err)
	}
}

func TestNewTableQRStopsOldSticker(t *testing.T) {
	s := newStore(t)
	old := qrCode(t, s, 3)
	if _, err := s.OpenForGuests(3, true, "", 0, at("18:00")); err != nil {
		t.Fatal(err)
	}
	q, err := s.NewTableQR(3)
	if err != nil || q.Code == old {
		t.Fatalf("%+v %v", q, err)
	}
	if _, _, err := s.JoinTable(old, at("18:01")); !errors.Is(err, ErrNoTable) {
		t.Fatalf("old sticker: %v", err)
	}
	if _, _, err := s.JoinTable(q.Code, at("18:01")); err != nil {
		t.Fatal(err)
	}
}

func TestOldGuestNamesAreRemoved(t *testing.T) {
	s := newStore(t)
	b, keys := threeGuests(t, s)
	_ = s.SetGuestName(keys[0], "Anna")
	all, _ := s.StartPayment(keys[0], true, PayHoldFor, at("19:00"))
	if _, err := s.AttemptPaid(all.Ref, "pi", at("19:01")); err != nil {
		t.Fatal(err)
	}
	day := b.OpenedAt.AddDate(0, 0, 1).Format("2006-01-02")
	if _, err := s.Anonymize(day); err != nil {
		t.Fatal(err)
	}
	got, _ := s.BillByID(b.ID)
	for _, sh := range got.Split {
		if sh.Name != "" || sh.Key != "" {
			t.Fatalf("guest %d still has %q / %q", sh.Guest, sh.Name, sh.Key)
		}
	}
	if _, ok := s.AttemptByRef(all.Ref); ok {
		t.Fatal("old payment attempt kept")
	}
	if got.PaidAt == nil || got.Split[0].PaidBy != PaidOnline {
		t.Fatal("the money side must stay")
	}
}

func TestStaffAddsForOneGuest(t *testing.T) {
	s := newStore(t)
	b, keys := threeGuests(t, s)
	// Guest 3 asks the waiter for a coffee instead of using their phone.
	if _, _, err := s.AddForGuest(b.ID, 3, []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, at("18:30")); err != nil {
		t.Fatal(err)
	}
	got, _ := s.BillByID(b.ID)
	pay, _, _ := got.Parts()
	if pay[2] != 1150+300 || len(got.Unassigned()) != 0 {
		t.Fatalf("parts %v", pay)
	}
	// The waiter brings it: not on the "to bring" list.
	if n := len(s.DrinksToBring()); n != 1 {
		t.Fatalf("%d drink tickets, want only guest 1's soda", n)
	}
	// Food for a guest goes to the chef with their number.
	_, tk, _ := s.AddForGuest(b.ID, 2, []NewLine{{Name: "Sorbet", PriceCents: 750, Qty: 1}}, at("18:31"))
	if tk == nil || tk.Items[0].Guest != 2 {
		t.Fatalf("ticket %+v", tk)
	}
	// Not for a guest who paid, nor one paying right now.
	all, _ := s.StartPayment(keys[0], false, PayHoldFor, at("19:00"))
	if _, _, err := s.AddForGuest(b.ID, 1, []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, at("19:00")); !errors.Is(err, ErrBusy) {
		t.Fatalf("adding to a part being paid: %v", err)
	}
	_, _ = s.AttemptPaid(all.Ref, "pi", at("19:01"))
	if _, _, err := s.AddForGuest(b.ID, 1, []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, at("19:02")); !errors.Is(err, ErrSharePaid) {
		t.Fatalf("adding to a paid part: %v", err)
	}
	if _, _, err := s.AddForGuest(b.ID, 9, []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1}}, at("19:02")); !errors.Is(err, ErrBadGuest) {
		t.Fatalf("guest 9: %v", err)
	}
}

func TestReceiptRequests(t *testing.T) {
	s := newStore(t)
	b, keys := threeGuests(t, s)
	if err := s.AskPaperReceipt(keys[0], at("19:00")); !errors.Is(err, ErrNotPaidYet) {
		t.Fatalf("receipt before paying: %v", err)
	}
	if _, _, err := s.UseReceiptEmail(keys[0]); !errors.Is(err, ErrNotPaidYet) {
		t.Fatalf("email before paying: %v", err)
	}
	a, _ := s.StartPayment(keys[1], true, PayHoldFor, at("19:00"))
	_, _ = s.AttemptPaid(a.Ref, "pi", at("19:01"))
	// Guest 1 was paid for; they still get their own receipt.
	if err := s.AskPaperReceipt(keys[0], at("19:02")); err != nil {
		t.Fatal(err)
	}
	_ = s.AskPaperReceipt(keys[0], at("19:03")) // pressed twice: still one
	list := s.ReceiptsToPrint()
	if len(list) != 1 || list[0].Guest != 1 || list[0].Table != 5 {
		t.Fatalf("to print %+v", list)
	}
	if err := s.ReceiptGiven(b.ID, 1, at("19:05")); err != nil {
		t.Fatal(err)
	}
	if len(s.ReceiptsToPrint()) != 0 {
		t.Fatal("still waiting after it was given")
	}
	for i := 0; i < MaxReceiptEmails; i++ {
		if _, g, err := s.UseReceiptEmail(keys[2]); err != nil || g != 3 {
			t.Fatalf("email %d: %d %v", i, g, err)
		}
	}
	if _, _, err := s.UseReceiptEmail(keys[2]); !errors.Is(err, ErrTooManyMails) {
		t.Fatalf("4th email: %v", err)
	}
}

func TestTablesOpenForQRByThemselves(t *testing.T) {
	s := newStore(t)
	s.Clock = func() time.Time { return at("19:00") }
	code := qrCode(t, s, 4)
	// Guests with a 20 € deposit sit down at table 4: QR orders are on.
	opened, err := s.OpenOnArrival(4, "ref-anna", 2000, at("18:00"))
	if err != nil || !opened {
		t.Fatalf("opened=%v err=%v", opened, err)
	}
	b, _ := s.OpenBill(4)
	if !b.QROpen || b.DepositCents != 2000 || b.BookingRef != "ref-anna" {
		t.Fatalf("bill %+v", b)
	}
	if _, _, err := s.JoinTable(code, at("18:01")); err != nil {
		t.Fatalf("joining after arrival: %v", err)
	}
	// Staff taking the first order at a table opens it too.
	b2, _, err := s.AddToBill(2, 0, []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, "", 0, at("18:05"))
	if err != nil || !b2.QROpen {
		t.Fatalf("first order: %+v %v", b2, err)
	}
	// ...and staff can still un-split it while no phone has joined.
	_ = s.SplitBill(b2.ID, 2)
	if err := s.SplitBill(b2.ID, 1); err != nil {
		t.Fatalf("un-split without phones: %v", err)
	}
	// New guests arriving at a table whose bill still has orders on it are
	// not put on the old bill.
	if opened, _ := s.OpenOnArrival(2, "ref-new", 1000, at("18:10")); opened {
		t.Fatal("re-opened someone else's bill")
	}
	b2, _ = s.OpenBill(2)
	if b2.BookingRef == "ref-new" {
		t.Fatal("the new guests' deposit went on the old bill")
	}
	// A table opened where nobody ordered is gone the next day.
	if _, err := s.OpenOnArrival(6, "", 0, at("21:00")); err != nil {
		t.Fatal(err)
	}
	if err := s.TidyEmptyBills(at("21:00").Add(12 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, open := s.OpenBill(6); open {
		t.Fatal("empty bill left from yesterday")
	}
	if _, open := s.OpenBill(2); !open {
		t.Fatal("a bill with orders on it must stay")
	}
}

func TestCallTheWaiterFromThePhone(t *testing.T) {
	s := newStore(t)
	_, keys := threeGuests(t, s)
	_ = s.SetGuestName(keys[1], "Bo")
	// No deal: a call is "please come", with who called.
	res, err := s.CallFromPhone(keys[1], false, at("18:40"))
	if err != nil || res.Kind != PressWaiter {
		t.Fatalf("%+v %v", res, err)
	}
	c := s.TablesNow()[4].Call
	if c == nil || !c.Waiter || c.Guest != 2 || c.Name != "Bo" {
		t.Fatalf("call %+v", c)
	}
	if err := s.Answer(5); err != nil || s.TablesNow()[4].Call != nil {
		t.Fatalf("answer: %v", err)
	}

	// During 3 meals: "ready for meal 2" works like the table button...
	if err := s.StartDeal(5, at("18:45")); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.CallFromPhone(keys[0], false, at("19:10")); res.Kind != PressNext || res.Meal != 1 {
		t.Fatalf("ready for the next meal: %+v", res)
	}
	tn := s.TablesNow()[4]
	if !tn.Run.Waiting() || tn.Call == nil || tn.Call.Waiter {
		t.Fatalf("after ready: %+v %+v", tn.Run, tn.Call)
	}
	if err := s.ServeNext(5, 2, at("19:12")); err != nil {
		t.Fatal(err)
	}
	// ...and "call the waiter" during the deal is a real call, answered with Done.
	if _, err := s.CallFromPhone(keys[2], true, at("19:20")); err != nil {
		t.Fatal(err)
	}
	if c := s.TablesNow()[4].Call; c == nil || !c.Waiter || s.TablesNow()[4].Run.Waiting() {
		t.Fatalf("waiter call during the deal: %+v", c)
	}
	if err := s.Answer(5); err != nil {
		t.Fatalf("answering a waiter call during the deal: %v", err)
	}
}
