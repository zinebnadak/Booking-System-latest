package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// One lunch, start to finish, through the screens staff use: an online
// booking with a deposit arrives, a waiter counts the house wine, takes the
// order, splits the bill, one guest pays part with a gift card bought that
// day, a walk-in gets the last glass of wine. Then the end-of-day report
// must match what was paid, way by way.
func TestAWholeLunch(t *testing.T) {
	wrongPasswordDelay = 0
	e := newEnv(t, nil)
	wine, burger := e.menuItem("Husets vin, glas"), e.menuItem("E-Meal-burgare")
	qty := func(m db.MenuItem) string { return fmt.Sprintf("qty_%d", m.ID) }

	// 1. An online booking for 4 at 12:00 with a 20 € deposit. They arrive.
	rec := e.do("POST", "/book", guestForm(1, "12:00", "4"), false)
	ref := refFromLocation(t, rec.Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	noon := time.Date(2026, 10, 26, 12, 5, 0, 0, schedule.Location)
	e.srv.Now = func() time.Time { return noon }
	e.do("POST", "/admin/bookings/"+ref+"/arrived", url.Values{"arrived": {"yes"}}, true)
	bk, _ := e.store.Get(ref)
	table := bk.TableNo
	page := fmt.Sprintf("/admin/orders/%d", table)

	// 2. Lisa, a waiter, counts the house wine: 3 glasses left in the bottle.
	e.do("POST", "/admin/staff", url.Values{"name": {"Lisa"}, "username": {"lisa"}, "password": {"secret123"}}, true)
	lisa, _ := e.loginAs(t, "lisa", "secret123")
	location(t, e.as(lisa, "POST", fmt.Sprintf("/admin/stock/%d", wine.ID), url.Values{"do": {"count"}, "left": {"3"}}), "/admin/stock")

	// 3. The order: 2 burgers (to the chef), 2 glasses of wine. 1 glass left.
	location(t, e.as(lisa, "POST", page+"/add", url.Values{"bill": {"0"}, qty(burger): {"2"}, qty(wine): {"2"}}), "msg=addedfood")
	e.expect(e.do("GET", "/admin/kitchen?part=tickets", nil, true), 200, "E-Meal-burgare")
	e.expect(e.as(lisa, "GET", "/admin/orders", nil), 200, "Running low:", "Husets vin, glas (1 left)")
	b, _ := e.store.OpenBill(table)
	id := fmt.Sprint(b.ID)
	if b.Deposit() != 2000 || b.ToPay() != 2*1990+2*850-2000 {
		t.Fatalf("lunch bill: deposit %d, to pay %d", b.Deposit(), b.ToPay())
	}

	// 4. A gift card sold at the counter this morning (10 €, cash).
	location(t, e.as(lisa, "POST", "/admin/giftcards", url.Values{"kind": {"gift"}, "amount": {"10"}, "method": {"cash"}}), "/admin/giftcards")
	cards := e.store.Vouchers(1)
	if len(cards) != 1 || cards[0].Cents != 1000 {
		t.Fatalf("gift card: %+v", cards)
	}

	// 5. They split it evenly. The deposit comes off guest 1's part (the one
	// who booked): 28,40 − 20,00 = 8,40 €; guest 2 owes 28,40 €.
	// Guest 1 pays with Smartum. Guest 2: the gift card, then the terminal.
	location(t, e.as(lisa, "POST", page+"/split/guests", url.Values{"bill": {id}, "guests": {"2"}}), "msg=splitset")
	location(t, e.as(lisa, "POST", page+"/split/even", url.Values{"bill": {id}}), "msg=spliteven")
	b, _ = e.store.OpenBill(table)
	part1, part2 := b.ShareLeft(1), b.ShareLeft(2)
	if part1 != 840 || part2 != 2840 {
		t.Fatalf("parts %d and %d", part1, part2)
	}
	location(t, e.as(lisa, "POST", page+"/split/pay", url.Values{"bill": {id}, "guest": {"1"}, "method": {"smartum"}}), "msg=guestpaid")
	location(t, e.as(lisa, "POST", page+"/split/part", url.Values{"bill": {id}, "guest": {"2"}, "method": {"giftcard"}, "code": {cards[0].Code}}), "msg=guestpart")
	e.expect(e.as(lisa, "GET", "/admin/orders", nil), 200, "Not paid yet", "18,40 €") // what guest 2 still owes
	location(t, e.as(lisa, "POST", page+"/split/pay", url.Values{"bill": {id}, "guest": {"2"}, "method": {"card"}}), "msg=allpaid")
	if _, open := e.store.OpenBill(table); open {
		t.Fatal("the table should be free")
	}

	// 6. A walk-in at another table wants 2 glasses: only 1 left. They take it.
	walkIn := 2
	if table == 2 {
		walkIn = 3
	}
	wpage := fmt.Sprintf("/admin/orders/%d", walkIn)
	rec = e.as(lisa, "POST", wpage+"/add", url.Values{"bill": {"0"}, qty(wine): {"2"}})
	location(t, rec, "msg=soldout")
	e.expect(e.as(lisa, "GET", loc(rec), nil), 200, "Only 1 left of Husets vin, glas. Nothing was added.")
	location(t, e.as(lisa, "POST", wpage+"/add", url.Values{"bill": {"0"}, qty(wine): {"1"}}), "msg=addeddrinks")
	e.expect(e.as(lisa, "GET", "/admin/orders", nil), 200, "Out of stock:", "Husets vin, glas")
	if strings.Contains(e.do("GET", "/takeaway", nil, false).Body.String(), "Husets vin") {
		t.Fatal("wine on the takeaway page")
	}
	w, _ := e.store.OpenBill(walkIn)
	location(t, e.as(lisa, "POST", wpage+"/pay", url.Values{"bill": {fmt.Sprint(w.ID)}, "method": {"cash"}}), "msg=billpaid")

	// 7. The end of the day: every euro where it was paid.
	lunch := part1 + part2
	total := lunch + 850
	_, report := e.srv.dailyReport(noon, noon.Add(10*time.Hour))
	for _, want := range []string{
		fmt.Sprintf("PAID AT THE TABLES: %s (2 bills)", euro(total)),
		fmt.Sprintf("Card terminal:        %s", euro(part2-1000)),
		fmt.Sprintf("Cash:                 %s", euro(850)),
		fmt.Sprintf("Smartum:              %s", euro(part1)),
		fmt.Sprintf("Gift card:            %s (paid for when sold)", euro(1000)),
		"Gift cards sold:      10,00 €",
		"(paid at the terminal 0,00 €, in cash 10,00 €; not in the total above)",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report is missing %q:\n%s", want, report)
		}
	}
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, euro(total))
	e.expect(e.do("GET", "/admin/activity?date=2026-10-26", nil, true), 200,
		"Lisa", "counted the stock", "paid with a gift card", "paid with a lunch card", "paid in cash", "sold a gift card")
	e.expect(e.do("GET", "/admin/stock", nil, true), 200, "Out of stock now", "Husets vin, glas")
}
