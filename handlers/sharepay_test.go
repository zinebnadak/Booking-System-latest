package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"ozmo-booking/db"
)

// Two guests ordered on their phones at table 5. Anna pays part of hers with
// a gift card at the waiter, then the rest at the terminal.
func TestSplitGuestPaysWithGiftCard(t *testing.T) {
	e := newEnv(t, nil)
	code := tableCode(t, e, 5)
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	a, b := join(t, e, code, 1), join(t, e, code, 2)
	lax, soda, burger := menuID(t, e, "Laxfilé"), menuID(t, e, "Läsk 33 cl"), menuID(t, e, "Ozmo-burgare")
	a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + lax: {"1"}, "qty_" + soda: {"2"}}) // 26,90 + 7,00
	b.do("POST", "/g/"+b.key+"/order", url.Values{"qty_" + burger: {"1"}})                    // 19,90
	bill, _ := e.store.OpenBill(5)
	id := fmt.Sprint(bill.ID)
	gift, err := e.store.SellGiftCard(2000, db.PaidCash, "admin", "", e.srv.Now())
	if err != nil {
		t.Fatal(err)
	}

	e.expect(e.do("GET", "/admin/orders/5/split", nil, true), 200, "Gift card, lunch coupons or part of it", "Code on the card")

	// The gift card, typed in lower case.
	part := url.Values{"bill": {id}, "guest": {"1"}, "method": {"giftcard"}, "code": {strings.ToLower(gift.Code)}}
	location(t, e.do("POST", "/admin/orders/5/split/part", part, true), "msg=guestpart")
	e.expect(e.do("GET", "/admin/orders/5/split", nil, true), 200, "Left to pay", "13,90", gift.Code)
	e.expect(e.do("GET", "/admin/activity", nil, true), 200, "paid with a gift card", "Guest 1 · 20,00")

	// Anna's phone says what's left, and she can't pay it there.
	page := a.do("GET", "/g/"+a.key, nil).Body.String()
	if !strings.Contains(page, "paid 20,00") || !strings.Contains(page, "13,90") || strings.Contains(page, "Pay my part") {
		t.Fatal("Anna's phone should show the part paid and no Pay button")
	}
	location(t, a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"0"}}), "msg=withwaiter")
	// The other guest still pays their own on the phone, but not for everyone.
	other := b.do("GET", "/g/"+b.key, nil).Body.String()
	if !strings.Contains(other, "Pay my part") || strings.Contains(other, "Pay for the whole table") {
		t.Fatal("guest 2: own part yes, whole table no")
	}

	// Not together with guest 2: Anna pays the rest of hers on her own.
	location(t, e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {id}, "guest": {"1", "2"}, "method": {"card"}}, true), "msg=guestpartly")

	// Typed on the wrong guest? Take it back: the card has its 20 € again.
	location(t, e.do("POST", "/admin/orders/5/split/undopart", url.Values{"bill": {id}, "guest": {"1"}}, true), "msg=partundone")
	if v, _ := e.store.VoucherByCode(gift.Code); v.Cents != 2000 {
		t.Fatalf("card after taking it back: %d", v.Cents)
	}
	location(t, e.do("POST", "/admin/orders/5/split/part", part, true), "msg=guestpart")

	// A wrong code, a bad amount.
	location(t, e.do("POST", "/admin/orders/5/split/part", url.Values{"bill": {id}, "guest": {"1"}, "method": {"giftcard"}, "code": {"PK-0000-0000"}}, true), "msg=nocard")
	location(t, e.do("POST", "/admin/orders/5/split/part", url.Values{"bill": {id}, "guest": {"1"}, "method": {"cash"}, "amount": {"99"}}, true), "msg=badamount")
	location(t, e.do("POST", "/admin/orders/5/split/part", url.Values{"bill": {id}, "guest": {"1"}, "method": {"cash"}, "amount": {"abc"}}, true), "msg=badamount")

	// The rest at the terminal.
	location(t, e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {id}, "guest": {"1"}, "method": {"card"}}, true), "msg=guestpaid")
	after, _ := e.store.BillByID(bill.ID)
	if sh := after.Split[0]; sh.PaidBy != db.PaidMixed || len(sh.Payments) != 2 || sh.Payments[1].Cents != 1390 {
		t.Fatalf("Anna paid: %+v", sh)
	}
	// Her receipt lists both.
	e.expect(e.do("GET", "/r/"+after.Split[0].Ref, nil, false), 200, gift.Code, "20,00", "13,90")

	// Guest 2 pays; the table is free. The day's money: 20 € on a gift card.
	location(t, e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {id}, "guest": {"2"}, "method": {"cash"}}, true), "msg=allpaid")
	done, _ := e.store.BillByID(bill.ID)
	if tk := done.Takings(); tk[db.PaidGiftCard] != 2000 || tk[db.PaidCard] != 1390 || tk[db.PaidCash] != 1990 {
		t.Fatalf("takings %v", tk)
	}
}
