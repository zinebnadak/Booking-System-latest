package db

import (
	"errors"
	"testing"
	"time"
)

// Two guests at table 4: guest 1 had 35 €, guest 2 had 12 €.
func splitTwo(t *testing.T, s *Store, now time.Time) Bill {
	t.Helper()
	b, _, err := s.AddToBill(4, 0, []NewLine{
		{Name: "Oxfilé", PriceCents: 3500, Qty: 1, NoKitchen: true},
		{Name: "Lunch", PriceCents: 1200, Qty: 1, NoKitchen: true},
	}, "", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SplitBill(b.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignLine(b.ID, b.Lines[0].ID, []int{1}); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignLine(b.ID, b.Lines[1].ID, []int{2}); err != nil {
		t.Fatal(err)
	}
	b, _ = s.BillByID(b.ID)
	return b
}

func TestGuestPaysPartWithGiftCard(t *testing.T) {
	s := newStore(t)
	now := at("13:00")
	s.Clock = func() time.Time { return now }
	b := splitTwo(t, s, now)
	gift, _ := s.SellGiftCard(2000, PaidCash, "Lisa", "", now)

	// Guest 1 gives a 20 € gift card for their 35 €.
	out, closed, err := s.AddSharePayment(b.ID, 1, Payment{Method: PaidGiftCard, Code: gift.Code}, now)
	if err != nil || closed {
		t.Fatalf("gift card: %v %v", closed, err)
	}
	if left := out.ShareLeft(1); left != 1500 || out.Split[0].PaidAt != nil || out.Split[0].PartPaid() != 2000 {
		t.Fatalf("after the gift card: left %d %+v", left, out.Split[0])
	}
	if v, _ := s.VoucherByCode(gift.Code); v.Cents != 0 {
		t.Fatalf("card after use: %d", v.Cents)
	}
	// The same card again: nothing left on it.
	if _, _, err := s.AddSharePayment(b.ID, 1, Payment{Method: PaidGiftCard, Code: gift.Code}, now); !errors.Is(err, ErrVoucherUsed) {
		t.Fatalf("used card: %v", err)
	}

	// Guest 1's dishes can't change now, the bill can't be voided, and they
	// can't be paid for together with guest 2.
	if err := s.UpdateLine(b.ID, b.Lines[0].ID, 100, 1, ""); !errors.Is(err, ErrSharePaid) {
		t.Fatalf("changing a part-paid guest's dish: %v", err)
	}
	if err := s.VoidBill(b.ID); !errors.Is(err, ErrSharePaid) {
		t.Fatalf("void: %v", err)
	}
	if _, err := s.PayShares(b.ID, []int{1, 2}, PaidCard, now); !errors.Is(err, ErrPartlyPaid) {
		t.Fatalf("together: %v", err)
	}
	// Too much in cash.
	if _, _, err := s.AddSharePayment(b.ID, 1, Payment{Method: PaidCash, Cents: 1600}, now); !errors.Is(err, ErrBadAmount) {
		t.Fatalf("more than left: %v", err)
	}

	// Typed by mistake: take it back, the card gets its 20 € back.
	if p, err := s.UndoSharePart(b.ID, 1); err != nil || p.Cents != 2000 {
		t.Fatalf("undo part: %+v %v", p, err)
	}
	if v, _ := s.VoucherByCode(gift.Code); v.Cents != 2000 {
		t.Fatalf("card after undo: %d", v.Cents)
	}
	if bb, _ := s.BillByID(b.ID); bb.ShareLeft(1) != 3500 || bb.AnySharePaid() {
		t.Fatalf("after undo: left %d", bb.ShareLeft(1))
	}

	// Again, then the rest at the terminal: paid, in several ways.
	s.AddSharePayment(b.ID, 1, Payment{Method: PaidGiftCard, Code: gift.Code}, now)
	if closed, err := s.PayShare(b.ID, 1, PaidCard, now); err != nil || closed {
		t.Fatalf("rest at the terminal: %v %v", closed, err)
	}
	bb, _ := s.BillByID(b.ID)
	sh := bb.Split[0]
	if sh.PaidAt == nil || sh.PaidBy != PaidMixed || len(sh.Payments) != 2 || sh.Payments[1].Cents != 1500 || sh.Payments[1].Method != PaidCard {
		t.Fatalf("guest 1 paid: %+v", sh)
	}

	// Undo the terminal part: guest 1 owes 15 € again, the gift card stays used.
	if err := s.UndoShare(b.ID, 1); err != nil {
		t.Fatal(err)
	}
	if bb, _ := s.BillByID(b.ID); bb.ShareLeft(1) != 1500 || len(bb.Split[0].Payments) != 1 {
		t.Fatalf("after undo: left %d, parts %v", bb.ShareLeft(1), bb.Split[0].Payments)
	}
	if _, err := s.PayShare(b.ID, 1, PaidCash, now); err != nil {
		t.Fatal(err)
	}

	// Guest 2: a 50 € card for their 12 €: it pays it all, 38 € stay on it.
	big, _ := s.SellGiftCard(5000, PaidCard, "Lisa", "", now)
	out, closed, err = s.AddSharePayment(b.ID, 2, Payment{Method: PaidGiftCard, Code: big.Code}, now)
	if err != nil || !closed || out.PaidAt == nil {
		t.Fatalf("guest 2: closed %v err %v", closed, err)
	}
	if v, _ := s.VoucherByCode(big.Code); v.Cents != 3800 {
		t.Fatalf("big card left: %d", v.Cents)
	}
	if out.Split[1].PaidBy != PaidGiftCard {
		t.Fatalf("guest 2 paid by %q", out.Split[1].PaidBy)
	}
	tk := out.Takings()
	if tk[PaidGiftCard] != 3200 || tk[PaidCash] != 1500 || tk[PaidCard] != 0 {
		t.Fatalf("takings %v", tk)
	}
}

func TestSharePartChecks(t *testing.T) {
	s := newStore(t)
	now := at("13:00")
	s.Clock = func() time.Time { return now }
	b := splitTwo(t, s, now)

	// Not split: use the bill's own part payments.
	plain, _, _ := s.AddToBill(5, 0, []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, "", 0, now)
	if _, _, err := s.AddSharePayment(plain.ID, 1, Payment{Method: PaidCash, Cents: 100}, now); !errors.Is(err, ErrNotSplit) {
		t.Fatalf("not split: %v", err)
	}
	if _, _, err := s.AddSharePayment(b.ID, 9, Payment{Method: PaidCash, Cents: 100}, now); !errors.Is(err, ErrBadGuest) {
		t.Fatalf("no guest 9: %v", err)
	}
	if _, _, err := s.AddSharePayment(b.ID, 1, Payment{Method: PaidGiftCard, Code: "PK-0000-0000"}, now); !errors.Is(err, ErrNoVoucher) {
		t.Fatalf("unknown card: %v", err)
	}
	if _, _, err := s.AddSharePayment(b.ID, 1, Payment{Method: "bitcoin", Cents: 100}, now); !errors.Is(err, ErrBadMethod) {
		t.Fatalf("unknown way: %v", err)
	}
	if _, err := s.UndoSharePart(b.ID, 1); !errors.Is(err, ErrNothingLeft) {
		t.Fatalf("nothing to undo: %v", err)
	}
	// A lunch coupon pays one lunch (up to 12 €) of guest 2's 12 €.
	booklet, _ := s.SellLunchBooklet(10, 1200, 10800, PaidCard, "Lisa", "", now)
	out, _, err := s.AddSharePayment(b.ID, 2, Payment{Method: PaidLunchCoupon, Code: booklet.Code, Coupons: 1}, now)
	if err != nil || out.Split[1].PaidAt == nil || out.Split[1].PaidBy != PaidLunchCoupon {
		t.Fatalf("coupon: %+v %v", out.Split[1], err)
	}
	if v, _ := s.VoucherByCode(booklet.Code); v.Left != 9 {
		t.Fatalf("coupons left: %d", v.Left)
	}
	// Paid guests take no more part payments.
	if _, _, err := s.AddSharePayment(b.ID, 2, Payment{Method: PaidCash, Cents: 100}, now); !errors.Is(err, ErrSharePaid) {
		t.Fatalf("paid guest: %v", err)
	}
	if _, err := s.UndoSharePart(b.ID, 2); !errors.Is(err, ErrSharePaid) {
		t.Fatalf("undo part of a paid guest: %v", err)
	}
	// Undo on the paid guest gives the coupon back.
	if err := s.UndoShare(b.ID, 2); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.VoucherByCode(booklet.Code); v.Left != 10 {
		t.Fatalf("coupons after undo: %d", v.Left)
	}
}

func TestPayTheWholeTable(t *testing.T) {
	s := newStore(t)
	now := at("13:00")
	s.Clock = func() time.Time { return now }
	b := splitTwo(t, s, now) // guest 1: 35 €, guest 2: 12 €
	// Not split: no.
	plain, _, _ := s.AddToBill(5, 0, []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 1, Drink: true}}, "", 0, now)
	if _, err := s.PayTable(plain.ID, PaidCard, now); !errors.Is(err, ErrNotSplit) {
		t.Fatalf("not split: %v", err)
	}
	// Guest 2 pays; then the waiter adds dessert for the table (nobody's).
	s.PayShare(b.ID, 2, PaidCash, now)
	s.AddToBill(4, b.ID, []NewLine{{Name: "Dessert", PriceCents: 900, Qty: 1, NoKitchen: true}}, "", 0, now)
	bb, _ := s.BillByID(b.ID)
	if bb.TableLeft() != 3500+900 {
		t.Fatalf("left %d", bb.TableLeft())
	}
	// Guest 1 gave a gift card for part: then the whole table waits for the rest of theirs.
	gift, _ := s.SellGiftCard(1000, PaidCash, "x", "", now)
	if _, _, err := s.AddSharePayment(b.ID, 1, Payment{Method: PaidGiftCard, Code: gift.Code}, now); !errors.Is(err, ErrUnassigned) {
		t.Fatalf("part with something nobody's: %v", err)
	}
	cents, err := s.PayTable(b.ID, PaidCard, now)
	if err != nil || cents != 4400 {
		t.Fatalf("pay table: %d %v", cents, err)
	}
	bb, _ = s.BillByID(b.ID)
	if bb.PaidAt == nil || bb.Takings()[PaidCard] != 4400 || bb.Takings()[PaidCash] != 1200 {
		t.Fatalf("closed %v takings %v", bb.PaidAt, bb.Takings())
	}

	// Both guests paid, but the waiter added something for the table
	// meanwhile: it goes on a new guest (the table), and the bill closes.
	b2 := splitTwo(t, s, now)
	s.AddToBill(4, b2.ID, []NewLine{{Name: "Kaffe", PriceCents: 300, Qty: 2, Drink: true}}, "", 0, now)
	if _, err := s.PayShares(b2.ID, []int{1, 2}, PaidCard, now); !errors.Is(err, ErrUnassigned) {
		t.Fatalf("guests with something nobody's: %v", err)
	}
	cents, err = s.PayTable(b2.ID, PaidCash, now)
	bb, _ = s.BillByID(b2.ID)
	if err != nil || cents != 3500+1200+600 || bb.PaidAt == nil || len(bb.Split) != 2 {
		t.Fatalf("whole table b2: %d %v %+v", cents, err, bb.Split)
	}
}
