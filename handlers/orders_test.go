package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

func (e *env) menuItem(name string) db.MenuItem {
	for _, m := range e.store.Menu(false) {
		if m.Name == name {
			return m
		}
	}
	e.t.Fatalf("no %q on the menu", name)
	return db.MenuItem{}
}

func location(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), want) {
		t.Fatalf("got %d %q, want a redirect containing %q\n%s", rec.Code, rec.Header().Get("Location"), want, rec.Body.String())
	}
}

// The restaurant's example: table 5 orders food, later a soda. The chef only
// sees the food; everything is on one bill, paid at the end.
func TestTableOrderToPaidBill(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	soup, soda := e.menuItem("Fisksoppa"), e.menuItem("Läsk 33 cl")
	id := func(m db.MenuItem) string { return fmt.Sprint(m.ID) }

	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "Take the order", "Läsk 33 cl", "Fisksoppa", "Something not on the menu")
	e.expect(e.do("GET", "/admin/orders/9", nil, true), 404)

	// Food with a note and a changed price.
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{
		"bill": {"0"}, "qty_" + id(soup): {"1"}, "note_" + id(soup): {"utan lök"}, "price_" + id(soup): {"12,00"},
	}, true), "msg=addedfood")
	bill, ok := e.store.OpenBill(5)
	if !ok || bill.Total() != 1200 || bill.Lines[0].Note != "utan lök" {
		t.Fatalf("bill after food: %+v", bill)
	}

	// The chef sees table 5's soup and the note, no prices.
	k := e.do("GET", "/admin/kitchen?part=tickets", nil, true)
	e.expect(k, 200, "Table 5", "Fisksoppa", "utan lök")
	if strings.Contains(k.Body.String(), "€") {
		t.Fatal("prices on the kitchen screen")
	}

	// A soda while eating: same bill, nothing for the chef.
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{
		"bill": {fmt.Sprint(bill.ID)}, "qty_" + id(soda): {"1"}, "note_" + id(soda): {"ignored"},
	}, true), "msg=addeddrinks")
	k = e.do("GET", "/admin/kitchen?part=tickets", nil, true)
	if strings.Contains(k.Body.String(), "Läsk") || strings.Count(k.Body.String(), "data-alert-key=") != 1 {
		t.Fatalf("the drink reached the kitchen:\n%s", k.Body.String())
	}
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "15,50 €", "menu price 15,50 €", "With the chef", "Paid at the terminal")
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "15,50 €", "With the chef")
	e.expect(e.do("GET", "/admin/tables?part=tiles", nil, true), 200, "Bill 15,50 €")

	// The chef finishes: staff see "Food ready".
	ticket := e.store.OpenTickets()[0]
	live := httptest.NewRequest("POST", fmt.Sprintf("http://example.test/admin/kitchen/%d/done", ticket.ID), nil)
	live.Header.Set("X-Live", "1")
	live.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleManager, e.srv.Now())})
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, live)
	e.expect(rec, 204)
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Food ready 18:00 · pick up", "Food ready to pick up", fmt.Sprintf(`data-alert-key="ready-%d"`, ticket.ID))
	e.expect(e.do("GET", "/admin/tables?part=tiles", nil, true), 200, "Food ready to pick up")
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "Ready 18:00 · pick up")
	e.expect(e.do("GET", "/admin/kitchen?part=tickets", nil, true), 200, "No orders right now", "Ready at the pass", "Not done yet")

	// A waiter takes it to the table and taps Served: waiting to pay.
	location(t, e.do("POST", fmt.Sprintf("/admin/kitchen/%d/served", ticket.ID), url.Values{"back": {"orders"}}, true), "/admin/orders")
	tiles := e.do("GET", "/admin/orders?part=tiles", nil, true)
	e.expect(tiles, 200, "Served · waiting to pay", "ot-served")
	if strings.Contains(tiles.Body.String(), "Food ready") {
		t.Fatal("still says food ready after it was served")
	}
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "Served 18:00")
	e.expect(e.do("GET", "/admin/kitchen?part=tickets", nil, true), 200, "No orders right now", "Served and done", "Undo")

	// Two sodas instead of one; the soup can only change price now.
	b, _ := e.store.OpenBill(5)
	bid := fmt.Sprint(b.ID)
	location(t, e.do("POST", fmt.Sprintf("/admin/orders/5/lines/%d/save", b.Lines[1].ID), url.Values{"bill": {bid}, "price": {"3,50"}, "qty": {"2"}}, true), "msg=linesaved")
	location(t, e.do("POST", fmt.Sprintf("/admin/orders/5/lines/%d/save", b.Lines[0].ID), url.Values{"bill": {bid}, "price": {"12,00"}, "qty": {"3"}}, true), "msg=sentalready")
	if b, _ = e.store.OpenBill(5); b.Total() != 1200+700 {
		t.Fatalf("total after changes: %d", b.Total())
	}

	// Show the bill, then paid in cash: the table is free again.
	location(t, e.do("POST", "/admin/orders/5/billed", url.Values{"bill": {bid}}, true), "/admin/orders/5/bill?bill="+bid)
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Bill shown · waiting for payment")
	e.expect(e.do("GET", "/admin/orders/5/bill?bill="+bid, nil, true), 200, "To pay", "19,00 €", "Thank you for your visit, welcome back!", "Pro forma")
	location(t, e.do("POST", "/admin/orders/5/pay", url.Values{"bill": {bid}, "method": {"cash"}}, true), "msg=billpaid")
	if _, ok := e.store.OpenBill(5); ok {
		t.Fatal("bill still open after paying")
	}
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Paid today", "In cash", "19,00 €")

	// A waiter still looking at the paid bill can't add to it.
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {bid}, "qty_" + id(soda): {"1"}}, true), "msg=billstale")
	// Bad input changes nothing.
	location(t, e.do("POST", "/admin/orders/4/add", url.Values{"bill": {"0"}, "qty_" + id(soda): {"1"}, "price_" + id(soda): {"abc"}}, true), "msg=invalid")
	location(t, e.do("POST", "/admin/orders/4/add", url.Values{"bill": {"0"}, "qty_" + id(soda): {"51"}}, true), "msg=invalid")
	location(t, e.do("POST", "/admin/orders/4/add", url.Values{"bill": {"0"}}, true), "msg=nothingadded")
	if _, ok := e.store.OpenBill(4); ok {
		t.Fatal("a bad order opened a bill")
	}
}

