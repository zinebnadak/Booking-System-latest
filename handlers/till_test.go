package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// asTill sends a request from a device set up as a till (with an optional login).
func (e *env) asTill(till, login *http.Cookie, method, path string, form url.Values) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "http://example.test"+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept-Language", "en")
	if till != nil {
		req.AddCookie(till)
	}
	if login != nil {
		req.AddCookie(login)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestSharedTillPINAndClockCard(t *testing.T) {
	wrongPasswordDelay = 0
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	e.do("POST", "/admin/staff", url.Values{"name": {"Lisa"}, "username": {"lisa"}, "password": {"secret123"}}, true)
	m := e.store.StaffList()[0]
	if m.No != 1 {
		t.Fatalf("Lisa is waiter %d, want 1", m.No)
	}
	if !strings.Contains(loc(e.do("POST", "/admin/staff/1/pin", url.Values{"pin": {"12a4"}}, true)), "msg=pinbad") {
		t.Fatal("a PIN with a letter was accepted")
	}
	if !strings.Contains(loc(e.do("POST", "/admin/staff/1/pin", url.Values{"pin": {"4711"}}, true)), "msg=pinset") {
		t.Fatal("PIN not saved")
	}

	// On a device that isn't a till, the keypad doesn't take PINs.
	e.expect(e.asTill(nil, nil, "GET", "/till", nil), 200, "a till yet")
	if rec := e.asTill(nil, nil, "POST", "/till", url.Values{"no": {"1"}, "pin": {"4711"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("PIN from a normal device: %d", rec.Code)
	}

	// The manager makes this device a till.
	rec := e.do("POST", "/admin/staff/till", url.Values{"on": {"1"}}, true)
	var till *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == tillCookie {
			till = c
		}
	}
	if till == nil {
		t.Fatal("no till cookie")
	}
	e.expect(e.asTill(till, nil, "GET", "/till", nil), 200, "Servitör", "Clock in")
	// A forged till cookie doesn't work.
	if rec := e.asTill(&http.Cookie{Name: tillCookie, Value: "dGlsbHxmYWtl.abc"}, nil, "POST", "/till", url.Values{"no": {"1"}, "pin": {"4711"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("forged till: %d", rec.Code)
	}

	// Clock in, log in with the PIN, lock, clock out.
	e.expect(e.asTill(till, nil, "POST", "/till", url.Values{"no": {"1"}, "pin": {"4711"}, "action": {"in"}}), 200, "Lisa clocked in at 18:00")
	rec = e.asTill(till, nil, "POST", "/till", url.Values{"no": {"1"}, "pin": {"4711"}, "action": {"login"}})
	if loc(rec) != "/admin/orders" {
		t.Fatalf("PIN login: %d %s", rec.Code, loc(rec))
	}
	var lisa *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			lisa = c
		}
	}
	e.expect(e.asTill(till, lisa, "GET", "/admin/orders", nil), 200, "Lisa", "Lock")
	if rec := e.asTill(till, lisa, "POST", "/till/lock", nil); loc(rec) != "/till" {
		t.Fatalf("lock: %s", loc(rec))
	}
	e.srv.Now = func() time.Time { return saturday.Add(5*time.Hour + 30*time.Minute) }
	e.expect(e.asTill(till, nil, "POST", "/till", url.Values{"no": {"1"}, "pin": {"4711"}, "action": {"out"}}), 200, "Lisa clocked out at 23:30 after 5 h 30 min")
	e.expect(e.do("GET", "/admin/hours?week=2026-10-31", nil, true), 200, "Lisa", "5 h 30 min")

	// Wrong PINs: after 5 for one number, the keypad stops for that number.
	for i := 0; i < 5; i++ {
		if rec := e.asTill(till, nil, "POST", "/till", url.Values{"no": {"1"}, "pin": {"0000"}}); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong PIN %d: %d", i, rec.Code)
		}
	}
	if rec := e.asTill(till, nil, "POST", "/till", url.Values{"no": {"1"}, "pin": {"4711"}}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after 5 wrong PINs: %d", rec.Code)
	}
}
