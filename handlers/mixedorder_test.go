package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"emeal-booking/db"
)

// The waitress orders for a guest at table 5; then he scans the QR code and
// orders a drink himself. It's all one bill, the waiters see the drink to
// bring, the stock counts it, and one tap takes the payment for everything.
func TestWaiterAndPhoneOrdersOnOneBill(t *testing.T) {
	e := newEnv(t, nil)
	wine, burger := e.menuItem("Husets vin, glas"), e.menuItem("E-Meal-burgare")
	e.do("POST", fmt.Sprintf("/admin/stock/%d", wine.ID), url.Values{"do": {"count"}, "left": {"5"}}, true)

	// 1. The waitress takes his order on her phone.
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"1"}}, true), "msg=addedfood")
	// 2. He scans the table's code and orders a glass of wine.
	a := join(t, e, tableCode(t, e, 5), 1)
	a.do("POST", "/g/"+a.key+"/name", url.Values{"name": {"Erik"}})
	if rec := a.do("POST", "/g/"+a.key+"/order", url.Values{fmt.Sprintf("qty_%d", wine.ID): {"1"}}); loc(rec) != "/g/"+a.key+"?msg=ordered" {
		t.Fatalf("phone order: %s", loc(rec))
	}

	// One bill with both.
	b, _ := e.store.OpenBill(5)
	if len(b.Lines) != 2 || b.ToPay() != 1990+850 {
		t.Fatalf("bill: %+v", b.Lines)
	}
	// The waiters see the drink to bring (with a chime), and on the table's bill.
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Drinks to bring", "1× Husets vin, glas", "Erik", `data-alert-key="drink-`)
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "E-Meal-burgare", "Husets vin, glas", "Erik", "To bring")
	e.expect(e.do("GET", "/admin/kitchen?part=tickets", nil, true), 200, "E-Meal-burgare")
	if m, _ := e.store.MenuItem(wine.ID); m.Left != 4 {
		t.Fatalf("wine left: %d", m.Left)
	}

	// When the wine runs out, his phone can't order it.
	e.do("POST", fmt.Sprintf("/admin/stock/%d", wine.ID), url.Values{"do": {"out"}}, true)
	if strings.Contains(a.do("GET", "/g/"+a.key, nil).Body.String(), fmt.Sprintf(`name="qty_%d"`, wine.ID)) {
		t.Fatal("wine can still be ordered on his phone")
	}
	if rec := a.do("POST", "/g/"+a.key+"/order", url.Values{fmt.Sprintf("qty_%d", wine.ID): {"1"}}); loc(rec) != "/g/"+a.key+"?msg=soldout" {
		t.Fatalf("ordering wine that's out: %s", loc(rec))
	}

	// 3. He pays the waitress for everything: one tap.
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "Still to pay at this table: 28,40 €", "All at the terminal")
	location(t, e.do("POST", "/admin/orders/5/paytable", url.Values{"bill": {fmt.Sprint(b.ID)}, "method": {"card"}}, true), "msg=allpaid")
	if _, open := e.store.OpenBill(5); open {
		t.Fatal("table should be free")
	}
	paid, _ := e.store.BillByID(b.ID)
	if tk := paid.Takings(); tk[db.PaidCard] != 2840 || len(tk) != 1 {
		t.Fatalf("takings: %v", tk)
	}
	e.expect(a.do("GET", "/g/"+a.key, nil), 200, "The table is paid. Thank you!", "Your receipt")
	e.expect(e.do("GET", "/admin/activity", nil, true), 200, "28,40 € (whole table)")

	// Another table: the waitress brings two coffees for the table; a guest
	// orders a burger on his phone and pays only his own part there. The
	// coffees are still to pay: one tap puts them on "the table" and closes it.
	coffee := e.menuItem("Kaffe")
	location(t, e.do("POST", "/admin/orders/6/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", coffee.ID): {"2"}}, true), "msg=added")
	g := join(t, e, tableCode(t, e, 6), 1)
	g.do("POST", "/g/"+g.key+"/order", url.Values{fmt.Sprintf("qty_%d", burger.ID): {"1"}})
	ref := strings.TrimPrefix(loc(g.do("POST", "/g/"+g.key+"/pay", url.Values{"all": {"0"}})), "/dev/tab/")
	g.do("POST", "/dev/tab/"+ref, url.Values{"action": {"pay"}})
	b6, _ := e.store.OpenBill(6)
	if b6.SharesPaid() != 1 || b6.TableLeft() != 600 {
		t.Fatalf("table 6: paid %d, left %d", b6.SharesPaid(), b6.TableLeft())
	}
	e.expect(e.do("GET", "/admin/orders/6", nil, true), 200, "Still to pay at this table: 6,00 €")
	location(t, e.do("POST", "/admin/orders/6/paytable", url.Values{"bill": {fmt.Sprint(b6.ID)}, "method": {"cash"}}, true), "msg=allpaid")
	b6, _ = e.store.BillByID(b6.ID)
	if b6.PaidAt == nil || len(b6.Split) != 2 || b6.Takings()[db.PaidCash] != 600 || b6.Takings()[db.PaidOnline] != 1990 {
		t.Fatalf("table 6 after: %+v %v", b6.Split, b6.Takings())
	}
}
