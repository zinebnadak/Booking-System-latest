package handlers

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// as sends a request with a given session cookie.
func (e *env) as(cookie *http.Cookie, method, path string, form url.Values) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, "http://example.test"+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept-Language", "en")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) loginAs(t *testing.T, user, pass string) (*http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	rec := e.as(nil, "POST", "/login", url.Values{"username": {user}, "password": {pass}})
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c, rec
		}
	}
	return nil, rec
}

func TestWaitersHaveTheirOwnLogin(t *testing.T) {
	wrongPasswordDelay = 0
	e := newEnv(t, nil)
	// The manager adds Lisa.
	if rec := e.do("POST", "/admin/staff", url.Values{"name": {"Lisa"}, "username": {"lisa"}, "password": {"short"}}, true); !strings.Contains(loc(rec), "msg=staffbad") {
		t.Fatalf("short password: %s", loc(rec))
	}
	if rec := e.do("POST", "/admin/staff", url.Values{"name": {"Lisa"}, "username": {"Lisa"}, "password": {"secret123"}}, true); !strings.Contains(loc(rec), "msg=staffadded") {
		t.Fatalf("add: %s", loc(rec))
	}
	e.expect(e.do("GET", "/admin/staff", nil, true), 200, "Lisa", "lisa")

	// Lisa logs in: straight to Orders, with her name at the top.
	lisa, rec := e.loginAs(t, "lisa", "secret123")
	if lisa == nil || loc(rec) != "/admin/orders" {
		t.Fatalf("login: %d %s", rec.Code, loc(rec))
	}
	page := e.as(lisa, "GET", "/admin/orders", nil)
	e.expect(page, 200, "Lisa")
	if strings.Contains(page.Body.String(), `href="/admin/menu"`) {
		t.Fatal("waiters see the Menu link")
	}
	// She can use the tables, kitchen and bookings, and mark a dish sold out...
	for _, p := range []string{"/admin/orders", "/admin/kitchen", "/admin/day", "/admin/orders/3"} {
		if rec := e.as(lisa, "GET", p, nil); rec.Code != 200 {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}
	lax := menuID(t, e, "Laxfilé")
	if rec := e.as(lisa, "POST", "/admin/menu/"+lax+"/soldout", url.Values{"soldout": {"1"}}); rec.Code != 303 {
		t.Fatalf("sold out: %d", rec.Code)
	}
	// ...but not the menu, closed days, staff, activity or the money pages.
	for _, p := range []string{"/admin/menu", "/admin/closed", "/admin/staff", "/admin/activity"} {
		if rec := e.as(lisa, "GET", p, nil); rec.Code != 403 {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}
	if rec := e.as(lisa, "POST", "/admin/menu", url.Values{"id": {lax}, "name": {"x"}, "category": {"y"}, "price": {"1"}}); rec.Code != 403 {
		t.Fatalf("editing the menu: %d", rec.Code)
	}
	if rec := e.as(lisa, "POST", "/admin/staff", url.Values{"name": {"Me"}, "username": {"me"}, "password": {"12345678"}}); rec.Code != 403 {
		t.Fatalf("adding staff: %d", rec.Code)
	}
	if rec := e.as(lisa, "GET", "/owner", nil); rec.Code != 403 {
		t.Fatalf("owner page: %d", rec.Code)
	}

	// What she does with money is recorded with her name.
	burger := menuID(t, e, "Ozmo-burgare")
	e.as(lisa, "POST", "/admin/orders/2/add", url.Values{"bill": {"0"}, "qty_" + burger: {"2"}})
	b, _ := e.store.OpenBill(2)
	e.as(lisa, "POST", fmt.Sprintf("/admin/orders/2/lines/%d/save", b.Lines[0].ID), url.Values{"bill": {fmt.Sprint(b.ID)}, "price": {"15,00"}, "qty": {"2"}})
	e.as(lisa, "POST", "/admin/orders/2/pay", url.Values{"bill": {fmt.Sprint(b.ID)}, "method": {"cash"}})
	act := e.do("GET", "/admin/activity?date=2026-10-26", nil, true)
	e.expect(act, 200, "Lisa", "changed a price", "Ozmo-burgare: 2× 19,90 € → 2× 15,00 €", "paid in cash", "30,00 €", "Table 2")

	// A new password logs her out everywhere at once.
	id := e.store.StaffList()[0].ID
	e.do("POST", fmt.Sprintf("/admin/staff/%d/password", id), url.Values{"password": {"another123"}}, true)
	if rec := e.as(lisa, "GET", "/admin/orders", nil); rec.Code != 303 || !strings.HasPrefix(loc(rec), "/login") {
		t.Fatalf("old login after a new password: %d %s", rec.Code, loc(rec))
	}
	if c, _ := e.loginAs(t, "lisa", "secret123"); c != nil {
		t.Fatal("old password still works")
	}
	lisa, _ = e.loginAs(t, "lisa", "another123")
	if lisa == nil {
		t.Fatal("new password refused")
	}
	// Removed: logged out, can't log in again, still in the activity list.
	e.do("POST", fmt.Sprintf("/admin/staff/%d/remove", id), url.Values{}, true)
	if rec := e.as(lisa, "GET", "/admin/orders", nil); rec.Code != 303 {
		t.Fatalf("removed waiter still logged in: %d", rec.Code)
	}
	if c, _ := e.loginAs(t, "lisa", "another123"); c != nil {
		t.Fatal("removed waiter can log in")
	}
	e.expect(e.do("GET", "/admin/activity?date=2026-10-26", nil, true), 200, "Lisa", "removed a waiter")
	// The manager's and head manager's own logins work as before.
	if c, _ := e.loginAs(t, "admin", "pw"); c == nil {
		t.Fatal("manager login broken")
	}
}