func TestSomethingNotOnTheMenuAndMoving(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	location(t, e.do("POST", "/admin/orders/2/add", url.Values{
		"bill": {"0"}, "custom_name": {"Birthday cake"}, "custom_price": {"25"}, "custom_qty": {"1"}, "custom_type": {"food"}, "custom_note": {"candles: 7"},
	}, true), "msg=addedfood")
	e.expect(e.do("GET", "/admin/kitchen", nil, true), 200, "Birthday cake", "candles: 7")
	b, _ := e.store.OpenBill(2)
	location(t, e.do("POST", "/admin/orders/2/move", url.Values{"bill": {fmt.Sprint(b.ID)}, "to": {"6"}}, true), "/admin/orders/6?msg=billmoved")
	e.expect(e.do("GET", "/admin/kitchen?part=tickets", nil, true), 200, "Table 6")
	location(t, e.do("POST", "/admin/orders/6/void", url.Values{"bill": {fmt.Sprint(b.ID)}}, true), "msg=billvoided")
	e.expect(e.do("GET", "/admin/kitchen?part=tickets", nil, true), 200, "cancelled", "Clear")
}

func TestDrinksNeverOnTakeaway(t *testing.T) {
	e := newEnv(t, nil)
	soda := e.menuItem("Läsk 33 cl")
	if strings.Contains(e.do("GET", "/takeaway", nil, false).Body.String(), "Läsk") {
		t.Fatal("a drink on the takeaway menu")
	}
	// Even a hand-made form can't order one.
	f := url.Values{"date": {"2026-10-26"}, "time": {"12:30"}, "name": {"Erik"}, "email": {"erik@example.com"}, "phone": {"0401234567"},
		fmt.Sprintf("qty_%d", soda.ID): {"2"}}
	e.expect(e.do("POST", "/takeaway", f, false), 422, "at least one dish")
}

