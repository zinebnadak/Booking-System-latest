package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"ozmo-booking/db"
)

// Every sentence on the staff pages must have a Swedish version.
func TestStaffSwedishComplete(t *testing.T) {
	var keys []string
	tmpl := regexp.MustCompile(`\{\{\s*trf?\s+"((?:[^"\\]|\\.)*)"`)
	files, _ := filepath.Glob("../templates/*.html")
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		for _, m := range tmpl.FindAllStringSubmatch(string(raw), -1) {
			keys = append(keys, strings.ReplaceAll(m[1], `\"`, `"`))
		}
	}
	code := regexp.MustCompile(`\bstf?\((?:lang|langOf\(r\)), "((?:[^"\\]|\\.)*)"`)
	gofiles, _ := filepath.Glob("*.go")
	for _, f := range gofiles {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, _ := os.ReadFile(f)
		for _, m := range code.FindAllStringSubmatch(string(raw), -1) {
			keys = append(keys, m[1])
		}
	}
	for _, f := range flashes {
		keys = append(keys, f)
	}
	for _, f := range guestTabMsgs {
		keys = append(keys, f)
	}
	keys = append(keys, activityWords...)
	for _, s := range []db.Status{db.StatusPending, db.StatusPaid, db.StatusExpired, db.StatusCancelled,
		db.StatusRefunded, db.StatusArrived, db.StatusNoShow, db.StatusPickedUp} {
		keys = append(keys, statusText(s))
	}
	for _, m := range metrics {
		keys = append(keys, m.Label)
	}
	keys = append(keys, "Arrived", "deposit taken off the bill", "Confirmed, not here yet", "Takeaway orders", "No-shows",
		"deposit kept", "Cancelled after paying", "Refunded", "deposit paid back", "Never paid", "left before paying", "new",
		"Booking", "Type", "Booked via", "Date", "Time", "Table", "Guests", "Name", "Phone", "Email", "Status", "Paid",
		"Deposit €", "Food total €", "Items", "Occasion", "Cancelled by", "website", "phone", "walk-in", "guest", "staff")
	if len(keys) < 250 {
		t.Fatalf("only found %d sentences, the scan is broken", len(keys))
	}
	for _, k := range keys {
		if _, ok := staffSV[k]; !ok {
			t.Errorf("no Swedish for %q", k)
		}
	}
}

func TestStaffPagesInSwedish(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Cfg.OwnerUser, e.srv.Cfg.OwnerPassword = "owner", "boss"
	e.h = e.srv.Routes()
	e.bookAndPay(1, "2026-10-26", "18:00", "2")
	get := func(path string, who role) string {
		req := httptest.NewRequest("GET", "http://example.test"+path, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(who, e.srv.Now())})
		req.AddCookie(&http.Cookie{Name: langCookie, Value: "sv"})
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		return rec.Body.String()
	}
	checks := map[string][]string{
		"/admin/day":                  {"Bordsbokningar", "Borden", "måndag 26 oktober", "Bekräftad", "Telefonbokning", "Logga ut", `lang="sv"`},
		"/admin/calendar":             {"Oktober 2026", "mån", "I din telefon"},
		"/admin/menu":                 {"Dryck med alkohol (25,5 % moms)", "Lägg till rätt"},
		"/admin/closed":               {"Stängda dagar", "Stäng en dag"},
		"/owner?date=2026-10-26":      {"Intäkter i dag", "jämfört med måndag förra veckan"},
		"/owner/charts?month=2026-10": {"Dag för dag", "Mest sålda rätter", "Oktober 2026", "Pengar"},
	}
	for path, want := range checks {
		body := get(path, roleOwner)
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", path, w)
			}
		}
		for _, english := range []string{"Table bookings", "Log out", "Made today", "Day by day", "Closed days</h1>"} {
			if strings.Contains(body, english) {
				t.Errorf("%s: still has English %q", path, english)
			}
		}
	}
	// The switch goes back to English.
	body := get("/admin/day?lang=en", roleManager)
	if !strings.Contains(body, "Table bookings") || !strings.Contains(body, `href="/admin/day?lang=sv"`) {
		t.Fatal("English switch doesn't work on staff pages")
	}
}

func TestEuroThousands(t *testing.T) {
	for cents, want := range map[int]string{0: "0,00 €", 1450: "14,50 €", 99999: "999,99 €",
		564810: "5 648,10 €", 123456789: "1 234 567,89 €", -564810: "−5 648,10 €"} {
		if got := euro(cents); got != want {
			t.Errorf("euro(%d) = %q, want %q", cents, got, want)
		}
	}
}
