package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"emeal-booking/db"
)

// Once something is on a bill, a waiter can't take it off, lower it, give a
// discount or void the bill. She asks the manager, who takes it off or
// says no. Guests who leave without paying: "Skip for now" frees the table
// and the bill waits for the manager, who can write it off. The charts
// show everything that was taken off.
func TestOnlyTheManagerTakesThingsOff(t *testing.T) {
	e := newEnv(t, nil)
	burger, soda := e.menuItem("E-Meal-burgare"), e.menuItem("Läsk 33 cl")
	m, err := e.store.AddStaff("Lisa", "lisa", "a-long-password-1", e.srv.Now())
	if err != nil {
		t.Fatal(err)
	}
	lisa := &http.Cookie{Name: sessionCookie, Value: e.srv.makeStaffSession(m, e.srv.Now())}
	location(t, e.as(lisa, "POST", "/admin/orders/5/add", url.Values{"bill": {"0"},
		fmt.Sprintf("qty_%d", burger.ID): {"2"}, fmt.Sprintf("qty_%d", soda.ID): {"3"}}), "msg=added")
	b, _ := e.store.OpenBill(5)
	var sodaLine db.BillLine
	for _, l := range b.Lines {
		if l.MenuItemID == soda.ID {
			sodaLine = l
		}
	}
	bill := fmt.Sprint(b.ID)
	line := func(action string) string { return fmt.Sprintf("/admin/orders/5/lines/%d/%s", sodaLine.ID, action) }

	// Everything that would make the bill smaller is refused.
	for _, try := range []struct {
		path string
		form url.Values
	}{
		{line("remove"), url.Values{"bill": {bill}}},
		{line("approve"), url.Values{"bill": {bill}}},
		{line("save"), url.Values{"bill": {bill}, "price": {euro(sodaLine.PriceCents)}, "qty": {"1"}}},
		{line("save"), url.Values{"bill": {bill}, "price": {"0,00"}, "qty": {"3"}}},
		{line("save"), url.Values{"bill": {bill}, "price": {euro(sodaLine.PriceCents)}, "qty": {"3"}, "discount": {"100"}}},
		{"/admin/orders/5/discount", url.Values{"bill": {bill}, "pct": {"50"}}},
		{"/admin/orders/5/void", url.Values{"bill": {bill}, "why": {"x"}}},
		{"/admin/orders/5/writeoff", url.Values{"bill": {bill}, "why": {"x"}}},
	} {
		location(t, e.as(lisa, "POST", try.path, try.form), "msg=askmanager")
	}
	if after, _ := e.store.OpenBill(5); after.Total() != b.Total() || len(e.store.Removals("2000-01-01", "2100-01-01")) != 0 {
		t.Fatalf("the bill changed: %d → %d", b.Total(), after.Total())
	}
	// More is fine.
	location(t, e.as(lisa, "POST", line("save"), url.Values{"bill": {bill}, "price": {euro(sodaLine.PriceCents)}, "qty": {"4"}, "discount": {"0"}}), "msg=linesaved")

	// She asks the manager to take one soda off.
	location(t, e.as(lisa, "POST", line("ask"), url.Values{"bill": {bill}, "qty": {"1"}}), "msg=noreason")
	location(t, e.as(lisa, "POST", line("ask"), url.Values{"bill": {bill}, "qty": {"1"}, "why": {"Added by mistake"}}), "msg=asksent")
	e.expect(e.as(lisa, "GET", "/admin/orders/5", nil), 200, "Asked to take off 1 · Added by mistake · Lisa", "Take the question back")
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Asked to take off a bill", "1× Läsk 33 cl", "Lisa")
	location(t, e.do("POST", line("approve"), url.Values{"bill": {bill}, "back": {"orders"}}, true), "/admin/orders?msg=lineremoved")
	b, _ = e.store.OpenBill(5)
	for _, l := range b.Lines {
		if l.ID == sodaLine.ID && (l.Qty != 3 || l.Ask != nil) {
			t.Fatalf("after: %+v", l)
		}
	}
	rm := e.store.Removals("2000-01-01", "2100-01-01")
	if len(rm) != 1 || rm[0].Qty != 1 || rm[0].Name != "Läsk 33 cl" || rm[0].AskedBy != "Lisa" || rm[0].By != "Manager" ||
		rm[0].Why != "Added by mistake" || rm[0].Cents != sodaLine.PriceCents || rm[0].Waiter != "Lisa" {
		t.Fatalf("written down: %+v", rm)
	}

	// The guests leave without paying; new guests want the table.
	location(t, e.as(lisa, "POST", "/admin/orders/5/skip", url.Values{"bill": {bill}}), "msg=skipped")
	if _, open := e.store.OpenBill(5); open {
		t.Fatal("table 5 should be free")
	}
	tiles := e.as(lisa, "GET", "/admin/orders?part=tiles", nil).Body.String()
	if !strings.Contains(tiles, "Skipped, not paid yet") || !strings.Contains(tiles, "Skipped 18:00 by Lisa") && !strings.Contains(tiles, "by Lisa") || strings.Contains(tiles, "/writeoff") {
		t.Fatalf("skipped list for a waiter:\n%s", tiles)
	}
	location(t, e.as(lisa, "POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", soda.ID): {"1"}}), "msg=added")
	if nb, _ := e.store.OpenBill(5); nb.ID == b.ID || len(nb.Lines) != 1 {
		t.Fatalf("new guests got the old bill: %+v", nb)
	}

	// The manager writes the skipped one off: on the charts.
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "/writeoff")
	location(t, e.do("POST", "/admin/orders/5/writeoff", url.Values{"bill": {bill}, "why": {"Left without paying"}}, true), "/admin/orders?msg=writtenoff")
	if len(e.store.SkippedBills()) != 0 {
		t.Fatal("still skipped")
	}
	req := httptest.NewRequest("GET", "http://example.test/owner/charts?month="+e.srv.Now().Format("2006-01"), nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleManager, e.srv.Now())})
	req.Header.Set("Accept-Language", "en")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	e.expect(rec, 200, "Taken off bills", "1× Läsk 33 cl", "Added by mistake", "Never paid", "Left without paying", "2× E-Meal-burgare", "3× Läsk 33 cl", "By waiter", "Lisa")
}

