package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"emeal-booking/db"
)

// from sends a request as if it came from the visitor address ip.
func (e *env) from(ip, method, path string, form url.Values, headers ...string) *httptest.ResponseRecorder {
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
	req.RemoteAddr = ip + ":5555"
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func noLoginDelay(t *testing.T) {
	old := wrongPasswordDelay
	wrongPasswordDelay = 0
	t.Cleanup(func() { wrongPasswordDelay = old })
}

func TestLoginIsRateLimited(t *testing.T) {
	noLoginDelay(t)
	e := newEnv(t, nil)
	login := func(ip, pass string) *httptest.ResponseRecorder {
		return e.from(ip, "POST", "/login", url.Values{"username": {"admin"}, "password": {pass}})
	}
	for i := 0; i < limitLogin.n; i++ {
		e.expect(login("203.0.113.9", fmt.Sprintf("guess%d", i)), http.StatusUnauthorized)
	}
	// Used up: even the right password is not checked from this address now.
	e.expect(login("203.0.113.9", "pw"), http.StatusTooManyRequests, "Please wait a moment")
	// Someone else (the real manager) can still log in.
	if rec := login("198.51.100.7", "pw"); rec.Code != http.StatusSeeOther {
		t.Fatalf("manager from another address: %d", rec.Code)
	}
	// After the window the address may try again.
	e.at(fixedNow.Add(limitLogin.window + time.Minute))
	if rec := login("203.0.113.9", "pw"); rec.Code != http.StatusSeeOther {
		t.Fatalf("after waiting: %d", rec.Code)
	}
}

func TestRightPasswordsDontUseUpTheLimit(t *testing.T) {
	noLoginDelay(t)
	e := newEnv(t, nil)
	for i := 0; i < limitLogin.n+5; i++ {
		rec := e.from("192.0.2.50", "POST", "/login", url.Values{"username": {"admin"}, "password": {"pw"}})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("login %d: %d", i, rec.Code)
		}
	}
}

func TestIPv6VisitorsCountPerBlock(t *testing.T) {
	noLoginDelay(t)
	e := newEnv(t, nil)
	for i := 0; i < limitLogin.n; i++ {
		// A new address inside the same /64 every time.
		ip := fmt.Sprintf("[2001:db8:1:2::%x]", i+1)
		e.expect(e.from(ip, "POST", "/login", url.Values{"username": {"admin"}, "password": {"x"}}), http.StatusUnauthorized)
	}
	e.expect(e.from("[2001:db8:1:2::ffff]", "POST", "/login", url.Values{"username": {"admin"}, "password": {"x"}}), http.StatusTooManyRequests)
}

func TestForwardedForOnlyBehindProxy(t *testing.T) {
	e := newEnv(t, nil)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Forwarded-For", "1.1.1.1, 9.9.9.9")
	if got := e.srv.clientIP(req); got != "192.0.2.1" {
		t.Fatalf("without TRUST_PROXY the header must be ignored, got %s", got)
	}
	e.srv.Cfg.TrustProxy = true
	// The last address is the one our proxy added; the first is made up by the visitor.
	if got := e.srv.clientIP(req); got != "9.9.9.9" {
		t.Fatalf("behind a proxy: got %s", got)
	}
}

func TestBookingFloodIsStopped(t *testing.T) {
	e := newEnv(t, nil)
	clocks := []string{"15:00", "15:30", "16:00", "16:30", "17:00", "17:30", "18:00"}
	for i := 0; i < limitBooking.n; i++ {
		rec := e.from("203.0.113.20", "POST", "/book", guestForm(100+i, clocks[i], "2"))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("booking %d: %d", i, rec.Code)
		}
	}
	e.expect(e.from("203.0.113.20", "POST", "/book", guestForm(200, "18:00", "2")), http.StatusTooManyRequests, "Please wait a moment")
	// Takeaway orders share the same limit, so they can't be used instead.
	ta := url.Values{"date": {"2026-10-26"}, "time": {"17:00"}, "name": {"X"}, "email": {"x@example.com"}, "phone": {"+358 40 111 2222"}}
	ta.Set(fmt.Sprintf("qty_%d", e.store.Menu(true)[0].ID), "1")
	e.expect(e.from("203.0.113.20", "POST", "/takeaway", ta), http.StatusTooManyRequests)
	// Other guests are not affected.
	if rec := e.from("198.51.100.30", "POST", "/book", guestForm(300, "18:00", "2")); rec.Code != http.StatusSeeOther {
		t.Fatalf("another guest: %d", rec.Code)
	}
}

