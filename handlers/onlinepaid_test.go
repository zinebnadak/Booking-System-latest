package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// The waitress orders for a guest; he pays the whole table on his phone and
// leaves. The waiters are told (with a chime) until one taps OK.
func TestWaitersSeeAPhonePayment(t *testing.T) {
	e := newEnv(t, nil)
	burger := e.menuItem("E-Meal-burgare")
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"1"}}, true), "msg=addedfood")
	a := join(t, e, tableCode(t, e, 5), 1)
	a.do("POST", "/g/"+a.key+"/name", url.Values{"name": {"Erik"}})
	// Nothing on his own part: he pays for the whole table, with a 10 % tip.
	e.expect(a.do("GET", "/g/"+a.key, nil), 200, "Pay for the whole table · 19,90 €")
	ref := strings.TrimPrefix(loc(a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"1"}, "tip": {"10"}})), "/dev/tab/")
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Paying on a phone") // on his phone right now
	a.do("POST", "/dev/tab/"+ref, url.Values{"action": {"pay"}})

	if _, open := e.store.OpenBill(5); open {
		t.Fatal("the table should be free")
	}
	for _, p := range []string{"/admin/orders?part=tiles"} {
		e.expect(e.do("GET", p, nil, true), 200, "Paid on phones", "Table 5", "Erik paid 19,90 € online", "1,99 € tip", "for the whole table", "Table is free", `data-alert-key="paid-`+ref)
	}
	e.expect(e.do("GET", "/owner", nil, true), 200, "Online at the table", "19,90 €")
	// A waiter taps OK: gone from the screens.
	if rec := e.do("POST", "/admin/online/"+ref+"/seen", url.Values{}, true); rec.Code != 303 {
		t.Fatalf("seen: %d", rec.Code)
	}
	if strings.Contains(e.do("GET", "/admin/orders?part=tiles", nil, true).Body.String(), "Paid on phones") {
		t.Fatal("still shown after OK")
	}
	if rec := e.do("POST", "/admin/online/nope/seen", url.Values{}, true); rec.Code != 303 {
		t.Fatalf("unknown: %d", rec.Code)
	}
}