func TestDepositComesOffTheBill(t *testing.T) {
	e := newEnv(t, nil)
	// An online booking for 4 at 12:00 (20 € deposit), paid; they arrive.
	rec := e.do("POST", "/book", guestForm(1, "12:00", "4"), false)
	ref := refFromLocation(t, rec.Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	e.srv.Now = func() time.Time { return time.Date(2026, 10, 26, 12, 5, 0, 0, schedule.Location) }
	e.do("POST", "/admin/bookings/"+ref+"/arrived", url.Values{"arrived": {"yes"}}, true)
	bk, _ := e.store.Get(ref)

	page := fmt.Sprintf("/admin/orders/%d", bk.TableNo)
	e.expect(e.do("GET", page, nil, true), 200, "These guests paid a 20,00 € deposit online.")
	burger := e.menuItem("Ozmo-burgare")
	location(t, e.do("POST", page+"/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"2"}}, true), "msg=addedfood")
	b, _ := e.store.OpenBill(bk.TableNo)
	if b.BookingRef != ref || b.Deposit() != 2000 || b.ToPay() != 2*1990-2000 {
		t.Fatalf("deposit not taken off: %+v", b)
	}
	e.expect(e.do("GET", page, nil, true), 200, "Deposit paid online", "−20,00 €", "19,80 €")
}

func TestOrdersAreStaffOnly(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{"/admin/orders", "/admin/orders/3", "/admin/kitchen"} {
		rec := e.do("GET", p, nil, false)
		if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
			t.Fatalf("%s without login: %d", p, rec.Code)
		}
	}
	req := httptest.NewRequest("POST", "http://example.test/admin/orders/3/add", strings.NewReader("bill=0&custom_name=x&custom_price=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleManager, e.srv.Now())})
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	e.expect(rec, 403)
	e.expect(e.do("GET", "/admin/kitchen?lang=sv", nil, true), 200, "Kök", "Inga beställningar just nu")
	e.expect(e.do("GET", "/admin/orders/3?lang=sv", nil, true), 200, "Ta upp beställningen", "Lägg på notan")
}

// Every bill a table has is kept: "Table 5 · bill 1", "bill 2", on its day,
// with the full order, and the receipt can be opened again later.
func TestTableHistory(t *testing.T) {
	e := newEnv(t, nil)
	soup, soda := e.menuItem("Fisksoppa"), e.menuItem("Läsk 33 cl")
	id := func(m db.MenuItem) string { return fmt.Sprint(m.ID) }
	at := func(day, clock string) {
		d, _ := schedule.ParseDate(day)
		t0, _ := schedule.At(d, clock)
		e.srv.Now = func() time.Time { return t0 }
	}
	payBill := func(method string) int {
		b, ok := e.store.OpenBill(5)
		if !ok {
			t.Fatal("no open bill")
		}
		location(t, e.do("POST", "/admin/orders/5/pay", url.Values{"bill": {fmt.Sprint(b.ID)}, "method": {method}}, true), "msg=billpaid")
		return b.ID
	}

	e.expect(e.do("GET", "/admin/orders/5/history", nil, true), 200, "Table 5 · history", "No bills yet")

	// Friday: one bill. Saturday: a voided bill, then two paid ones, then one still open.
	at("2026-10-30", "18:00")
	e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, "qty_" + id(soup): {"2"}}, true)
	at("2026-10-30", "19:10")
	fri := payBill("cash")

	at("2026-10-31", "12:00")
	e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, "qty_" + id(soda): {"1"}}, true)
	b, _ := e.store.OpenBill(5)
	e.do("POST", "/admin/orders/5/void", url.Values{"bill": {fmt.Sprint(b.ID)}}, true)

	at("2026-10-31", "18:05")
	e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, "qty_" + id(soup): {"1"}, "price_" + id(soup): {"10,00"}}, true)
	at("2026-10-31", "18:40")
	b, _ = e.store.OpenBill(5)
	e.do("POST", "/admin/orders/5/add", url.Values{"bill": {fmt.Sprint(b.ID)}, "qty_" + id(soda): {"1"}, "price_" + id(soda): {"5,00"}}, true)
	at("2026-10-31", "19:20")
	first := payBill("card")

	at("2026-10-31", "19:45")
	e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, "qty_" + id(soda): {"2"}}, true)
	at("2026-10-31", "20:30")
	second := payBill("card")

	at("2026-10-31", "20:50")
	e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, "qty_" + id(soda): {"1"}}, true)

	// Another table's bill never shows in table 5's history.
	e.do("POST", "/admin/orders/2/add", url.Values{"bill": {"0"}, "qty_" + id(soup): {"1"}, "note_" + id(soup): {"TABLE-TWO"}}, true)

	h := e.do("GET", "/admin/orders/5/history", nil, true)
	e.expect(h, 200, "3 bills paid", "Saturday 31 October", "Friday 30 October",
		"Bill 1", "Bill 2", "Bill 3", "Voided bill", "voided, not paid", "Open now",
		"18:05–19:20", "Paid at the terminal", "Paid in cash", "1× Fisksoppa", "menu price 15,50 €", "15,00 €")
	body := h.Body.String()
	if strings.Contains(body, "TABLE-TWO") {
		t.Fatal("table 2's order in table 5's history")
	}
	// Newest day first; inside a day, bill 1 before bill 2.
	if strings.Index(body, "Saturday 31 October") > strings.Index(body, "Friday 30 October") ||
		strings.Index(body, fmt.Sprintf("#%d", first)) > strings.Index(body, fmt.Sprintf("#%d", second)) {
		t.Fatal("history is in the wrong order")
	}

	// The receipts can be opened again, numbered, with a way back to the history.
	e.expect(e.do("GET", fmt.Sprintf("/admin/orders/5/bill?bill=%d", first), nil, true), 200, "Bill 1", fmt.Sprintf("#%d", first), "15,00 €", "Paid by card", "/admin/orders/5/history")
	e.expect(e.do("GET", fmt.Sprintf("/admin/orders/5/bill?bill=%d", fri), nil, true), 200, "Bill 1", "31,00 €", "Paid in cash")
	e.expect(e.do("GET", fmt.Sprintf("/admin/orders/2/bill?bill=%d", first), nil, true), 404)

	// Swedish, and staff only.
	e.expect(e.do("GET", "/admin/orders/5/history?lang=sv", nil, true), 200, "Bord 5 · historik", "Nota 2", "Makulerad nota")
	if rec := e.do("GET", "/admin/orders/5/history", nil, false); rec.Code != 303 {
		t.Fatalf("history without login: %d", rec.Code)
	}
	e.expect(e.do("GET", "/admin/orders/99/history", nil, true), 404)
}

