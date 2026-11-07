package handlers

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"emeal-booking/db"
	"emeal-booking/mailer"
	"emeal-booking/payments"
	"emeal-booking/schedule"
)

// Monday 26 Oct 2026, 09:00 in Mariehamn.
var fixedNow = time.Date(2026, 10, 26, 9, 0, 0, 0, schedule.Location)

type env struct {
	t     *testing.T
	srv   *Server
	h     http.Handler
	store *db.Store
}

func newEnv(t *testing.T, pay *payments.Client) *env {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		BaseURL: "http://example.test", RestaurantName: "E-Meal Kitchen",
		ManagerEmail: "manager@example.test", AdminUser: "admin", AdminPassword: "pw", CalendarToken: "tok",
		TableButtons: true, TableQR: true, // the tests cover them; offTest switches them off
	}
	if pay == nil {
		pay = &payments.Client{}
	}
	s, err := New(cfg, store, pay, &mailer.Mailer{}, os.DirFS(".."))
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return fixedNow }
	return &env{t: t, srv: s, h: s.Routes(), store: store}
}

func (e *env) do(method, path string, form url.Values, admin bool) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, "http://example.test"+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// Each guest comes from their own address, like real visitors, so
		// tests with many guests stay inside the per-address rate limits.
		if email := form.Get("email"); email != "" {
			h := fnv.New32a()
			h.Write([]byte(email))
			n := h.Sum32()
			req.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:1234", n>>16&255, n>>8&255, n&255)
		}
	}
	if admin {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleManager, e.srv.Now())})
	}
	// Tests read the English pages unless they ask for Swedish with ?lang=sv.
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) expect(rec *httptest.ResponseRecorder, code int, contains ...string) {
	e.t.Helper()
	if rec.Code != code {
		e.t.Fatalf("status %d, want %d\n%s", rec.Code, code, rec.Body.String())
	}
	for _, c := range contains {
		if !strings.Contains(rec.Body.String(), c) {
			e.t.Fatalf("response missing %q\n%s", c, rec.Body.String())
		}
	}
}

func bookingForm(clock, size string) url.Values {
	return url.Values{
		"date": {"2026-10-26"}, "time": {clock}, "size": {size},
		"name": {"Anna Svensson"}, "email": {"anna@example.com"}, "phone": {"+358 40 123 4567"}, "notes": {"Window please"},
	}
}

// guestForm is a booking form for guest number i (different email and phone).
func guestForm(i int, clock, size string) url.Values {
	f := bookingForm(clock, size)
	f.Set("email", fmt.Sprintf("guest%d@example.com", i))
	f.Set("phone", fmt.Sprintf("+358 40 000 %04d", i))
	return f
}

func refFromLocation(t *testing.T, loc string) string {
	t.Helper()
	parts := strings.Split(strings.TrimRight(loc, "/"), "/")
	return parts[len(parts)-1]
}

func TestPagesRender(t *testing.T) {
	e := newEnv(t, nil)
	e.expect(e.do("GET", "/", nil, false), 200, "Book a table", "Monday–Thursday", "Sunday")
	// No date picked: starts on the first open day (today) and shows times grouped.
	e.expect(e.do("GET", "/book", nil, false), 200, "How many guests?", "Lunch", "Afternoon", "Evening", "10:30")
	e.expect(e.do("GET", "/book?date=2026-10-26&size=4", nil, false), 200, "10:30", "19:00", "20,00 €")
	closeSundays(t) // the restaurant is open on Sundays: pretend it isn't, to see the "closed" message
	e.expect(e.do("GET", "/book?date=2026-11-01&size=2", nil, false), 200, "closed on Sunday 1 November")
	e.expect(e.do("GET", "/book?date=2026-10-20&size=2", nil, false), 200, "already passed")
	e.expect(e.do("GET", "/takeaway", nil, false), 200, "Fisksoppa", "Förrätter", "11:00")
	e.expect(e.do("GET", "/static/style.css", nil, false), 200, "--brass")
	if strings.Contains(e.do("GET", "/takeaway", nil, false).Body.String(), "<select") {
		t.Fatal("takeaway page should have no <select> dropdowns")
	}
	e.expect(e.do("GET", "/nope", nil, false), 404)
}

