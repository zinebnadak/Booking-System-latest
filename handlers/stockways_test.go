package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// The same burger counted down whichever way it's ordered: a waiter at the
// table, a guest with the table's QR code, or online for takeaway.
func TestStockCountsEveryWayToOrder(t *testing.T) {
	e := newEnv(t, nil)
	burger := e.menuItem("E-Meal-burgare")
	id := fmt.Sprint(burger.ID)
	left := func() int {
		for _, m := range e.store.Menu(false) {
			if m.ID == burger.ID {
				return m.Left
			}
		}
		return -1
	}
	e.do("POST", "/admin/stock/"+id, url.Values{"do": {"count"}, "left": {"5"}}, true)

	// A waiter orders one at table 5.
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, "qty_" + id: {"1"}}, true), "msg=addedfood")
	if n := left(); n != 4 {
		t.Fatalf("after the waiter: %d", n)
	}
	// A guest at the same table orders one with the QR code.
	a := join(t, e, tableCode(t, e, 5), 1)
	if rec := a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + id: {"1"}}); loc(rec) != "/g/"+a.key+"?msg=ordered" {
		t.Fatalf("QR order: %s", loc(rec))
	}
	if n := left(); n != 3 {
		t.Fatalf("after the QR order: %d", n)
	}
	// Online takeaway: counted when it's paid.
	f := url.Values{"date": {"2026-10-26"}, "time": {"12:30"}, "name": {"Erik"}, "email": {"erik@example.com"},
		"phone": {"0401234567"}, "qty_" + id: {"2"}}
	ref := refFromLocation(t, e.do("POST", "/takeaway", f, false).Header().Get("Location"))
	if n := left(); n != 3 {
		t.Fatalf("unpaid takeaway took stock: %d", n)
	}
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	if n := left(); n != 1 {
		t.Fatalf("after the paid takeaway: %d", n)
	}
	// One left: another online order for two is stopped before paying.
	f.Set("email", "lisa@example.com")
	f.Set("phone", "0407654321")
	rec := e.do("POST", "/takeaway", f, false)
	e.expect(rec, 422, "only 1 left of E-Meal-burgare")
	// The waiters see it on the Stock page, under Food.
	page := e.do("GET", "/admin/stock", nil, true).Body.String()
	for _, want := range []string{`data-kind-filter`, `id="s-` + id + `" data-kind="food"`, `data-kind="drink"`, `data-kind-section="food"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("stock page missing %q", want)
		}
	}
}
