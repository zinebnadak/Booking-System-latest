package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestStockPage(t *testing.T) {
	wrongPasswordDelay = 0
	e := newEnv(t, nil)
	wine := menuID(t, e, "Husets vin, glas")
	soda := menuID(t, e, "Läsk 33 cl")
	code := tableCode(t, e, 5)

	// A waiter (or the kitchen) uses the Stock page: it's in their menu bar.
	e.do("POST", "/admin/staff", url.Values{"name": {"Lisa"}, "username": {"lisa"}, "password": {"secret123"}}, true)
	lisa, _ := e.loginAs(t, "lisa", "secret123")
	e.expect(e.as(lisa, "GET", "/admin/orders", nil), 200, `href="/admin/stock"`)
	e.expect(e.as(lisa, "GET", "/admin/stock", nil), 200, "Stock", "Husets vin, glas", "Nothing. Everything on the menu can be ordered.")

	// The house wine runs out.
	rec := e.as(lisa, "POST", "/admin/stock/"+wine, url.Values{"do": {"out"}})
	if rec.Code != http.StatusSeeOther || loc(rec) != "/admin/stock#s-"+wine {
		t.Fatalf("out of stock: %d %s", rec.Code, loc(rec))
	}
	e.expect(e.do("GET", "/admin/stock?part=list", nil, true), 200, "Out of stock now", "Back in stock")
	e.expect(e.do("GET", "/admin/activity", nil, true), 200, "Lisa", "marked out of stock", "Husets vin, glas")

	// Every waiter's screen says so at the top...
	for _, p := range []string{"/admin/orders", "/admin/orders/3"} {
		e.expect(e.as(lisa, "GET", p, nil), 200, "Out of stock:", "Husets vin, glas")
	}
	// ...and on the order form it's greyed out, with nothing to tap.
	page := e.as(lisa, "GET", "/admin/orders/3", nil).Body.String()
	if strings.Contains(page, `name="qty_`+wine+`"`) || !strings.Contains(page, `name="qty_`+soda+`"`) {
		t.Fatal("the wine can still be added on the order form")
	}
	// Someone had the form open from before: nothing is added, and it says why.
	rec = e.as(lisa, "POST", "/admin/orders/3/add", url.Values{"bill": {"0"}, "qty_" + wine: {"1"}, "qty_" + soda: {"1"}})
	if !strings.Contains(loc(rec), "msg=soldout") {
		t.Fatalf("ordering it anyway: %s", loc(rec))
	}
	e.expect(e.as(lisa, "GET", loc(rec), nil), 200, "Husets vin, glas is out of stock. Nothing was added.")
	if _, open := e.store.OpenBill(3); open {
		t.Fatal("a bill was opened")
	}

	// Guests can't order it on their phones.
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	a := join(t, e, code, 1)
	if strings.Contains(a.do("GET", "/g/"+a.key, nil).Body.String(), "Husets vin, glas") {
		t.Fatal("still on the phone menu")
	}
	if rec := a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + wine: {"1"}}); loc(rec) != "/g/"+a.key+"?msg=soldout" {
		t.Fatalf("guest orders it: %s", loc(rec))
	}

	// A delivery: 2 bottles' worth counted. It counts down as it's sold.
	if rec := e.do("POST", "/admin/stock/"+wine, url.Values{"do": {"count"}, "left": {" 2 "}}, true); rec.Code != http.StatusSeeOther {
		t.Fatalf("count: %d", rec.Code)
	}
	page = e.do("GET", "/admin/orders/3", nil, true).Body.String()
	if !strings.Contains(page, "2 left") || !strings.Contains(page, `name="qty_`+wine+`" min="0" max="2"`) {
		t.Fatal("order form should say 2 left, and allow at most 2")
	}
	rec = e.do("POST", "/admin/orders/3/add", url.Values{"bill": {"0"}, "qty_" + wine: {"3"}}, true)
	e.expect(e.do("GET", loc(rec), nil, true), 200, "Only 2 left of Husets vin, glas. Nothing was added.")
	if rec := e.do("POST", "/admin/orders/3/add", url.Values{"bill": {"0"}, "qty_" + wine: {"2"}}, true); !strings.Contains(loc(rec), "msg=addeddrinks") {
		t.Fatalf("the last 2: %s", loc(rec))
	}
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Out of stock:", "Husets vin, glas")

	// Back in stock, not counted: on the phone menu again.
	e.do("POST", "/admin/stock/"+wine, url.Values{"do": {"back"}}, true)
	e.expect(a.do("GET", "/g/"+a.key, nil), 200, "Husets vin, glas")
	if strings.Contains(e.do("GET", "/admin/orders", nil, true).Body.String(), "Out of stock:") {
		t.Fatal("strip still shown")
	}

	// Counting, then an empty box: not counted any more.
	e.do("POST", "/admin/stock/"+soda, url.Values{"do": {"count"}, "left": {"3"}}, true)
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Running low:", "Läsk 33 cl (3 left)")
	e.do("POST", "/admin/stock/"+soda, url.Values{"do": {"count"}, "left": {""}}, true)
	if strings.Contains(e.do("GET", "/admin/orders", nil, true).Body.String(), "Running low:") {
		t.Fatal("still running low after it stopped being counted")
	}

	// Mistakes.
	if rec := e.do("POST", "/admin/stock/"+soda, url.Values{"do": {"count"}, "left": {"lots"}}, true); !strings.Contains(loc(rec), "msg=stockbad") {
		t.Fatalf("not a number: %s", loc(rec))
	}
	// Taps on the live screen are sent in the background (X-Live).
	live := func(form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://example.test/admin/stock/"+soda, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Live", "1")
		req.AddCookie(lisa)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	if rec := live(url.Values{"do": {"count"}, "left": {"-1"}}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "9999") {
		t.Fatalf("live, negative: %d %s", rec.Code, rec.Body.String())
	}
	if rec := live(url.Values{"do": {"out"}}); rec.Code != http.StatusNoContent {
		t.Fatalf("live out: %d", rec.Code)
	}
	if rec := e.do("POST", "/admin/stock/99999", url.Values{"do": {"out"}}, true); !strings.Contains(loc(rec), "msg=billstale") {
		t.Fatalf("unknown item: %s", loc(rec))
	}
	// Guests can't reach it.
	if rec := e.do("GET", "/admin/stock", nil, false); rec.Code == http.StatusOK {
		t.Fatal("stock page without logging in")
	}
}
