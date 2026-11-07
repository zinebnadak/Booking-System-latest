package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"emeal-booking/db"
)

// The old till's features, at a table: courses, a kitchen message, a
// discount, a gift card and a lunch card paying part of the bill.
func TestTillFeaturesAtATable(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	soup, salmon, cake, wine := e.menuItem("Fisksoppa"), e.menuItem("Laxfilé"), e.menuItem("Chokladfondant"), e.menuItem("Husets vin, glas")
	id := func(m db.MenuItem) string { return fmt.Sprint(m.ID) }

	// Starter, main and dessert in one go with "Vänta varmt": only the soup goes now.
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{
		"bill": {"0"}, "hold": {"1"}, "qty_" + id(soup): {"2"}, "qty_" + id(salmon): {"2"}, "qty_" + id(cake): {"1"}, "qty_" + id(wine): {"2"},
	}, true), "msg=addedheld")
	k := e.do("GET", "/admin/kitchen?part=tickets", nil, true).Body.String()
	if !strings.Contains(k, "Waiting for the go") || !strings.Contains(k, "Laxfilé") {
		t.Fatal("the kitchen should see the held mains greyed out")
	}
	bill, _ := e.store.OpenBill(5)
	bid := fmt.Sprint(bill.ID)
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "Mains now (Varma fram)", "Dessert now (Fram efterrätt)", "Waits for your go")
	location(t, e.do("POST", "/admin/orders/5/fire", url.Values{"bill": {bid}, "course": {"2"}}, true), "msg=firedmains")
	if c := e.store.HeldCourses(bill.ID); len(c) != 1 || c[0] != db.CourseDessert {
		t.Fatalf("still held: %v", c)
	}

	// A message to the kitchen.
	location(t, e.do("POST", "/admin/orders/5/message", url.Values{"bill": {bid}, "text": {"Gäst 2 är allergisk mot nötter"}}, true), "msg=messagesent")
	e.expect(e.do("GET", "/admin/kitchen?part=tickets", nil, true), 200, "Gäst 2 är allergisk mot nötter")

	// 10 % off the whole bill: 2×15,50 + 2×26,90 + 9,50 + 2×8,50 = 111,30 → 100,17.
	location(t, e.do("POST", "/admin/orders/5/discount", url.Values{"bill": {bid}, "pct": {"10"}}, true), "msg=discounted")
	bill, _ = e.store.OpenBill(5)
	if bill.ToPay() != 11130-bill.Discounts() || bill.Discounts() == 0 {
		t.Fatalf("after discount: to pay %d, discounts %d", bill.ToPay(), bill.Discounts())
	}

	// A 40 € gift card, sold at the till, pays part; Edenred 20 €; the rest at the terminal.
	location(t, e.do("POST", "/admin/giftcards", url.Values{"kind": {"gift"}, "amount": {"40"}, "method": {"cash"}}, true), "/print")
	cards := e.store.Vouchers(1)
	e.expect(e.do("GET", "/admin/giftcards/"+cards[0].Code+"/print", nil, true), 200, cards[0].Code, "40,00 €")
	location(t, e.do("POST", "/admin/orders/5/part", url.Values{"bill": {bid}, "method": {"giftcard"}, "code": {strings.ToLower(cards[0].Code)}}, true), "msg=partpaid")
	location(t, e.do("POST", "/admin/orders/5/part", url.Values{"bill": {bid}, "method": {"edenred"}, "amount": {"20,00"}}, true), "msg=partpaid")
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "Left to pay", "Rest at the terminal", cards[0].Code)
	location(t, e.do("POST", "/admin/orders/5/pay", url.Values{"bill": {bid}, "method": {"card"}}, true), "msg=billpaid")
	paid, _ := e.store.BillByID(bill.ID)
	tk := paid.Takings()
	if tk[db.PaidGiftCard] != 4000 || tk[db.PaidEdenred] != 2000 || tk[db.PaidCard] != paid.ToPay()-6000 {
		t.Fatalf("takings %v", tk)
	}
	day := e.do("GET", "/admin/orders", nil, true)
	e.expect(day, 200, "Gift card", "Edenred", "Gift cards and lunch booklets sold (1)")
	e.expect(e.do("GET", "/admin/activity", nil, true), 200, "paid with a gift card", "paid with a lunch card", "gave a discount on the whole bill", "sold a gift card")

	// The receipt has the VAT split, like the old till's.
	e.expect(e.do("GET", "/admin/orders/5/bill?bill="+bid, nil, true), 200, "25,50", "13,50", "Without VAT")
}
