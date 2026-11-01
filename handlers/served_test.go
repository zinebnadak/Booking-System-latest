package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The waitress brings a soda; the guest orders a burger on his phone. The chef taps Done: the waiters'
// screens chime "Food ready to pick up" until the food has gone out. The
// kitchen guy taps Served when a waiter takes it: the table is "Served ·
// waiting to pay", and the guest's phone stops saying "Ready".
func TestFoodReadyThenServed(t *testing.T) {
	e := newEnv(t, nil)
	burger, soda := e.menuItem("Ozmo-burgare"), e.menuItem("Läsk 33 cl")
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", soda.ID): {"1"}}, true), "msg=added")
	a := join(t, e, tableCode(t, e, 5), 1)
	a.do("POST", "/g/"+a.key+"/name", url.Values{"name": {"Erik"}})
	if rec := a.do("POST", "/g/"+a.key+"/order", url.Values{fmt.Sprintf("qty_%d", burger.ID): {"1"}}); loc(rec) != "/g/"+a.key+"?msg=ordered" {
		t.Fatalf("phone order: %s", loc(rec))
	}
	tk := e.store.OpenTickets()
	if len(tk) != 1 {
		t.Fatalf("tickets: %+v", tk)
	}
	id := tk[0].ID
	lisa, err := e.store.AddStaff("Lisa", "lisa", "a-long-password-1", e.srv.Now())
	if err != nil {
		t.Fatal(err)
	}
	waiter := &http.Cookie{Name: sessionCookie, Value: e.srv.makeStaffSession(lisa, e.srv.Now())}
	live := func(path string) int {
		req := httptest.NewRequest("POST", "http://example.test"+path, strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Live", "1")
		req.AddCookie(waiter)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Still cooking: nobody can tap Served yet.
	if code := live(fmt.Sprintf("/admin/kitchen/%d/served", id)); code != http.StatusConflict {
		t.Fatalf("served while cooking: %d", code)
	}
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "With the chef")

	// Done: at the pass, the waiters are told, with the guest's name.
	later := e.srv.Now().Add(5 * time.Minute)
	e.srv.Now = func() time.Time { return later }
	if code := live(fmt.Sprintf("/admin/kitchen/%d/done", id)); code != http.StatusNoContent {
		t.Fatalf("done: %d", code)
	}
	key := fmt.Sprintf(`data-alert-key="ready-%d"`, id)
	for _, p := range []string{"/admin/orders?part=tiles", "/admin/tables?part=tiles"} {
		e.expect(e.as(waiter, "GET", p, nil), 200, "Food ready to pick up", "Table 5", "1× Ozmo-burgare", "Erik", key)
	}
	e.expect(e.as(waiter, "GET", "/admin/orders?part=tiles", nil), 200, "ot-ready", "Food ready")
	e.expect(e.as(waiter, "GET", "/admin/kitchen?part=tickets", nil), 200, "Ready at the pass", "Ozmo-burgare", "Served", "Not done yet")
	e.expect(a.do("GET", "/g/"+a.key, nil), 200, `badge-ok">Ready<`)

	// Five minutes at the pass: it turns red.
	cold := later.Add(5 * time.Minute)
	e.srv.Now = func() time.Time { return cold }
	e.expect(e.as(waiter, "GET", "/admin/orders?part=tiles", nil), 200, "rf-late", "ready "+clockOf(later)+" · 5 min")

	// The kitchen guy taps Served (from the kitchen screen).
	if rec := e.as(waiter, "POST", fmt.Sprintf("/admin/kitchen/%d/served", id), url.Values{}); loc(rec) != "/admin/kitchen" {
		t.Fatalf("served: %d %s", rec.Code, loc(rec))
	}
	tiles := e.as(waiter, "GET", "/admin/orders?part=tiles", nil).Body.String()
	if strings.Contains(tiles, key) || strings.Contains(tiles, "Food ready") {
		t.Fatal("still waiting at the pass after Served")
	}
	if !strings.Contains(tiles, "Served · waiting to pay") {
		t.Fatalf("tile doesn't say waiting to pay:\n%s", tiles)
	}
	if strings.Contains(a.do("GET", "/g/"+a.key, nil).Body.String(), `badge-ok">Ready<`) {
		t.Fatal("the guest's phone still says Ready")
	}
	e.expect(e.as(waiter, "GET", "/admin/kitchen?part=tickets", nil), 200, "Served and done", "Undo")

	// Tapped by mistake: Undo puts it back at the pass.
	if code := live(fmt.Sprintf("/admin/kitchen/%d/undo", id)); code != http.StatusNoContent {
		t.Fatalf("undo: %d", code)
	}
	e.expect(e.as(waiter, "GET", "/admin/orders?part=tiles", nil), 200, "Food ready to pick up", key)

	// He pays on his phone and leaves before anyone tapped Served: the
	// list doesn't keep chiming for a table that has gone.
	ref := strings.TrimPrefix(loc(a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"1"}})), "/dev/tab/")
	a.do("POST", "/dev/tab/"+ref, url.Values{"action": {"pay"}})
	if _, open := e.store.OpenBill(5); open {
		t.Fatal("the table should be free")
	}
	if strings.Contains(e.as(waiter, "GET", "/admin/orders?part=tiles", nil).Body.String(), "Food ready to pick up") {
		t.Fatal("a paid table still waits for its food")
	}
}