func TestTableBookingDemoFlow(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do("POST", "/book", bookingForm("18:00", "2"), false)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "/dev/pay/") {
		t.Fatalf("expected redirect to demo payment, got %d %s", rec.Code, rec.Header().Get("Location"))
	}
	ref := refFromLocation(t, rec.Header().Get("Location"))

	e.expect(e.do("GET", "/b/"+ref, nil, false), 200, "Almost done")
	e.expect(e.do("GET", "/dev/pay/"+ref, nil, false), 200, "10,00 €")
	if rec := e.do("POST", "/dev/pay/"+ref, url.Values{}, false); rec.Code != 303 {
		t.Fatalf("demo pay: %d", rec.Code)
	}
	e.expect(e.do("GET", "/b/"+ref, nil, false), 200, "Your table is booked", "18:00–20:00", "Window please")

	b, _ := e.store.Get(ref)
	if b.Status != db.StatusPaid || b.DepositCents != 1000 {
		t.Fatalf("booking = %+v", b)
	}
	// Manager sees it, and it's in the calendar feed.
	e.expect(e.do("GET", "/admin/day?date=2026-10-26", nil, true), 200, "Anna Svensson", "Confirmed")
	e.expect(e.do("GET", "/admin/calendar?month=2026-10", nil, true), 200, "1 tbl · 2 ppl")
	e.expect(e.do("GET", "/calendar/tok.ics", nil, false), 200, "BEGIN:VEVENT", "Table for 2")
	e.expect(e.do("GET", "/calendar/wrong.ics", nil, false), 404)
}

func TestValidationErrors(t *testing.T) {
	e := newEnv(t, nil)
	f := bookingForm("18:00", "2")
	f.Set("email", "not-an-email")
	f.Set("phone", "12")
	e.expect(e.do("POST", "/book", f, false), 422, "valid email", "phone number so we can reach you")

	f = bookingForm("18:15", "2") // not a real slot
	e.expect(e.do("POST", "/book", f, false), 422, "Please pick a time")

	f = bookingForm("18:00", "9")
	e.expect(e.do("POST", "/book", f, false), 422)
}

func TestTablesFillUp(t *testing.T) {
	e := newEnv(t, nil)
	for i := 0; i < schedule.TableCount(); i++ {
		if rec := e.do("POST", "/book", guestForm(i, "18:00", "3"), false); rec.Code != 303 {
			t.Fatalf("booking %d failed: %d", i, rec.Code)
		}
	}
	e.expect(e.do("POST", "/book", guestForm(10, "18:00", "3"), false), 409, "just taken")
	e.expect(e.do("POST", "/book", guestForm(11, "19:00", "3"), false), 409) // overlaps 18:00
	if rec := e.do("POST", "/book", guestForm(12, "16:00", "3"), false); rec.Code != 303 {
		t.Fatalf("16:00 should still be free, got %d", rec.Code)
	}
	e.expect(e.do("GET", "/book?date=2026-10-26&size=3", nil, false), 200, "Full")
}

func TestTakeawayFlow(t *testing.T) {
	e := newEnv(t, nil)
	menu := e.store.Menu(true)
	f := url.Values{
		"date": {"2026-10-26"}, "time": {"12:30"},
		"name": {"Erik"}, "email": {"erik@example.com"}, "phone": {"0401234567"},
		fmt.Sprintf("qty_%d", menu[0].ID): {"2"}, // 2 × 14,50
		fmt.Sprintf("qty_%d", menu[2].ID): {"1"}, // 1 × 15,50
	}
	rec := e.do("POST", "/takeaway", f, false)
	if rec.Code != 303 {
		t.Fatalf("takeaway: %d %s", rec.Code, rec.Body.String())
	}
	ref := refFromLocation(t, rec.Header().Get("Location"))
	b, _ := e.store.Get(ref)
	if b.TotalCents != 4450 || b.DepositCents != 1113 || b.RemainingCents() != 3337 {
		t.Fatalf("totals: %d / %d / %d", b.TotalCents, b.DepositCents, b.RemainingCents())
	}
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	e.expect(e.do("GET", "/b/"+ref, nil, false), 200, "Your order is confirmed", "33,37 €")

	// Empty order and bad pickup time are rejected.
	e.expect(e.do("POST", "/takeaway", url.Values{"date": {"2026-10-26"}, "time": {"12:30"}, "name": {"x"}, "email": {"x@y.fi"}, "phone": {"0401234567"}}, false), 422, "at least one dish")
	f.Set("time", "09:00")
	e.expect(e.do("POST", "/takeaway", f, false), 422, "pickup day and time")
}

