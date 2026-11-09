package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// A Wolt order: staff tap the Wolt circle on Orders and write the name, add
// the dishes like at a table, the chef sees "Wolt · name", the order shows
// next to the tables, and when the courier takes it it's paid by Wolt.
// The money pages count it, with a filter: with Wolt, without, or only Wolt.
func TestWoltOrder(t *testing.T) {
	e := newEnv(t, nil)
	burger := e.menuItem("E-Meal-burgare")

	orders := e.do("GET", "/admin/orders", nil, true)
	e.expect(orders, 200, "New Wolt order", `action="/admin/wolt"`)

	location(t, e.do("POST", "/admin/wolt", url.Values{"name": {"  "}}, true), "msg=woltname")
	rec := e.do("POST", "/admin/wolt", url.Values{"name": {"Lisa  K"}}, true)
	page := loc(rec)
	if rec.Code != 303 || page != fmt.Sprintf("/admin/orders/%d", schedule.WoltFirst) {
		t.Fatalf("new Wolt order went to %d %q", rec.Code, page)
	}
	e.expect(e.do("GET", page, nil, true), 200, "Wolt · Lisa K", "Picked up by Wolt")
	if strings.Contains(e.do("GET", page, nil, true).Body.String(), "Guests at the table") {
		t.Error("a Wolt order has no guests at a table")
	}
	// A second one gets its own number.
	if loc(e.do("POST", "/admin/wolt", url.Values{"name": {"Omar"}}, true)) != fmt.Sprintf("/admin/orders/%d", schedule.WoltFirst+1) {
		t.Fatal("second Wolt order should get the next number")
	}

	location(t, e.do("POST", page+"/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"2"}}, true), "msg=added")
	b, _ := e.store.OpenBill(schedule.WoltFirst)
	if b.Wolt != "Lisa K" || b.Total() != 2*burger.PriceCents {
		t.Fatalf("bill %+v", b)
	}
	kitchen := e.do("GET", "/admin/kitchen", nil, true)
	e.expect(kitchen, 200, "Wolt · Lisa K")

	// On Orders, next to the tables; the Wolt order isn't a table to pay or
	// to give a waiter.
	orders = e.do("GET", "/admin/orders", nil, true)
	e.expect(orders, 200, "ot-wolt", "Lisa K", "With the chef")
	location(t, e.do("POST", page+"/pay", url.Values{"bill": {fmt.Sprint(b.ID)}, "method": {"cash"}}, true), "msg=invalid")
	location(t, e.do("POST", "/admin/orders/3/woltpickup", url.Values{"bill": {fmt.Sprint(b.ID)}}, true), "msg=invalid")

	// The chef finishes it: ready for the courier (not in "food to take to a table").
	tickets := e.store.TicketsForBill(b.ID)
	for id := range tickets {
		if err := e.store.TicketDone(id, false, e.srv.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.srv.readyFood(e.srv.Now())) != 0 {
		t.Error("Wolt food shouldn't be on the list of food to take to a table")
	}
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Ready for the courier")

	location(t, e.do("POST", page+"/woltpickup", url.Values{"bill": {fmt.Sprint(b.ID)}}, true), "msg=woltdone")
	paid, _ := e.store.BillByID(b.ID)
	if paid.PaidBy != db.PaidWolt || paid.PaidAt == nil {
		t.Fatalf("picked up: %+v", paid)
	}
	if loc(e.do("GET", page, nil, true)) != "/admin/orders" {
		t.Error("a picked-up Wolt order's page should go back to Orders")
	}

	// Money pages: with Wolt (the start), without, only.
	want := euro(2 * burger.PriceCents)
	e.expect(e.do("GET", "/owner", nil, true), 200, "Wolt · 1 order", want, "Without Wolt")
	only := e.do("GET", "/owner?wolt=only", nil, true).Body.String()
	if !strings.Contains(only, `<div class="kpi-big">`+want) {
		t.Error("only Wolt: the big number should be the Wolt money")
	}
	without := e.do("GET", "/owner?wolt=without", nil, true).Body.String()
	if strings.Contains(without, `<div class="kpi-big">`+want) {
		t.Error("without Wolt: the big number shouldn't have the Wolt money")
	}
	e.expect(e.do("GET", "/owner/charts?metric=wolt", nil, true), 200, "Wolt orders")
}

// A Wolt order started by mistake can be cancelled while it's empty, by
// anyone; once something is on it, only the manager can void it.
func TestCancelEmptyWolt(t *testing.T) {
	e := newEnv(t, nil)
	page := loc(e.do("POST", "/admin/wolt", url.Values{"name": {"Oops"}}, true))
	b, _ := e.store.OpenBill(schedule.WoltFirst)
	e.expect(e.do("GET", page, nil, true), 200, "Started by mistake? Cancel it")
	location(t, e.do("POST", page+"/woltcancel", url.Values{"bill": {fmt.Sprint(b.ID)}}, true), "msg=woltcancelled")
	if _, open := e.store.OpenBill(schedule.WoltFirst); open {
		t.Fatal("still open")
	}
	burger := e.menuItem("E-Meal-burgare")
	page = loc(e.do("POST", "/admin/wolt", url.Values{"name": {"Full"}}, true))
	location(t, e.do("POST", page+"/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"1"}}, true), "msg=added")
	b, _ = e.store.OpenBill(schedule.WoltFirst)
	location(t, e.do("POST", page+"/woltcancel", url.Values{"bill": {fmt.Sprint(b.ID)}}, true), "msg=askmanager")
}