// A skipped bill can go back on a free table, and be paid later.
func TestSkippedBillBackAndPaid(t *testing.T) {
	e := newEnv(t, nil)
	soda := e.menuItem("Läsk 33 cl")
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", soda.ID): {"2"}}, true), "msg=added")
	b, _ := e.store.OpenBill(5)
	location(t, e.do("POST", "/admin/orders/5/skip", url.Values{"bill": {fmt.Sprint(b.ID)}}, true), "msg=skipped")
	location(t, e.do("POST", "/admin/orders/5/back", url.Values{"bill": {fmt.Sprint(b.ID)}, "to": {"6"}}, true), "/admin/orders/6?msg=billback")
	if back, open := e.store.OpenBill(6); !open || back.ID != b.ID {
		t.Fatal("not back on table 6")
	}
	location(t, e.do("POST", "/admin/orders/6/skip", url.Values{"bill": {fmt.Sprint(b.ID)}}, true), "msg=skipped")
	location(t, e.do("POST", "/admin/orders/6/pay", url.Values{"bill": {fmt.Sprint(b.ID)}, "method": {"cash"}}, true), "msg=billpaid")
	if len(e.store.SkippedBills()) != 0 || len(e.store.Removals("2000-01-01", "2100-01-01")) != 0 {
		t.Fatal("paid later: nothing skipped or taken off")
	}
}