func TestAdminAuthAndCancel(t *testing.T) {
	e := newEnv(t, nil)
	// Not logged in: sent to the login page.
	if rec := e.do("GET", "/admin/day", nil, false); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/login?next=") {
		t.Fatalf("not logged in: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	// A booking 3 days ahead, paid, then cancelled with refund.
	f := bookingForm("18:00", "4")
	f.Set("date", "2026-10-29")
	ref := refFromLocation(t, e.do("POST", "/book", f, false).Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	e.expect(e.do("GET", "/admin/day?date=2026-10-29", nil, true), 200, "Cancel and refund", "policy says refund")

	rec := e.do("POST", "/admin/bookings/"+ref+"/cancel", url.Values{"refund": {"yes"}}, true)
	if rec.Code != 303 {
		t.Fatalf("cancel: %d", rec.Code)
	}
	b, _ := e.store.Get(ref)
	if b.Status != db.StatusRefunded {
		t.Fatalf("status = %s", b.Status)
	}
	e.expect(e.do("GET", "/b/"+ref, nil, false), 200, "has been refunded")

	// Cross-site form posts are blocked.
	req := httptest.NewRequest("POST", "http://example.test/admin/closed", strings.NewReader("date=2026-12-24"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleManager, e.srv.Now())})
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("cross-site post got %d, want 403", rr.Code)
	}
}

func TestAdminMenuAndClosedDays(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do("POST", "/admin/menu", url.Values{"id": {"0"}, "category": {"Desserter"}, "name": {"Åländsk pannkaka"}, "price": {"9,50"}, "available": {"on"}, "sort": {"500"}}, true)
	if rec.Code != 303 {
		t.Fatalf("add dish: %d", rec.Code)
	}
	e.expect(e.do("GET", "/takeaway", nil, false), 200, "Åländsk pannkaka", "9,50 €")
	e.expect(e.do("GET", "/admin/menu", nil, true), 200, "Åländsk pannkaka")

	e.do("POST", "/admin/closed", url.Values{"date": {"2026-10-27"}, "reason": {"Private event"}}, true)
	e.expect(e.do("GET", "/book?date=2026-10-27&size=2", nil, false), 200, "Private event")
	e.expect(e.do("GET", "/admin/closed", nil, true), 200, "Private event")
}

// fakeStripe pretends to be api.stripe.com.
type fakeStripe struct {
	mu       sync.Mutex
	sessions map[string]string // session id -> booking ref
	refunds  []string
}

func (f *fakeStripe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.ParseForm()
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/checkout/sessions":
		id := fmt.Sprintf("cs_test_%d", len(f.sessions)+1)
		f.sessions[id] = r.FormValue("client_reference_id")
		json.NewEncoder(w).Encode(map[string]string{"id": id, "url": "https://checkout.stripe.test/" + id})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/checkout/sessions/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/checkout/sessions/")
		json.NewEncoder(w).Encode(map[string]string{"id": id, "payment_status": "paid", "client_reference_id": f.sessions[id], "payment_intent": "pi_" + id})
	case r.Method == "POST" && r.URL.Path == "/v1/refunds":
		f.refunds = append(f.refunds, r.FormValue("payment_intent"))
		w.Write([]byte(`{"id":"re_1"}`))
	default:
		http.Error(w, `{"error":{"message":"unknown"}}`, 404)
	}
}