func TestTableHistoryPages(t *testing.T) {
	e := newEnv(t, nil)
	soda := e.menuItem("Läsk 33 cl")
	start, _ := schedule.ParseDate("2026-09-01")
	for i := 0; i < 20; i++ { // 20 days with one paid bill each
		now := start.AddDate(0, 0, i).Add(18 * time.Hour)
		e.srv.Now = func() time.Time { return now }
		e.do("POST", "/admin/orders/1/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", soda.ID): {"1"}}, true)
		b, _ := e.store.OpenBill(1)
		e.do("POST", "/admin/orders/1/pay", url.Values{"bill": {fmt.Sprint(b.ID)}, "method": {"card"}}, true)
	}
	page1 := e.do("GET", "/admin/orders/1/history", nil, true)
	e.expect(page1, 200, "20 bills paid", "Sunday 20 September", "Older days", "before=2026-09-07")
	if strings.Contains(page1.Body.String(), "Sunday 6 September") {
		t.Fatal("more than 14 days on one page")
	}
	page2 := e.do("GET", "/admin/orders/1/history?before=2026-09-07", nil, true)
	e.expect(page2, 200, "Sunday 6 September", "Tuesday 1 September", "Newest days")
	if strings.Contains(page2.Body.String(), "Older days") || strings.Contains(page2.Body.String(), "Monday 7 September") {
		t.Fatal("second page is wrong")
	}
}