func TestUnpaidBookingsPerGuestAreCapped(t *testing.T) {
	e := newEnv(t, nil)
	// Same guest, three different days, never paying (each from a new address).
	for i, date := range []string{"2026-10-26", "2026-10-27", "2026-10-28"} {
		f := bookingForm("18:00", "2")
		f.Set("date", date)
		rec := e.from(fmt.Sprintf("192.0.2.%d", 10+i), "POST", "/book", f)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("booking %s: %d", date, rec.Code)
		}
	}
	// A fourth unpaid booking for the same email is refused...
	ta := url.Values{"date": {"2026-10-26"}, "time": {"17:00"}, "name": {"Anna"}, "email": {"ANNA@example.com"}, "phone": {"+358 50 999 9999"}}
	ta.Set(fmt.Sprintf("qty_%d", e.store.Menu(true)[0].ID), "1")
	e.expect(e.from("192.0.2.40", "POST", "/takeaway", ta), http.StatusTooManyRequests, "You already have unpaid bookings")
	// ...and for the same phone number with another email.
	f := bookingForm("16:00", "2")
	f.Set("email", "other@example.com")
	e.expect(e.from("192.0.2.41", "POST", "/book", f), http.StatusTooManyRequests, "unpaid bookings")
	// Starting again at an overlapping time replaces the old hold, so that still works.
	if rec := e.from("192.0.2.42", "POST", "/book", bookingForm("17:30", "2")); rec.Code != http.StatusSeeOther {
		t.Fatalf("restarting the same booking: %d", rec.Code)
	}
	// Once the holds run out, the guest can book again.
	e.at(fixedNow.Add(36 * time.Minute))
	if rec := e.from("192.0.2.43", "POST", "/book", bookingForm("18:00", "2")); rec.Code != http.StatusSeeOther {
		t.Fatalf("after holds expired: %d", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do("GET", "/", nil, false)
	h := rec.Header()
	for k, want := range map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "same-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
	} {
		if h.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, h.Get(k), want)
		}
	}
	csp := h.Get("Content-Security-Policy")
	for _, part := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "form-action 'self' https://checkout.stripe.com", "font-src 'self'"} {
		if !strings.Contains(csp, part) {
			t.Errorf("CSP missing %q: %s", part, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "googleapis") {
		t.Errorf("CSP too loose: %s", csp)
	}
	if h.Get("Strict-Transport-Security") != "" {
		t.Error("no HSTS on a plain http:// site")
	}
	// Every inline script on the page must be allowed by its fingerprint,
	// otherwise the browser would block it.
	body := rec.Body.Bytes()
	scripts := regexp.MustCompile(`(?s)<script(?: type="speculationrules")?>(.*?)</script>`).FindAllSubmatch(body, -1)
	if len(scripts) < 2 {
		t.Fatalf("expected the two inline scripts, found %d", len(scripts))
	}
	for _, m := range scripts {
		sum := sha256.Sum256(m[1])
		if !strings.Contains(csp, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") {
			t.Errorf("inline script not allowed by CSP: %.60q", m[1])
		}
	}
	// No inline event handlers (onclick=...), which the CSP would block.
	for _, page := range []string{"/", "/book", "/takeaway", "/admin/day", "/admin/calendar", "/admin/menu", "/admin/closed"} {
		b := e.do("GET", page, nil, true).Body.String()
		if m := regexp.MustCompile(`\son[a-z]+="`).FindString(b); m != "" {
			t.Errorf("%s has an inline handler %q", page, m)
		}
		if strings.Contains(b, "fonts.googleapis.com") || strings.Contains(b, "fonts.gstatic.com") {
			t.Errorf("%s still loads Google Fonts", page)
		}
	}
	// HTTPS sites also get HSTS.
	e.srv.Cfg.BaseURL = "https://book.example.test"
	e.h = e.srv.Routes()
	if got := e.do("GET", "/", nil, false).Header().Get("Strict-Transport-Security"); !strings.HasPrefix(got, "max-age=") {
		t.Errorf("HSTS missing on https: %q", got)
	}
}

func TestGuestPagesAreNotCached(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do("POST", "/book", bookingForm("18:00", "2"), false)
	ref := refFromLocation(t, rec.Header().Get("Location"))
	for _, p := range []string{"/b/" + ref, "/dev/pay/" + ref, "/login"} {
		if got := e.do("GET", p, nil, false).Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q", p, got)
		}
	}
}