func TestStripeFlow(t *testing.T) {
	fs := &fakeStripe{sessions: map[string]string{}}
	api := httptest.NewServer(fs)
	defer api.Close()
	e := newEnv(t, &payments.Client{SecretKey: "sk_test_x", WebhookSecret: "whsec_x", APIBase: api.URL})

	// 1. Guest books -> sent to Stripe checkout.
	f := bookingForm("18:00", "2")
	f.Set("date", "2026-10-29")
	rec := e.do("POST", "/book", f, false)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "https://checkout.stripe.test/") {
		t.Fatalf("expected Stripe redirect, got %d %s", rec.Code, rec.Header().Get("Location"))
	}
	var ref string
	for _, r := range fs.sessions {
		ref = r
	}

	// 2. Stripe sends a signed webhook -> booking confirmed.
	payload := []byte(fmt.Sprintf(`{"id":"evt_1","type":"checkout.session.completed","data":{"object":{"id":"cs_test_1","payment_status":"paid","client_reference_id":%q,"payment_intent":"pi_123"}}}`, ref))
	req := httptest.NewRequest("POST", "/stripe/webhook", strings.NewReader(string(payload)))
	req.Header.Set("Stripe-Signature", payments.SignForTest(payload, "whsec_x", fixedNow))
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("webhook: %d %s", rr.Code, rr.Body.String())
	}
	b, _ := e.store.Get(ref)
	if b.Status != db.StatusPaid || b.PaymentIntentID != "pi_123" {
		t.Fatalf("after webhook: %+v", b)
	}

	// A forged webhook is rejected.
	req = httptest.NewRequest("POST", "/stripe/webhook", strings.NewReader(string(payload)))
	req.Header.Set("Stripe-Signature", payments.SignForTest(payload, "whsec_wrong", fixedNow))
	rr = httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != 400 {
		t.Fatalf("forged webhook got %d", rr.Code)
	}

	// 3. Refund goes to Stripe.
	e.do("POST", "/admin/bookings/"+ref+"/cancel", url.Values{"refund": {"yes"}}, true)
	if len(fs.refunds) != 1 || fs.refunds[0] != "pi_123" {
		t.Fatalf("refunds = %v", fs.refunds)
	}

	// 4. Second booking confirmed via the return page (no webhook needed).
	f.Set("time", "12:00")
	e.do("POST", "/book", f, false)
	var ref2, sid2 string
	for id, r := range fs.sessions {
		if r != ref {
			ref2, sid2 = r, id
		}
	}
	e.expect(e.do("GET", "/b/"+ref2+"?session_id="+sid2, nil, false), 200, "Your table is booked")

	// Demo pay page is disabled when Stripe is on.
	e.expect(e.do("GET", "/dev/pay/"+ref2, nil, false), 404)
}

func TestTakeawayNeverRefunded(t *testing.T) {
	e := newEnv(t, nil)
	menu := e.store.Menu(true)
	f := url.Values{
		"date": {"2026-10-26"}, "time": {"12:30"},
		"name": {"Erik"}, "email": {"erik@example.com"}, "phone": {"0401234567"},
		fmt.Sprintf("qty_%d", menu[0].ID): {"1"},
	}
	ref := refFromLocation(t, e.do("POST", "/takeaway", f, false).Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	e.expect(e.do("GET", "/admin/day?date=2026-10-26", nil, true), 200, "Cancel order", "non-refundable")

	// Even a hand-crafted refund=yes must not refund a takeaway.
	e.do("POST", "/admin/bookings/"+ref+"/cancel", url.Values{"refund": {"yes"}}, true)
	b, _ := e.store.Get(ref)
	if b.Status != db.StatusCancelled {
		t.Fatalf("takeaway status = %s, want cancelled (deposit kept)", b.Status)
	}
}

func TestSwedish(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do("GET", "/?lang=sv", nil, false)
	e.expect(rec, 200, `lang="sv"`, "Boka bord", "Måndag–torsdag", "Söndag", "köket till 20:00")
	if c := rec.Header().Get("Set-Cookie"); !strings.Contains(c, "lang=sv") {
		t.Fatalf("language cookie not set: %q", c)
	}
	e.expect(e.do("GET", "/book?lang=sv&date=2026-10-26&size=3", nil, false), 200, "Hur många är ni?", "Eftermiddag", "Kväll", "Betala handpenning", "Idag")
	e.expect(e.do("GET", "/takeaway?lang=sv", nil, false), 200, "Beställ avhämtning", "Välj mat", "När vill du hämta?")

	// A Swedish booking: Swedish errors, then Swedish status page.
	f := bookingForm("18:00", "2")
	f.Set("email", "nope")
	e.expect(e.do("POST", "/book?lang=sv", f, false), 422, "Skriv en giltig e-postadress")
	f.Set("email", "anna@example.com")
	ref := refFromLocation(t, e.do("POST", "/book?lang=sv", f, false).Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	e.expect(e.do("GET", "/b/"+ref+"?lang=sv", nil, false), 200, "Ditt bord är bokat", "Måndag 26 oktober")
	b, _ := e.store.Get(ref)
	if b.Lang != "sv" {
		t.Fatalf("booking language = %q, want sv", b.Lang)
	}
	subj, body := confirmationEmail(e.srv.Cfg, b)
	if !strings.Contains(subj, "Ditt bord är bokat") || !strings.Contains(body, "Hej Anna") {
		t.Fatalf("Swedish email wrong:\n%s\n%s", subj, body)
	}
}

// Every word key used in a template must exist in both languages.
func TestTranslationsComplete(t *testing.T) {
	for k := range dictEN {
		if _, ok := dictSV[k]; !ok {
			t.Errorf("Swedish is missing %q", k)
		}
	}
	for k := range dictSV {
		if _, ok := dictEN[k]; !ok {
			t.Errorf("English is missing %q", k)
		}
	}
	re := regexp.MustCompile(`\$?t?\.T\.([a-z_0-9]+)|\$t\.([a-z_0-9]+)`)
	files, _ := filepath.Glob("../templates/*.html")
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
			key := m[1] + m[2]
			if _, ok := dictEN[key]; !ok {
				t.Errorf("%s uses unknown word key %q", filepath.Base(f), key)
			}
		}
	}
}