// The manager next to the waiter types their PIN: it comes off there and
// then, written down as the manager's. A mis-tap can be undone by the waiter
// within a minute. The manager sees a red number in the top bar, and the
// charts mark a waiter with many removals in a week.
func TestPINUndoBadgeAndFlags(t *testing.T) {
	e := newEnv(t, nil)
	soda := e.menuItem("Läsk 33 cl")
	burger := e.menuItem("E-Meal-burgare")
	m, err := e.store.AddStaff("Lisa", "lisa", "a-long-password-1", e.srv.Now())
	if err != nil {
		t.Fatal(err)
	}
	lisa := &http.Cookie{Name: sessionCookie, Value: e.srv.makeStaffSession(m, e.srv.Now())}
	add := func(item db.MenuItem, n int) db.BillLine {
		location(t, e.as(lisa, "POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", item.ID): {fmt.Sprint(n)}}), "msg=added")
		b, _ := e.store.OpenBill(5)
		return b.Lines[len(b.Lines)-1]
	}
	billID := func() string { b, _ := e.store.OpenBill(5); return fmt.Sprint(b.ID) }

	// Undo: right away yes; after a minute no.
	l := add(soda, 2)
	e.expect(e.as(lisa, "GET", "/admin/orders/5", nil), 200, fmt.Sprintf("/lines/%d/undo", l.ID), "data-hide-after=")
	location(t, e.as(lisa, "POST", fmt.Sprintf("/admin/orders/5/lines/%d/undo", l.ID), url.Values{"bill": {billID()}}), "msg=undone")
	l = add(soda, 1)
	later := e.srv.Now().Add(2 * time.Minute)
	e.srv.Now = func() time.Time { return later }
	if strings.Contains(e.as(lisa, "GET", "/admin/orders/5", nil).Body.String(), fmt.Sprintf("/lines/%d/undo", l.ID)) {
		t.Fatal("undo still offered after a minute")
	}
	location(t, e.as(lisa, "POST", fmt.Sprintf("/admin/orders/5/lines/%d/undo", l.ID), url.Values{"bill": {billID()}}), "msg=undolate")

	// The manager's PIN.
	location(t, e.do("POST", "/admin/staff/managerpin", url.Values{"pin": {"4321"}}, true), "msg=mgrpinset")
	e.expect(e.as(lisa, "GET", "/admin/orders/5", nil), 200, "the manager types their PIN", "Take off now")
	pin := func(pin string) *httptest.ResponseRecorder {
		return e.as(lisa, "POST", fmt.Sprintf("/admin/orders/5/lines/%d/pin", l.ID), url.Values{"bill": {billID()}, "qty": {"1"}, "why": {"Wrong table"}, "pin": {pin}})
	}
	location(t, pin("1111"), "msg=pinwrong")
	location(t, pin("4321"), "msg=lineremoved")
	var undo, viaPIN db.Removal
	for _, r := range e.store.Removals("2000-01-01", "2100-01-01") {
		switch r.Kind {
		case db.RemovedUndo:
			undo = r
		case db.RemovedLine:
			viaPIN = r
		}
	}
	if undo.Qty != 2 || undo.By != "Lisa" || viaPIN.By != "Manager (PIN)" || viaPIN.AskedBy != "Lisa" || viaPIN.Why != "Wrong table" {
		t.Fatalf("written down: undo %+v, PIN %+v", undo, viaPIN)
	}

	// Two more asks: the manager's top bar shows 2 waiting.
	b1, b2 := add(burger, 1), add(soda, 1)
	for _, x := range []db.BillLine{b1, b2} {
		location(t, e.as(lisa, "POST", fmt.Sprintf("/admin/orders/5/lines/%d/ask", x.ID), url.Values{"bill": {billID()}, "qty": {"1"}, "why": {"Added by mistake"}}), "msg=asksent")
	}
	e.expect(e.do("GET", "/admin/day", nil, true), 200, `class="nav-badge"`, ">2<")
	if strings.Contains(e.as(lisa, "GET", "/admin/day", nil).Body.String(), "nav-badge") {
		t.Fatal("waiters see the manager's badge")
	}
	for _, x := range []db.BillLine{b1, b2} {
		location(t, e.do("POST", fmt.Sprintf("/admin/orders/5/lines/%d/approve", x.ID), url.Values{"bill": {billID()}}, true), "msg=lineremoved")
	}
	// 3 taken off in a week (the undo doesn't count): Lisa is marked.
	req := httptest.NewRequest("GET", "http://example.test/owner/charts?month="+e.srv.Now().Format("2006-01"), nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleManager, e.srv.Now())})
	req.Header.Set("Accept-Language", "en")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	e.expect(rec, 200, "Check Lisa: 3 taken off and 0 skipped bills in the last 7 days.", "Many this week", "Undone by the waiter")
}