func TestCSVExportCantRunFormulas(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Cfg.OwnerUser, e.srv.Cfg.OwnerPassword = "owner", "boss"
	f := bookingForm("18:00", "2")
	f.Set("name", `=HYPERLINK("http://evil.example","Click")`)
	f.Set("email", `=cmd|'/Ccalc'!A0@evil.example.com`)
	rec := e.do("POST", "/book", f, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("booking: %d %s", rec.Code, rec.Body.String())
	}
	ref := refFromLocation(t, rec.Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)

	req := httptest.NewRequest("GET", "http://example.test/owner/export.csv?month=2026-10", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleOwner, e.srv.Now())})
	out := httptest.NewRecorder()
	e.h.ServeHTTP(out, req)
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(out.Body.Bytes(), []byte("\xEF\xBB\xBF"))))
	r.Comma = ';'
	rows, err := r.ReadAll()
	if err != nil || len(rows) < 2 {
		t.Fatalf("csv: %v %d rows", err, len(rows))
	}
	for _, row := range rows[1:] {
		for _, cell := range row {
			if cell != "" && strings.ContainsRune("=+-@", rune(cell[0])) && !isPlainPhone(cell) {
				t.Errorf("cell could run as a formula: %q", cell)
			}
		}
	}
	if !strings.Contains(out.Body.String(), `'=HYPERLINK`) {
		t.Error("name should be kept, as text")
	}
}

func isPlainPhone(s string) bool { return csvPhone(s) == s && !strings.ContainsAny(s, "=@") }

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{
		"Anna":            "Anna",
		"=1+1":            "'=1+1",
		"+1+1":            "'+1+1",
		"-2":              "'-2",
		"@SUM(A1)":        "'@SUM(A1)",
		"\t=1":            "'\t=1",
		"":                "",
		"anna@example.ax": "anna@example.ax",
	} {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
	if got := csvPhone("+358 40 123 4567"); got != "+358 40 123 4567" {
		t.Errorf("ordinary phone changed: %q", got)
	}
	if got := csvPhone("+cmd|calc"); got != "'+cmd|calc" {
		t.Errorf("odd phone not escaped: %q", got)
	}
}

func TestLogoutNeedsOurOwnSite(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.from("192.0.2.1", "POST", "/logout", url.Values{}, "Origin", "https://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site logout: %d", rec.Code)
	}
	rec = e.from("192.0.2.1", "POST", "/logout", url.Values{}, "Origin", "http://example.test")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("own logout: %d", rec.Code)
	}
}

func TestMenuTextIsCapped(t *testing.T) {
	e := newEnv(t, nil)
	long := strings.Repeat("Å", 1000)
	rec := e.do("POST", "/admin/menu", url.Values{"name": {long}, "category": {long}, "description": {long}, "tags": {long}, "price": {"12,50"}, "available": {"on"}}, true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", rec.Code)
	}
	var found *db.MenuItem
	for _, m := range e.store.Menu(false) {
		if strings.HasPrefix(m.Name, "ÅÅÅ") {
			m := m
			found = &m
		}
	}
	if found == nil {
		t.Fatal("dish not saved")
	}
	for name, got := range map[string]int{"name": len([]rune(found.Name)), "category": len([]rune(found.Category)), "description": len([]rune(found.Description)), "tags": len([]rune(found.Tags))} {
		if got > 300 {
			t.Errorf("%s kept %d letters", name, got)
		}
	}
}