func TestSameGuestCannotDoubleBook(t *testing.T) {
	e := newEnv(t, nil)
	// An unpaid attempt is replaced when the same guest tries again.
	first := refFromLocation(t, e.do("POST", "/book", bookingForm("11:30", "2"), false).Header().Get("Location"))
	second := refFromLocation(t, e.do("POST", "/book", bookingForm("11:30", "2"), false).Header().Get("Location"))
	if b, _ := e.store.Get(first); b.Status != db.StatusExpired {
		t.Fatalf("first unpaid attempt should be released, is %s", b.Status)
	}
	e.do("POST", "/dev/pay/"+second, url.Values{}, false)

	// Once paid, the same email or phone can't book an overlapping time.
	e.expect(e.do("POST", "/book", bookingForm("12:00", "2"), false), 409, "already have a booking at 11:30")
	f := bookingForm("12:30", "2")
	f.Set("email", "other@example.com") // same phone, different email
	e.expect(e.do("POST", "/book", f, false), 409, "already have a booking")
	// A different guest at the same time is fine (6 tables).
	if rec := e.do("POST", "/book", guestForm(1, "11:30", "2"), false); rec.Code != 303 {
		t.Fatalf("other guest blocked: %d", rec.Code)
	}
	// The same guest later the same day, not overlapping, is fine.
	if rec := e.do("POST", "/book", bookingForm("17:00", "2"), false); rec.Code != 303 {
		t.Fatalf("non-overlapping booking blocked: %d", rec.Code)
	}
}

