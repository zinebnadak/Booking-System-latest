package db

import (
	"errors"
	"testing"
	"time"

	"ozmo-booking/schedule"
)

func TestPayInPartsWithGiftCardAndLunchCard(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 10, 30, 19, 0, 0, 0, schedule.Location)
	// The bill on the old till's receipt: 2× Öppen mat 35 €, 2 bottles of Malbec.
	b, _, _ := s.AddToBill(5, 0, []NewLine{
		{Name: "Öppen mat", PriceCents: 3500, Qty: 2, NoKitchen: true},
		{Name: "Cuma Malbec, flaska", PriceCents: 4200, Qty: 2, Drink: true},
	}, "", 0, now)
	if b.ToPay() != 15400 {
		t.Fatalf("to pay %d", b.ToPay())
	}
	gift, err := s.SellGiftCard(5000, PaidCash, "Lisa", "", now)
	if err != nil || gift.Cents != 5000 || len(gift.Code) != 12 {
		t.Fatalf("gift card %+v %v", gift, err)
	}
	// 50 € gift card first (typed in lower case, without dashes).
	lower := []rune(gift.Code)
	code := string(lower[0:2]) + " " + string(lower[3:7]) + string(lower[8:])
	out, closed, err := s.AddPayment(b.ID, Payment{Method: PaidGiftCard, Code: code}, now)
	if err != nil || closed || out.Left() != 15400-5000 {
		t.Fatalf("after gift card: left %d closed %v err %v", out.Left(), closed, err)
	}
	if v, _ := s.VoucherByCode(gift.Code); v.Cents != 0 || len(v.Uses) != 1 {
		t.Fatalf("gift card after use: %+v", v)
	}
	// The same card can't pay again.
	if _, _, err := s.AddPayment(b.ID, Payment{Method: PaidGiftCard, Code: gift.Code}, now); !errors.Is(err, ErrVoucherUsed) {
		t.Fatalf("used card again: %v", err)
	}
	// Changing the bill now is blocked (part of it is paid).
	if err := s.UpdateLine(b.ID, out.Lines[0].ID, 100, 2, ""); !errors.Is(err, ErrPartlyPaid) {
		t.Fatalf("change after part payment: %v", err)
	}
	// 30 € on Edenred, then the rest at the terminal.
	if _, _, err := s.AddPayment(b.ID, Payment{Method: PaidEdenred, Cents: 3000}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddPayment(b.ID, Payment{Method: PaidCard, Cents: 999999}, now); !errors.Is(err, ErrBadAmount) {
		t.Fatalf("too much: %v", err)
	}
	paid, err := s.PayBill(b.ID, PaidCard, now)
	if err != nil || paid.PaidAt == nil || paid.PaidBy != PaidMixed {
		t.Fatalf("pay the rest: %+v %v", paid, err)
	}
	tk := paid.Takings()
	if tk[PaidGiftCard] != 5000 || tk[PaidEdenred] != 3000 || tk[PaidCard] != 7400 {
		t.Fatalf("takings %v", tk)
	}
	if card, cash, online := paid.PaidWith(); card != 7400 || cash != 0 || online != 0 {
		t.Fatalf("paid with %d %d %d", card, cash, online)
	}
}

func TestLunchBookletAndUndo(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, schedule.Location)
	book, err := s.SellLunchBooklet(10, 1290, 11500, PaidCard, "Lisa", "Anna", now)
	if err != nil || book.Left != 10 || book.Value() != 12900 {
		t.Fatalf("booklet %+v %v", book, err)
	}
	b, _, _ := s.AddToBill(3, 0, []NewLine{{Name: "Dagens lunch", PriceCents: 1290, Qty: 2}}, "", 0, now)
	out, closed, err := s.AddPayment(b.ID, Payment{Method: PaidLunchCoupon, Code: book.Code, Coupons: 2}, now)
	if err != nil || !closed || out.PaidBy != PaidLunchCoupon {
		t.Fatalf("two lunches with two coupons: %+v %v %v", out, closed, err)
	}
	if v, _ := s.VoucherByCode(book.Code); v.Left != 8 {
		t.Fatalf("coupons left %d", v.Left)
	}
	// A gift card code doesn't work as a lunch coupon.
	gift, _ := s.SellGiftCard(2000, PaidCard, "", "", now)
	b2, _, _ := s.AddToBill(4, 0, []NewLine{{Name: "Dagens lunch", PriceCents: 1290, Qty: 1}}, "", 0, now)
	if _, _, err := s.AddPayment(b2.ID, Payment{Method: PaidLunchCoupon, Code: gift.Code}, now); !errors.Is(err, ErrNoVoucher) {
		t.Fatalf("gift card as coupon: %v", err)
	}
	// Part of a gift card, then taken back: the money is back on the card.
	if _, _, err := s.AddPayment(b2.ID, Payment{Method: PaidGiftCard, Code: gift.Code, Cents: 500}, now); err != nil {
		t.Fatal(err)
	}
	if p, err := s.UndoPayment(b2.ID, now); err != nil || p.Cents != 500 {
		t.Fatalf("undo: %+v %v", p, err)
	}
	if v, _ := s.VoucherByCode(gift.Code); v.Cents != 2000 || len(v.Uses) != 0 {
		t.Fatalf("after undo: %+v", v)
	}
	if err := s.VoidVoucher(gift.Code); err != nil {
		t.Fatal(err)
	}
	if sold := s.VouchersSoldOn("2026-10-30"); len(sold) != 1 || sold[0].Kind != VoucherLunch {
		t.Fatalf("sold today: %+v", sold)
	}
}