func TestTableNumbersCheckInAndNoShow(t *testing.T) {
	e := newEnv(t, nil)
	var refs []string
	for i := 0; i < 3; i++ {
		ref := refFromLocation(t, e.do("POST", "/book", guestForm(i, "12:00", "2"), false).Header().Get("Location"))
		e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
		refs = append(refs, ref)
	}
	for i, ref := range refs {
		if b, _ := e.store.Get(ref); b.TableNo != i+1 {
			t.Fatalf("booking %d got table %d, want %d", i, b.TableNo, i+1)
		}
	}
	e.expect(e.do("GET", "/admin/day?date=2026-10-26", nil, true), 200, "Table 1", "Table 3", "No-show at 12:20", "Move to another table")

	// Move booking 1 to table 5; table 2 is taken, so moving there fails.
	e.do("POST", "/admin/bookings/"+refs[0]+"/table", url.Values{"table": {"5"}}, true)
	if b, _ := e.store.Get(refs[0]); b.TableNo != 5 {
		t.Fatalf("move failed: table %d", b.TableNo)
	}
	e.expect(e.do("POST", "/admin/bookings/"+refs[0]+"/table", url.Values{"table": {"2"}}, true), 303)
	if b, _ := e.store.Get(refs[0]); b.TableNo != 5 {
		t.Fatal("moved onto a taken table")
	}

	// At 12:10 guest 2 arrives. At 12:20 the others become no-shows.
	e.srv.Now = func() time.Time { return time.Date(2026, 10, 26, 12, 10, 0, 0, schedule.Location) }
	e.do("POST", "/admin/bookings/"+refs[1]+"/arrived", url.Values{"arrived": {"yes"}}, true)
	at := time.Date(2026, 10, 26, 12, 20, 0, 0, schedule.Location)
	e.srv.Now = func() time.Time { return at }
	b0, _ := e.store.Get(refs[0])
	b1, _ := e.store.Get(refs[1])
	if b0.EffectiveStatus(at) != db.StatusNoShow || b1.EffectiveStatus(at) != db.StatusArrived {
		t.Fatalf("statuses: %s / %s", b0.EffectiveStatus(at), b1.EffectiveStatus(at))
	}
	// No-shows free their tables: 6 tables minus the one guest who arrived.
	if free := e.store.FreeTables("2026-10-26", []string{"13:00"}, 0, true, at)["13:00"]; free != 5 {
		t.Fatalf("free tables after no-shows = %d, want 5", free)
	}
	e.expect(e.do("GET", "/admin/day?date=2026-10-26", nil, true), 200, "No-show · deposit kept", "Arrived late", "be cancelled after the guests arrived")
	if strings.Contains(e.do("GET", "/admin/day?date=2026-10-26", nil, true).Body.String(), "Undo") {
		t.Fatal("there should be no undo for a check-in")
	}
	// Arrived is final: the store won't clear it and a cancel is refused.
	e.do("POST", "/admin/bookings/"+refs[1]+"/arrived", url.Values{"arrived": {"no"}}, true)
	e.do("POST", "/admin/bookings/"+refs[1]+"/cancel", url.Values{"refund": {"yes"}}, true)
	if b, _ := e.store.Get(refs[1]); b.ArrivedAt == nil || b.Status != db.StatusPaid {
		t.Fatalf("arrived booking changed: arrived=%v status=%s", b.ArrivedAt, b.Status)
	}
}

func TestOwnerReports(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Cfg.OwnerUser, e.srv.Cfg.OwnerPassword = "owner", "boss"
	e.h = e.srv.Routes()
	menu := e.store.Menu(true)

	// The manager opens the money pages (there's no separate head manager).
	e.expect(e.do("GET", "/owner", nil, true), 200, "Money today")

	pay := func(f url.Values, path string) string {
		ref := refFromLocation(t, e.do("POST", path, f, false).Header().Get("Location"))
		e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
		return ref
	}
	// Monday 26 Oct, 09:00:
	arrived := pay(guestForm(1, "12:00", "2"), "/book")  // 10 €, comes
	noShow := pay(guestForm(2, "12:00", "4"), "/book")   // 20 €, never comes
	refunded := pay(guestForm(3, "18:00", "3"), "/book") // 20 €, refunded
	e.do("POST", "/admin/bookings/"+refunded+"/cancel", url.Values{"refund": {"yes"}}, true)
	e.do("POST", "/book", guestForm(4, "18:00", "2"), false) // never paid
	ta := url.Values{"date": {"2026-10-26"}, "time": {"12:30"}, "name": {"Erik"}, "email": {"erik@example.com"}, "phone": {"0401234567"},
		fmt.Sprintf("qty_%d", menu[0].ID): {"2"}, fmt.Sprintf("qty_%d", menu[2].ID): {"1"}} // 44,50 €
	pay(ta, "/takeaway")
	ta2 := url.Values{"date": {"2026-10-26"}, "time": {"13:00"}, "name": {"Lisa"}, "email": {"lisa@example.com"}, "phone": {"0407654321"},
		fmt.Sprintf("qty_%d", menu[2].ID): {"1"}} // 15,50 €, deposit 3,88 €, cancelled
	cancelled := pay(ta2, "/takeaway")
	e.do("POST", "/admin/bookings/"+cancelled+"/cancel", url.Values{"refund": {"no"}}, true)

	// 12:10: guest 1 arrives. Look at the report at 13:00 (guest 2 is a no-show).
	e.srv.Now = func() time.Time { return time.Date(2026, 10, 26, 12, 10, 0, 0, schedule.Location) }
	e.do("POST", "/admin/bookings/"+arrived+"/arrived", url.Values{"arrived": {"yes"}}, true)
	now := time.Date(2026, 10, 26, 13, 0, 0, 0, schedule.Location)
	e.srv.Now = func() time.Time { return now }
	_ = noShow

	m := computeStats(e.store.BookingsBetween("2026-10-01", "2026-10-31"), now)
	// Revenue = takeaway food 44,50 + kept takeaway deposit 3,88 + table deposits (10 + 20) = 78,38 €
	want := stats{
		Revenue: 7838, TakeawaySales: 4450, TakeawayKept: 388, ToCollect: 3337, DepositsNet: 3000, DepositsUsed: 1000, DepositsKept: 2000,
		DepositsGross: 5000, Refunded: 2000, TableBookings: 3, DepositsPaid: 3, Guests: 2, GuestsArrived: 2, TakeawayOrders: 1,
		Arrived: 1, NoShows: 1, Refunds: 1, TakeawayCancelled: 1, NeverPaid: 1,
	}
	if m != want {
		t.Fatalf("stats\n got %+v\nwant %+v", m, want)
	}

	owner := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://example.test"+path, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleOwner, e.srv.Now())})
		req.Header.Set("Accept-Language", "en")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	// Today page: money made and what happened. (The booked / arrived /
	// no-show boxes are gone from it.)
	if strings.Contains(owner("/owner?date=2026-10-26").Body.String(), "Not here yet") {
		t.Fatal("the not-arrived boxes are back")
	}
	e.expect(owner("/owner?date=2026-10-26"), 200, "78,38 €", "Refunded", "Left without paying", "Takeaway", "Erik")
	// Charts: day by day and month by month, every metric.
	for _, m := range []string{"money", "tables", "guests", "orders", "lost"} {
		e.expect(owner("/owner/charts?view=days&month=2026-10&metric="+m), 200, "Day by day", `id="c-main"`, "Show as a table")
		e.expect(owner("/owner/charts?view=months&month=2026-10&metric="+m), 200, "Month by month", "last 12 months")
	}
	e.expect(owner("/owner/charts?month=2026-10"), 200, "78,38 €", "Fisksoppa", "No-shows", "Never paid", `"unit":"eur"`)
	e.expect(owner("/owner/export.csv?month=2026-10"), 200, "Booking;Type;Booked via;Date", "No-show · deposit kept", "44,50", ";website;")
}

// The example from the head manager: 10 orders of 1 € a day for a 30-day
// month is 10 € a day and 300 € for the month.
func TestTenEurosADay(t *testing.T) {
	var bs []db.Booking
	paid := time.Date(2026, 11, 1, 0, 0, 0, 0, schedule.Location)
	for day := 1; day <= 30; day++ {
		for i := 0; i < 10; i++ {
			bs = append(bs, db.Booking{Kind: db.KindTakeaway, Status: db.StatusPaid, PaidAt: &paid,
				Date: fmt.Sprintf("2026-11-%02d", day), Time: "12:00", TotalCents: 100, DepositCents: 25})
		}
	}
	m := computeStats(bs, time.Date(2026, 12, 5, 0, 0, 0, 0, schedule.Location))
	if m.Revenue != 30000 || m.TakeawayOrders != 300 || m.Revenue/30 != 1000 {
		t.Fatalf("got revenue %d, orders %d", m.Revenue, m.TakeawayOrders)
	}
}

func TestSmoothPartsAndCalendar(t *testing.T) {
	e := newEnv(t, nil)
	// The booking page swaps in a day's times without reloading.
	rec := e.do("GET", "/book/times?date=2026-10-27&size=3", nil, false)
	e.expect(rec, 200, `data-when="Tuesday 27 October"`, "19:00", "Lunch")
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatal("times fragment should not contain the whole page")
	}
	closeSundays(t)
	e.expect(e.do("GET", "/book/times?date=2026-11-01&size=3", nil, false), 200, "closed on Sunday")

	// Scripts and styles are served with a version so browsers never use old copies.
	page := e.do("GET", "/", nil, false).Body.String()
	if !strings.Contains(page, "/static/app.js?v="+e.srv.assetVer) || e.srv.assetVer == "" {
		t.Fatal("app.js is not versioned")
	}
	e.expect(e.do("GET", "/static/app.js", nil, false), 200, "booking")
	e.expect(e.do("GET", "/static/charts.js", nil, false), 200, "ResizeObserver")

	// A paid guest can add the booking to their calendar; an unpaid one can't.
	ref := refFromLocation(t, e.do("POST", "/book", bookingForm("18:00", "2"), false).Header().Get("Location"))
	e.expect(e.do("GET", "/b/"+ref+"/calendar.ics", nil, false), 404)
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	e.expect(e.do("GET", "/b/"+ref+"/calendar.ics", nil, false), 200, "BEGIN:VEVENT", "DTSTART:20261026T160000Z", "Table at E-Meal Kitchen")
	e.expect(e.do("GET", "/b/"+ref, nil, false), 200, "Add to calendar")

	// Menu: the on/off switch hides a dish from the takeaway page.
	first := e.store.Menu(true)[0]
	e.do("POST", fmt.Sprintf("/admin/menu/%d/available", first.ID), url.Values{}, true)
	if strings.Contains(e.do("GET", "/takeaway", nil, false).Body.String(), first.Name+"<") {
		t.Fatalf("%s should be hidden", first.Name)
	}
	e.do("POST", fmt.Sprintf("/admin/menu/%d/available", first.ID), url.Values{"available": {"on"}}, true)
	e.expect(e.do("GET", "/takeaway", nil, false), 200, first.Name)
}

func TestStaffLogin(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Cfg.OwnerUser, e.srv.Cfg.OwnerPassword = "owner", "boss"
	e.h = e.srv.Routes()
	post := func(user, pass, next string) *httptest.ResponseRecorder {
		f := url.Values{"username": {user}, "password": {pass}, "next": {next}}
		req := httptest.NewRequest("POST", "http://example.test/login", strings.NewReader(f.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept-Language", "en")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	get := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://example.test"+path, nil)
		req.Header.Set("Accept-Language", "en")
		if c != nil {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	cookie := func(rec *httptest.ResponseRecorder) *http.Cookie {
		for _, c := range rec.Result().Cookies() {
			if c.Name == sessionCookie {
				return c
			}
		}
		t.Fatal("no session cookie")
		return nil
	}
	e.expect(get("/login", nil), 200, "Staff login")
	e.expect(post("admin", "wrong", ""), 401, "username or password")

	// The old head-manager login is just a second manager login: Orders,
	// with the money pages too.
	rec := post("owner", "boss", "")
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/orders" {
		t.Fatalf("owner login: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	oc := cookie(rec)
	e.expect(get("/owner", oc), 200, "Money today", "Bookings", "Log out")
	e.expect(get("/admin/day", oc), 200, "Money today", "Table bookings", ">Manager<")
	e.expect(get("/admin/menu", oc), 200, "Drink with alcohol (25,5 % VAT): tables only")

	// Manager: lands on Orders, or the money page they wanted.
	rec = post("admin", "pw", "/owner/charts")
	if rec.Header().Get("Location") != "/owner/charts" {
		t.Fatalf("manager to the money pages: %s", rec.Header().Get("Location"))
	}
	mc := cookie(rec)
	e.expect(get("/admin/day", mc), 200, "Money today")
	e.expect(get("/owner/charts", mc), 200, "Charts")

	// Going back to the page you wanted after logging in, but never to another site.
	if loc := post("admin", "pw", "/admin/menu").Header().Get("Location"); loc != "/admin/menu" {
		t.Fatalf("next: %s", loc)
	}
	if loc := post("admin", "pw", "//evil.example").Header().Get("Location"); loc != "/admin/orders" {
		t.Fatalf("open redirect: %s", loc)
	}
	// A forged cookie doesn't work.
	e.expect(get("/admin/day", &http.Cookie{Name: sessionCookie, Value: "b3duZXJ8OTk5OTk5OTk5OQ.abc"}), 303)
	// Log out clears the cookie.
	req := httptest.NewRequest("POST", "http://example.test/logout", nil)
	req.AddCookie(oc)
	out := httptest.NewRecorder()
	e.h.ServeHTTP(out, req)
	if c := cookie(out); c.MaxAge >= 0 {
		t.Fatal("logout did not clear the cookie")
	}
}

// closeSundays makes Sunday a closed day for the rest of one test, so the
// "we're closed that day" messages can still be checked.
func closeSundays(t *testing.T) {
	h, ok := schedule.Hours[time.Sunday]
	delete(schedule.Hours, time.Sunday)
	t.Cleanup(func() {
		if ok {
			schedule.Hours[time.Sunday] = h
		}
	})
}
