package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"emeal-booking/db"
	"emeal-booking/payments"
)

// phone is one guest's browser at the table: it keeps its own cookie.
type phone struct {
	e      *env
	cookie *http.Cookie
	ip     string
	key    string
}

func (p *phone) do(method, path string, form url.Values) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, "http://example.test"+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept-Language", "en")
	req.RemoteAddr = p.ip + ":5555"
	if p.cookie != nil {
		req.AddCookie(p.cookie)
	}
	rec := httptest.NewRecorder()
	p.e.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == tabCookie {
			p.cookie = c
		}
	}
	return rec
}

func tableCode(t *testing.T, e *env, table int) string {
	t.Helper()
	for _, q := range e.store.TableQRs() {
		if q.Table == table {
			return q.Code
		}
	}
	t.Fatalf("no code for table %d", table)
	return ""
}

func menuID(t *testing.T, e *env, name string) string {
	t.Helper()
	for _, m := range e.store.Menu(true) {
		if m.Name == name {
			return fmt.Sprint(m.ID)
		}
	}
	t.Fatalf("no %s on the menu", name)
	return ""
}

// join: a phone scans table 5's sticker and taps Start ordering.
func join(t *testing.T, e *env, code string, n int) *phone {
	t.Helper()
	p := &phone{e: e, ip: fmt.Sprintf("10.9.0.%d", n)}
	rec := p.do("POST", "/t/"+code, url.Values{})
	loc := rec.Header().Get("Location")
	if rec.Code != 303 || !strings.HasPrefix(loc, "/g/") {
		t.Fatalf("join: %d %s", rec.Code, loc)
	}
	p.key = strings.TrimPrefix(loc, "/g/")
	return p
}

func TestQRGuestsOrderAndOnePaysForAll(t *testing.T) {
	e := newEnv(t, nil) // demo payments
	code := tableCode(t, e, 5)

	// The stickers page has every table's link.
	e.expect(e.do("GET", "/admin/tables/qr", nil, true), 200, "/t/"+code, "Table 5")

	// Before a waiter opens the table, phones can't join.
	guest := &phone{e: e, ip: "10.9.0.1"}
	e.expect(guest.do("GET", "/t/"+code, nil), 200, "isn&#39;t taking orders from phones yet")
	if rec := guest.do("POST", "/t/"+code, url.Values{}); loc(rec) != "/t/"+code+"?msg=closed" {
		t.Fatalf("join a closed table: %s", loc(rec))
	}
	e.expect(guest.do("GET", "/t/0123456789abcdef0123456789abcdef", nil), 404)

	// The waiter opens table 5 for QR orders.
	if rec := e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true); loc(rec) != "/admin/orders/5?msg=qropen" {
		t.Fatalf("open: %d %s", rec.Code, loc(rec))
	}
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "QR orders are on")

	// Three phones join; scanning again brings you back to yourself.
	a, b, c := join(t, e, code, 1), join(t, e, code, 2), join(t, e, code, 3)
	if rec := a.do("GET", "/t/"+code, nil); loc(rec) != "/g/"+a.key {
		t.Fatalf("rescan: %d %s", rec.Code, loc(rec))
	}
	if rec := a.do("POST", "/t/"+code, url.Values{}); loc(rec) != "/g/"+a.key {
		t.Fatalf("double tap on Start: %s", loc(rec))
	}
	e.expect(a.do("GET", "/g/"+a.key, nil), 200, "Guest 1", "Your order", "Send order", "Laxfilé")

	// Each orders their own; the price always comes from the menu.
	lax, soda, burger, risotto := menuID(t, e, "Laxfilé"), menuID(t, e, "Läsk 33 cl"), menuID(t, e, "E-Meal-burgare"), menuID(t, e, "Svamprisotto")
	a.do("POST", "/g/"+a.key+"/name", url.Values{"name": {"Anna"}})
	if rec := a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + lax: {"1"}, "qty_" + soda: {"2"}, "price_" + lax: {"0,01"}, "note_" + lax: {"no dill"}}); loc(rec) != "/g/"+a.key+"?msg=ordered" {
		t.Fatalf("order: %d %s", rec.Code, loc(rec))
	}
	b.do("POST", "/g/"+b.key+"/order", url.Values{"qty_" + burger: {"1"}})
	c.do("POST", "/g/"+c.key+"/order", url.Values{"qty_" + risotto: {"1"}})
	if rec := c.do("POST", "/g/"+c.key+"/order", url.Values{"qty_" + soda: {"21"}}); loc(rec) != "/g/"+c.key+"?msg=toomany" {
		t.Fatalf("21 sodas: %s", loc(rec))
	}

	bill, _ := e.store.OpenBill(5)
	pay, _, _ := bill.Parts()
	if pay[0] != 2690+700 || pay[1] != 1990 || pay[2] != 2190 {
		t.Fatalf("parts %v", pay)
	}
	// The chef sees the food with whose it is; the waiters get the sodas.
	e.expect(e.do("GET", "/admin/kitchen", nil, true), 200, "Laxfilé", "Guest 1 · Anna", "no dill", "E-Meal-burgare")
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Drinks to bring", "2× Läsk 33 cl")
	e.expect(e.do("GET", "/admin/kitchen", nil, true), 200)
	if strings.Contains(e.do("GET", "/admin/kitchen", nil, true).Body.String(), "Läsk") {
		t.Fatal("drinks must never reach the chef")
	}
	e.expect(a.do("GET", "/g/"+a.key, nil), 200, "Guest 1 · Anna", "33,90 €", "Pay my part · 33,90 €", "Pay for the whole table · 75,70 €")

	// Guest 2 pays for everyone: the table is held for them.
	rec := b.do("POST", "/g/"+b.key+"/pay", url.Values{"all": {"1"}})
	demo := loc(rec)
	if !strings.HasPrefix(demo, "/dev/tab/tab-") {
		t.Fatalf("pay all: %d %s", rec.Code, demo)
	}
	e.expect(e.do("GET", demo, nil, false), 200, "75,70 €", "the whole table")
	live := a.do("GET", "/g/"+a.key+"?part=live", nil)
	e.expect(live, 200, "Guest 2 is paying for the whole table…")
	if strings.Contains(live.Body.String(), "Pay my part") || strings.Contains(live.Body.String(), `data-menu-state="open"`) {
		t.Fatal("guest 1 could still pay or order while guest 2 pays for everyone")
	}
	e.expect(b.do("GET", "/g/"+b.key+"?part=live", nil), 200, "You&#39;re paying for the whole table", "Continue paying", "Cancel payment")
	if rec := a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"0"}}); loc(rec) != "/g/"+a.key+"?msg=busy" {
		t.Fatalf("guest 1 paying meanwhile: %s", loc(rec))
	}
	if rec := a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + soda: {"1"}}); loc(rec) != "/g/"+a.key+"?msg=busy" {
		t.Fatalf("ordering meanwhile: %s", loc(rec))
	}
	// The staff can't take a card payment for a held part either.
	if rec := e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {fmt.Sprint(bill.ID)}, "guest": {"3"}, "method": {"card"}}, true); !strings.Contains(loc(rec), "msg=phonepaying") {
		t.Fatalf("staff pay during hold: %s", loc(rec))
	}
	e.expect(e.do("GET", "/admin/orders/5/split", nil, true), 200, "Guest 2 is paying for the whole table on their phone.", "Paying on their phone…")
	// Guest 1 can't cancel guest 2's payment.
	ref := strings.TrimPrefix(demo, "/dev/tab/")
	a.do("POST", "/g/"+a.key+"/cancel", url.Values{"a": {ref}})
	if at, _ := e.store.AttemptByRef(ref); !at.Pending() {
		t.Fatal("another guest cancelled the payment")
	}

	// It goes through: everyone is paid, the table is free.
	if rec := e.do("POST", demo, url.Values{"action": {"pay"}}, false); loc(rec) != "/g/"+b.key+"?msg=paid" {
		t.Fatalf("demo pay: %d %s", rec.Code, loc(rec))
	}
	if _, open := e.store.OpenBill(5); open {
		t.Fatal("table 5 should be free")
	}
	e.expect(a.do("GET", "/g/"+a.key, nil), 200, "The table is paid. Thank you!", "Guest 2 paid for you.")
	if rec := a.do("GET", "/t/"+code, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "isn&#39;t taking orders") {
		t.Fatalf("after paying, the table is closed for QR again: %d", rec.Code)
	}
	paid, _ := e.store.BillByID(bill.ID)
	e.expect(e.do("GET", "/r/"+paid.Split[0].Ref, nil, false), 200, "Paid online", "Guest 2 paid this for you.", "Anna")
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Online at the table", "75,70 €")
}

func TestQRPayForAllTimesOut(t *testing.T) {
	e := newEnv(t, nil)
	code := tableCode(t, e, 2)
	e.do("POST", "/admin/orders/2/qr", url.Values{"open": {"1"}}, true)
	a, b := join(t, e, code, 1), join(t, e, code, 2)
	burger := menuID(t, e, "E-Meal-burgare")
	a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + burger: {"1"}})
	b.do("POST", "/g/"+b.key+"/order", url.Values{"qty_" + burger: {"1"}})
	demo := loc(a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"1"}}))
	if rec := b.do("POST", "/g/"+b.key+"/pay", url.Values{"all": {"0"}}); loc(rec) != "/g/"+b.key+"?msg=busy" {
		t.Fatalf("held: %s", loc(rec))
	}
	// Guest 1 walks away. After 2 minutes guest 2 can pay.
	e.srv.Now = func() time.Time { return fixedNow.Add(db.PayHoldFor + time.Second) }
	e.srv.SweepPayments(context.Background())
	e.expect(b.do("GET", "/g/"+b.key+"?part=live", nil), 200, "Pay my part · 19,90 €")
	if rec := b.do("POST", "/g/"+b.key+"/pay", url.Values{"all": {"0"}}); !strings.HasPrefix(loc(rec), "/dev/tab/") {
		t.Fatalf("after the hold ran out: %s", loc(rec))
	}
	// Guest 1's old page can't be paid any more.
	e.expect(e.do("GET", demo, nil, false), 200, "ran out of time")
	if rec := e.do("POST", demo, url.Values{"action": {"pay"}}, false); loc(rec) != "/g/"+a.key+"?msg=timeout" {
		t.Fatalf("paying a run-out page: %s", loc(rec))
	}
}

// tabStripe pretends to be Stripe for payments at the table.
type tabStripe struct {
	mu       sync.Mutex
	n        int
	refs     map[string]string // session -> client_reference_id
	state    map[string]string // session -> open, complete, expired
	amounts  map[string]string
	tips     map[string]string // session -> the tip line: "234 Tip for the staff (10 %)"
	emails   []string
	expires  []string
	refunds  []string
	failNext bool
}

func (f *tabStripe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.ParseForm()
	path := r.URL.Path
	switch {
	case r.Method == "POST" && path == "/v1/checkout/sessions":
		if f.failNext {
			f.failNext = false
			http.Error(w, `{"error":{"message":"down"}}`, 500)
			return
		}
		f.n++
		id := fmt.Sprintf("cs_tab_%d", f.n)
		f.refs[id], f.state[id], f.amounts[id] = r.FormValue("client_reference_id"), "open", r.FormValue("line_items[0][price_data][unit_amount]")
		f.emails = append(f.emails, r.FormValue("customer_email"))
		if tip := r.FormValue("line_items[1][price_data][unit_amount]"); tip != "" {
			if f.tips == nil {
				f.tips = map[string]string{}
			}
			f.tips[id] = tip + " " + r.FormValue("line_items[1][price_data][product_data][name]")
		}
		json.NewEncoder(w).Encode(map[string]string{"id": id, "url": "https://checkout.stripe.test/" + id, "status": "open"})
	case r.Method == "POST" && strings.HasSuffix(path, "/expire"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/checkout/sessions/"), "/expire")
		f.expires = append(f.expires, id)
		if f.state[id] != "open" {
			http.Error(w, `{"error":{"message":"Only Checkout Sessions with a status of open can be expired."}}`, 400)
			return
		}
		f.state[id] = "expired"
		json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "expired"})
	case r.Method == "GET" && strings.HasPrefix(path, "/v1/checkout/sessions/"):
		id := strings.TrimPrefix(path, "/v1/checkout/sessions/")
		ps := "unpaid"
		if f.state[id] == "complete" {
			ps = "paid"
		}
		json.NewEncoder(w).Encode(map[string]string{"id": id, "status": f.state[id], "payment_status": ps, "client_reference_id": f.refs[id], "payment_intent": "pi_" + id})
	case r.Method == "POST" && path == "/v1/refunds":
		f.refunds = append(f.refunds, r.FormValue("payment_intent"))
		w.Write([]byte(`{"id":"re_1"}`))
	default:
		http.Error(w, `{"error":{"message":"unknown"}}`, 404)
	}
}

func (f *tabStripe) pay(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state[id] = "complete"
}

func webhook(t *testing.T, e *env, typ, session, ref string) {
	t.Helper()
	payload := []byte(fmt.Sprintf(`{"id":"evt_%s","type":%q,"data":{"object":{"id":%q,"status":"complete","payment_status":"paid","client_reference_id":%q,"payment_intent":"pi_%s"}}}`, session, typ, session, ref, session))
	req := httptest.NewRequest("POST", "/stripe/webhook", strings.NewReader(string(payload)))
	req.Header.Set("Stripe-Signature", payments.SignForTest(payload, "whsec_x", e.srv.Now()))
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("webhook: %d %s", rr.Code, rr.Body.String())
	}
}

func TestQRStripePayments(t *testing.T) {
	fs := &tabStripe{refs: map[string]string{}, state: map[string]string{}, amounts: map[string]string{}}
	api := httptest.NewServer(fs)
	defer api.Close()
	e := newEnv(t, &payments.Client{SecretKey: "sk_test_x", WebhookSecret: "whsec_x", APIBase: api.URL})
	code := tableCode(t, e, 6)
	e.do("POST", "/admin/orders/6/qr", url.Values{"open": {"1"}}, true)
	a, b, c := join(t, e, code, 1), join(t, e, code, 2), join(t, e, code, 3)
	lax, burger := menuID(t, e, "Laxfilé"), menuID(t, e, "E-Meal-burgare")
	a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + lax: {"1"}})
	b.do("POST", "/g/"+b.key+"/order", url.Values{"qty_" + burger: {"1"}})
	c.do("POST", "/g/"+c.key+"/order", url.Values{"qty_" + burger: {"1"}})

	// 1. Guest 1 starts paying their part, then presses Stripe's back link:
	// the Stripe page is cancelled and the hold lifts at once.
	rec := a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"0"}})
	if loc(rec) != "https://checkout.stripe.test/cs_tab_1" || fs.amounts["cs_tab_1"] != "2690" || fs.emails[0] != "" {
		t.Fatalf("pay my part: %s, amount %s, email %q", loc(rec), fs.amounts["cs_tab_1"], fs.emails[0])
	}
	if rec := c.do("POST", "/g/"+c.key+"/pay", url.Values{"all": {"1"}}); loc(rec) != "/g/"+c.key+"?msg=busy" {
		t.Fatalf("pay-for-all while guest 1 pays: %s", loc(rec))
	}
	ref1 := fs.refs["cs_tab_1"]
	if rec := a.do("GET", "/g/"+a.key+"/back?a="+ref1, nil); loc(rec) != "/g/"+a.key+"?msg=cancelled" {
		t.Fatalf("back: %s", loc(rec))
	}
	if fs.state["cs_tab_1"] != "expired" {
		t.Fatal("the Stripe page wasn't cancelled")
	}

	// 2. Guest 3 pays for all and walks away while Stripe is slow to answer:
	// when the hold runs out the sweeper cancels the page and lets go.
	e.srv.Now = func() time.Time { return fixedNow.Add(time.Minute) }
	c.do("POST", "/g/"+c.key+"/pay", url.Values{"all": {"1"}})
	if fs.amounts["cs_tab_2"] != fmt.Sprint(2690+1990+1990) {
		t.Fatalf("whole table amount %s", fs.amounts["cs_tab_2"])
	}
	e.srv.SweepPayments(context.Background()) // too early: nothing happens
	if fs.state["cs_tab_2"] != "open" {
		t.Fatal("cancelled too early")
	}
	e.srv.Now = func() time.Time { return fixedNow.Add(4 * time.Minute) }
	e.srv.SweepPayments(context.Background())
	if fs.state["cs_tab_2"] != "expired" {
		t.Fatal("the sweeper didn't cancel the page")
	}

	// 3. Guest 2 pays for all; they finish on Stripe just as their time runs
	// out. The sweeper can't cancel a paid page: it records the payment.
	b.do("POST", "/g/"+b.key+"/pay", url.Values{"all": {"1"}})
	fs.pay("cs_tab_3")
	e.srv.Now = func() time.Time { return fixedNow.Add(7 * time.Minute) }
	e.srv.SweepPayments(context.Background())
	if _, open := e.store.OpenBill(6); open {
		t.Fatal("a payment made at Stripe was lost")
	}
	// The webhook arrives afterwards: nothing changes, nothing refunded.
	webhook(t, e, "checkout.session.completed", "cs_tab_3", fs.refs["cs_tab_3"])
	if len(fs.refunds) != 0 {
		t.Fatalf("refunds %v", fs.refunds)
	}
	bill := e.store.BillsForTable(6)[0]
	for _, sh := range bill.Split {
		if sh.PaidBy != db.PaidOnline || sh.Payer != 2 {
			t.Fatalf("guest %d: %+v", sh.Guest, sh)
		}
	}
	e.expect(a.do("GET", "/g/"+a.key, nil), 200, "The table is paid")
}

func TestQRStripeWebhookAndReturn(t *testing.T) {
	fs := &tabStripe{refs: map[string]string{}, state: map[string]string{}, amounts: map[string]string{}}
	api := httptest.NewServer(fs)
	defer api.Close()
	e := newEnv(t, &payments.Client{SecretKey: "sk_test_x", WebhookSecret: "whsec_x", APIBase: api.URL})
	code := tableCode(t, e, 3)
	e.do("POST", "/admin/orders/3/qr", url.Values{"open": {"1"}}, true)
	a, b := join(t, e, code, 1), join(t, e, code, 2)
	burger := menuID(t, e, "E-Meal-burgare")
	a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + burger: {"1"}})
	b.do("POST", "/g/"+b.key+"/order", url.Values{"qty_" + burger: {"2"}})

	// Stripe is down: the hold is let go and the guest is told.
	fs.failNext = true
	if rec := a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"0"}}); loc(rec) != "/g/"+a.key+"?msg=payfail" {
		t.Fatalf("stripe down: %s", loc(rec))
	}
	// Guest 1 pays their part; back from Stripe it shows Paid at once.
	a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"0"}})
	fs.pay("cs_tab_1")
	ref := fs.refs["cs_tab_1"]
	if rec := a.do("GET", "/g/"+a.key+"/paid?a="+ref+"&session_id=cs_tab_1", nil); loc(rec) != "/g/"+a.key+"?msg=paid" {
		t.Fatalf("return: %s", loc(rec))
	}
	// Guest 2 pays theirs; the webhook confirms it and the table is free.
	b.do("POST", "/g/"+b.key+"/pay", url.Values{"all": {"0"}})
	fs.pay("cs_tab_2")
	webhook(t, e, "checkout.session.completed", "cs_tab_2", fs.refs["cs_tab_2"])
	if _, open := e.store.OpenBill(3); open {
		t.Fatal("table should be free")
	}
	// An expired-page webhook for an old attempt changes nothing.
	webhook(t, e, "checkout.session.expired", "cs_tab_1", ref)
	e.expect(b.do("GET", "/g/"+b.key, nil), 200, "The table is paid")
}

func TestQRGuestSafety(t *testing.T) {
	e := newEnv(t, nil)
	code := tableCode(t, e, 4)
	e.do("POST", "/admin/orders/4/qr", url.Values{"open": {"1"}}, true)
	a := join(t, e, code, 1)

	// A form from another site is refused.
	req := httptest.NewRequest("POST", "http://example.test/g/"+a.key+"/order", strings.NewReader("qty_1=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("cross-site order: %d", rec.Code)
	}
	// Made-up keys find nothing.
	e.expect(a.do("GET", "/g/0123456789abcdef0123456789abcdef", nil), 404)
	e.expect(a.do("GET", "/g/short", nil), 404)
	// Guest pages are never stored, never indexed, and keys stay out of logs.
	page := a.do("GET", "/g/"+a.key, nil)
	if page.Header().Get("Cache-Control") != "no-store" || page.Header().Get("X-Robots-Tag") != "noindex" {
		t.Fatalf("headers %v", page.Header())
	}
	if got := logPath("/g/" + a.key + "/pay"); got != "/g/…/pay" {
		t.Fatalf("log path %q", got)
	}
	if got := logPath("/t/" + code); got != "/t/…" {
		t.Fatalf("log path %q", got)
	}
	// Staff can't take a phone guest off the bill or un-split it.
	bill, _ := e.store.OpenBill(4)
	if rec := e.do("POST", "/admin/orders/4/split/guests", url.Values{"bill": {fmt.Sprint(bill.ID)}, "guests": {"1"}}, true); !strings.Contains(loc(rec), "msg=qrguests") {
		t.Fatalf("un-split: %s", loc(rec))
	}
	// Closing QR orders on a table nobody ordered at leaves no empty bill.
	e.do("POST", "/admin/orders/6/qr", url.Values{"open": {"1"}}, true)
	e.do("POST", "/admin/orders/6/qr", url.Values{"open": {"0"}}, true)
	if _, open := e.store.OpenBill(6); open {
		t.Fatal("empty bill left behind")
	}
	// A new sticker code: the old one stops working.
	e.do("POST", "/admin/tables/qr/4/new", url.Values{}, true)
	e.expect(a.do("GET", "/t/"+code, nil), 404)
}

func TestQRPagesInSwedish(t *testing.T) {
	e := newEnv(t, nil)
	code := tableCode(t, e, 5)
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	a := join(t, e, code, 1)
	a.cookie = nil
	req := httptest.NewRequest("GET", "http://example.test/g/"+a.key, nil)
	req.AddCookie(&http.Cookie{Name: langCookie, Value: "sv"})
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	e.expect(rec, 200, "Gäst 1", "Din beställning", "Skicka beställningen")
}

func loc(rec *httptest.ResponseRecorder) string { return rec.Header().Get("Location") }

func TestQRReceiptChoices(t *testing.T) {
	e := newEnv(t, nil)
	type sent struct{ to, subject, body string }
	var mails []sent
	e.srv.Mail.Capture = func(to, subject, body string) { mails = append(mails, sent{to, subject, body}) }
	code := tableCode(t, e, 5)
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	a, b := join(t, e, code, 1), join(t, e, code, 2)
	lax, burger := menuID(t, e, "Laxfilé"), menuID(t, e, "E-Meal-burgare")
	a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + lax: {"1"}})
	b.do("POST", "/g/"+b.key+"/order", url.Values{"qty_" + burger: {"1"}})

	// Before paying there's no receipt to send.
	if rec := b.do("POST", "/g/"+b.key+"/receipt/email", url.Values{"email": {"b@example.fi"}}); loc(rec) != "/g/"+b.key+"?msg=notpaid" {
		t.Fatalf("email before paying: %s", loc(rec))
	}
	// Guest 1 pays for both.
	demo := loc(a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"1"}}))
	e.do("POST", demo, url.Values{"action": {"pay"}}, false)

	// Guest 2 was paid for: they can still choose how to get their receipt.
	e.expect(b.do("GET", "/g/"+b.key, nil), 200, "Your receipt", "Show it on my phone", "Email it to me", "Bring me a printed receipt")

	// Email: a wrong address is refused; a right one gets the receipt.
	if rec := b.do("POST", "/g/"+b.key+"/receipt/email", url.Values{"email": {"not an email"}}); loc(rec) != "/g/"+b.key+"?msg=bademail" {
		t.Fatalf("bad email: %s", loc(rec))
	}
	if rec := b.do("POST", "/g/"+b.key+"/receipt/email", url.Values{"email": {"bo@example.fi"}}); loc(rec) != "/g/"+b.key+"?msg=emailed" {
		t.Fatalf("email: %s", loc(rec))
	}
	if len(mails) != 1 || mails[0].to != "bo@example.fi" {
		t.Fatalf("mails %+v", mails)
	}
	for _, want := range []string{"Your receipt from E-Meal Kitchen", "Table 5 · Guest 2", "1 × E-Meal-burgare", "19,90 €", "Paid online", "Guest 1 paid this for you.", "/r/"} {
		if !strings.Contains(mails[0].subject+"\n"+mails[0].body, want) {
			t.Fatalf("email missing %q:\n%s\n%s", want, mails[0].subject, mails[0].body)
		}
	}
	// The payer's email says who they paid for.
	a.do("POST", "/g/"+a.key+"/receipt/email", url.Values{"email": {"anna@example.fi"}})
	if !strings.Contains(mails[1].body, "You also paid for guest 2: 46,80 € in total.") {
		t.Fatalf("payer's email:\n%s", mails[1].body)
	}
	// At most 3 emails per guest.
	b.do("POST", "/g/"+b.key+"/receipt/email", url.Values{"email": {"bo@example.fi"}})
	b.do("POST", "/g/"+b.key+"/receipt/email", url.Values{"email": {"bo@example.fi"}})
	if rec := b.do("POST", "/g/"+b.key+"/receipt/email", url.Values{"email": {"bo@example.fi"}}); loc(rec) != "/g/"+b.key+"?msg=mailmax" {
		t.Fatalf("4th email: %s", loc(rec))
	}

	// Paper: it shows up on the Orders screen until a waiter gives it.
	if rec := b.do("POST", "/g/"+b.key+"/receipt/paper", url.Values{}); loc(rec) != "/g/"+b.key+"?msg=paper" {
		t.Fatalf("paper: %s", loc(rec))
	}
	e.expect(b.do("GET", "/g/"+b.key+"?part=live", nil), 200, "A waiter is bringing your printed receipt.")
	bill := e.store.BillsForTable(5)[0]
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Receipts to print", "Guest 2 · 19,90 €",
		fmt.Sprintf("/admin/orders/5/receipts?bill=%d&amp;guest=2", bill.ID))
	only := e.do("GET", fmt.Sprintf("/admin/orders/5/receipts?bill=%d&guest=2", bill.ID), nil, true)
	e.expect(only, 200, "Guest 2 of 2", "E-Meal-burgare")
	if strings.Contains(only.Body.String(), "Laxfilé") {
		t.Fatal("printed guest 1's receipt too")
	}
	e.do("POST", fmt.Sprintf("/admin/receipts/%d/2/given", bill.ID), url.Values{}, true)
	if strings.Contains(e.do("GET", "/admin/orders", nil, true).Body.String(), "Receipts to print") {
		t.Fatal("still on the list after it was given")
	}
	e.expect(b.do("GET", "/g/"+b.key+"?part=live", nil), 200, "You got your printed receipt.")
}

func TestQRNotPaidYetAlert(t *testing.T) {
	e := newEnv(t, nil)
	code := tableCode(t, e, 5)
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	a, b, c := join(t, e, code, 1), join(t, e, code, 2), join(t, e, code, 3)
	burger := menuID(t, e, "E-Meal-burgare")
	for _, p := range []*phone{a, b, c} {
		p.do("POST", "/g/"+p.key+"/order", url.Values{"qty_" + burger: {"1"}})
	}
	// Nobody has paid: no alarm yet.
	if strings.Contains(e.do("GET", "/admin/orders", nil, true).Body.String(), "Not paid yet") {
		t.Fatal("alert before anyone paid")
	}
	// Guests 1 and 3 pay their parts; guest 2 doesn't.
	for _, p := range []*phone{a, c} {
		e.do("POST", loc(p.do("POST", "/g/"+p.key+"/pay", url.Values{"all": {"0"}})), url.Values{"action": {"pay"}}, false)
	}
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Not paid yet", "Guest 2 hasn&#39;t paid · 19,90 €",
		"39,80 € of 59,70 € paid", "Not everyone has paid", "ot-alert")
	e.expect(e.do("GET", "/admin/orders/5/split", nil, true), 200, "gp-due")
	// While guest 2 pays on their phone it says so.
	demo := loc(b.do("POST", "/g/"+b.key+"/pay", url.Values{"all": {"0"}}))
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Guest 2 is paying now")
	e.do("POST", demo, url.Values{"action": {"pay"}}, false)
	// Everyone paid: the table is free and the alarm is gone.
	if body := e.do("GET", "/admin/orders", nil, true).Body.String(); strings.Contains(body, "Not paid yet") {
		t.Fatal("alert after everyone paid")
	}
	if _, open := e.store.OpenBill(5); open {
		t.Fatal("table should be free")
	}
}

func TestQRSomethingNobodysAfterAllPaid(t *testing.T) {
	e := newEnv(t, nil)
	code := tableCode(t, e, 5)
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	a, b := join(t, e, code, 1), join(t, e, code, 2)
	burger, water := menuID(t, e, "E-Meal-burgare"), menuID(t, e, "Mineralvatten")
	a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + burger: {"1"}})
	b.do("POST", "/g/"+b.key+"/order", url.Values{"qty_" + burger: {"1"}})
	bill, _ := e.store.OpenBill(5)
	// The waiter brings water "for the table".
	e.do("POST", "/admin/orders/5/add", url.Values{"bill": {fmt.Sprint(bill.ID)}, "for": {"0"}, "qty_" + water: {"1"}}, true)
	for _, p := range []*phone{a, b} {
		e.do("POST", loc(p.do("POST", "/g/"+p.key+"/pay", url.Values{"all": {"0"}})), url.Values{"action": {"pay"}}, false)
	}
	// Both paid their parts, but the water is nobody's: the table stays open, in red.
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Not paid yet", "3,00 € isn&#39;t anyone&#39;s yet")
	// Any guest can pay what's left on their phone.
	e.expect(a.do("GET", "/g/"+a.key+"?part=live", nil), 200, "Pay for the whole table · 3,00 €")
	e.do("POST", loc(a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"1"}})), url.Values{"action": {"pay"}}, false)
	if _, open := e.store.OpenBill(5); open {
		t.Fatal("table should be free")
	}
}

func TestStaffAddsForOneGuest(t *testing.T) {
	e := newEnv(t, nil)
	code := tableCode(t, e, 5)
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	a, b := join(t, e, code, 1), join(t, e, code, 2)
	a.do("POST", "/g/"+a.key+"/name", url.Values{"name": {"Anna"}})
	burger, coffee := menuID(t, e, "E-Meal-burgare"), menuID(t, e, "Kaffe")
	b.do("POST", "/g/"+b.key+"/order", url.Values{"qty_" + burger: {"1"}})
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "Who is it for?", "The table", "Guest 1 · Anna", "Guest 2")
	bill, _ := e.store.OpenBill(5)
	// Anna asks the waiter for a coffee.
	if rec := e.do("POST", "/admin/orders/5/add", url.Values{"bill": {fmt.Sprint(bill.ID)}, "for": {"1"}, "qty_" + coffee: {"1"}}, true); !strings.Contains(loc(rec), "msg=addeddrinks") {
		t.Fatalf("add for guest 1: %s", loc(rec))
	}
	e.expect(a.do("GET", "/g/"+a.key+"?part=live", nil), 200, "Kaffe", "3,00 €")
	got, _ := e.store.BillByID(bill.ID)
	if len(got.Unassigned()) != 0 {
		t.Fatal("the coffee should be Anna's")
	}
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "Guest 1 · Anna")
	// Not for a guest who already paid.
	e.do("POST", loc(b.do("POST", "/g/"+b.key+"/pay", url.Values{"all": {"0"}})), url.Values{"action": {"pay"}}, false)
	if rec := e.do("POST", "/admin/orders/5/add", url.Values{"bill": {fmt.Sprint(bill.ID)}, "for": {"2"}, "qty_" + coffee: {"1"}}, true); !strings.Contains(loc(rec), "msg=sharepaid") {
		t.Fatalf("add for a paid guest: %s", loc(rec))
	}
}

func TestQROpensWhenGuestsArrive(t *testing.T) {
	e := newEnv(t, nil)
	// A paid booking for 4 at 12:00 (20 € deposit); they arrive.
	ref := refFromLocation(t, e.do("POST", "/book", guestForm(1, "12:00", "4"), false).Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	e.srv.Now = func() time.Time { return time.Date(2026, 10, 26, 12, 5, 0, 0, fixedNow.Location()) }
	e.do("POST", "/admin/bookings/"+ref+"/arrived", url.Values{"arrived": {"yes"}}, true)
	bk, _ := e.store.Get(ref)
	// Nobody tapped "Turn on": the guests can scan and order straight away.
	a := join(t, e, tableCode(t, e, bk.TableNo), 1)
	burger := menuID(t, e, "E-Meal-burgare")
	a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + burger: {"2"}})
	// Their deposit comes off: 39,80 € − 20,00 €.
	e.expect(a.do("GET", "/g/"+a.key+"?part=live", nil), 200, "19,80 €")
	e.expect(e.do("GET", fmt.Sprintf("/admin/orders/%d", bk.TableNo), nil, true), 200, "QR orders are on")

	// Walk-ins seated at a table on Orders: open too.
	e.do("POST", "/admin/orders/3/guests", url.Values{"guests": {"2"}}, true)
	if b, open := e.store.OpenBill(3); !open || !b.QROpen {
		t.Fatalf("walk-in table 3: open=%v %+v", open, b)
	}
}

func TestCallTheWaiterFromThePhone(t *testing.T) {
	e := newEnv(t, nil)
	// A Saturday evening (3 meals day).
	e.srv.Now = func() time.Time { return time.Date(2026, 10, 31, 18, 0, 0, 0, fixedNow.Location()) }
	code := tableCode(t, e, 5)
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	a := join(t, e, code, 1)
	a.do("POST", "/g/"+a.key+"/name", url.Values{"name": {"Anna"}})
	e.expect(a.do("GET", "/g/"+a.key+"?part=live", nil), 200, "Call the waiter")

	if rec := a.do("POST", "/g/"+a.key+"/call", url.Values{"kind": {"waiter"}}); loc(rec) != "/g/"+a.key+"?msg=called" {
		t.Fatalf("call: %s", loc(rec))
	}
	e.expect(a.do("GET", "/g/"+a.key+"?part=live", nil), 200, "Your waiter is on the way (called 18:00).", "Call again")
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "Wants a waiter", "From the phone of Guest 1 · Anna")
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Wants a waiter · Guest 1 · Anna")
	e.do("POST", "/admin/tables/5/answer", url.Values{}, true)
	e.expect(a.do("GET", "/g/"+a.key+"?part=live", nil), 200, "Call the waiter")

	// 3 meals on table 5: the phone can say "ready for meal 2".
	e.do("POST", "/admin/tables/5/start", url.Values{}, true)
	e.expect(a.do("GET", "/g/"+a.key+"?part=live", nil), 200, "Ready for meal 2")
	if rec := a.do("POST", "/g/"+a.key+"/call", url.Values{"kind": {"next"}}); loc(rec) != "/g/"+a.key+"?msg=nextmeal" {
		t.Fatalf("next meal: %s", loc(rec))
	}
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "Ready for meal 2", "From the phone of Guest 1 · Anna")
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Ready for meal 2", "ot-call")
	e.expect(a.do("GET", "/g/"+a.key+"?part=live", nil), 200, "Meal 2 is on its way.")
}

func TestTableCodeForBookedTables(t *testing.T) {
	e := newEnv(t, nil)
	var mails []string
	e.srv.Mail.Capture = func(to, subject, body string) { mails = append(mails, subject+"\n"+body) }
	// Anna books table for 12:00 and pays: her email and booking page show her table code.
	ref := refFromLocation(t, e.do("POST", "/book", guestForm(1, "12:00", "2"), false).Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	bk, _ := e.store.Get(ref)
	if len(bk.TableCode) != 4 {
		t.Fatalf("code %q", bk.TableCode)
	}
	found := false
	for _, m := range mails {
		if strings.Contains(m, "Your table code: "+bk.TableCode) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no table code in the emails:\n%s", strings.Join(mails, "\n---\n"))
	}
	e.expect(e.do("GET", "/b/"+ref, nil, false), 200, "Your table code", bk.TableCode)

	code := tableCode(t, e, bk.TableNo)
	at := func(clock string) {
		e.srv.Now = func() time.Time {
			d, _ := time.ParseInLocation("2006-01-02 15:04", "2026-10-26 "+clock, fixedNow.Location())
			return d
		}
	}
	// An hour before: nothing yet (the table is just closed for phones).
	at("11:00")
	page := e.do("GET", "/t/"+code, nil, false).Body.String()
	if strings.Contains(page, "booked for") || !strings.Contains(page, "isn&#39;t taking orders") {
		t.Fatal("an hour early: should just be closed")
	}
	// 15 minutes before: "booked, type your code".
	at("11:45")
	stranger := &phone{e: e, ip: "10.9.9.9"}
	e.expect(stranger.do("GET", "/t/"+code, nil), 200, "This table is booked for 12:00.", "table code")
	// Nobody can order there without the code.
	if rec := stranger.do("POST", "/t/"+code, url.Values{}); loc(rec) != "/t/"+code+"?msg=closed" {
		t.Fatalf("joining a booked table without the code: %s", loc(rec))
	}
	// Guessing is cut off after 5 wrong codes.
	wrong := "0000"
	if bk.TableCode == wrong {
		wrong = "1111"
	}
	for i := 0; i < 5; i++ {
		if rec := stranger.do("POST", "/t/"+code+"/unlock", url.Values{"pin": {wrong}}); loc(rec) != "/t/"+code+"?msg=badcode" {
			t.Fatalf("wrong code %d: %s", i, loc(rec))
		}
	}
	if rec := stranger.do("POST", "/t/"+code+"/unlock", url.Values{"pin": {bk.TableCode}}); loc(rec) != "/t/"+code+"?msg=slow" {
		t.Fatalf("6th try: %s", loc(rec))
	}
	// Anna types her code on her phone: checked in, table open, she's guest 1.
	anna := &phone{e: e, ip: "10.9.0.1"}
	rec := anna.do("POST", "/t/"+code+"/unlock", url.Values{"pin": {bk.TableCode}})
	if !strings.HasPrefix(loc(rec), "/g/") || !strings.HasSuffix(loc(rec), "?msg=welcome") {
		t.Fatalf("right code: %s", loc(rec))
	}
	if b, _ := e.store.Get(ref); b.ArrivedAt == nil {
		t.Fatal("not checked in")
	}
	// Her friends just scan and join now.
	join(t, e, code, 2)
	bill, _ := e.store.OpenBill(bk.TableNo)
	if len(bill.Split) != 2 || bill.BookingRef != ref {
		t.Fatalf("bill %+v", bill)
	}

	// Carl booked 14:00 but never comes: 20 minutes late the table is free.
	ref2 := refFromLocation(t, e.do("POST", "/book", guestForm(2, "14:00", "2"), false).Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref2, url.Values{}, false)
	bk2, _ := e.store.Get(ref2)
	code2 := tableCode(t, e, bk2.TableNo)
	// (Anna's group is done: their table no longer takes phone orders.)
	e.do("POST", fmt.Sprintf("/admin/orders/%d/qr", bk.TableNo), url.Values{"open": {"0"}}, true)
	at("14:19")
	e.expect(e.do("GET", "/t/"+code2, nil, false), 200, "This table is booked for 14:00.")
	at("14:20")
	if strings.Contains(e.do("GET", "/t/"+code2, nil, false).Body.String(), "booked for") {
		t.Fatal("still locked after the no-show limit")
	}
	if rec := (&phone{e: e, ip: "10.9.0.3"}).do("POST", "/t/"+code2+"/unlock", url.Values{"pin": {bk2.TableCode}}); loc(rec) != "/t/"+code2+"?msg=notbooked" {
		t.Fatalf("code after the no-show limit: %s", loc(rec))
	}
}

func TestTakeawayOnTheKitchenScreen(t *testing.T) {
	e := newEnv(t, nil) // Monday 09:00
	burger := menuID(t, e, "E-Meal-burgare")
	f := url.Values{"date": {"2026-10-26"}, "time": {"12:30"}, "name": {"Erik"}, "email": {"erik@example.com"},
		"phone": {"0401234567"}, "notes": {"Extra napkins"}, "qty_" + burger: {"2"}}
	ref := refFromLocation(t, e.do("POST", "/takeaway", f, false).Header().Get("Location"))
	// Not paid yet: the chef doesn't see it.
	if strings.Contains(e.do("GET", "/admin/kitchen", nil, true).Body.String(), "Takeaway #") {
		t.Fatal("unpaid order on the kitchen screen")
	}
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	bk, _ := e.store.Get(ref)
	no := fmt.Sprint(bk.ID)
	e.expect(e.do("GET", "/admin/kitchen", nil, true), 200, "Takeaway #"+no, "2×", "E-Meal-burgare",
		"Ordered online. Erik picks it up at 12:30.", "Extra napkins", "Seen · remind me at 11:30")
	// The chef taps Seen: it moves to "coming up".
	active, _ := e.store.KitchenNow(e.srv.Now())
	e.do("POST", fmt.Sprintf("/admin/kitchen/%d/seen", active[0].ID), url.Values{}, true)
	page := e.do("GET", "/admin/kitchen", nil, true).Body.String()
	if !strings.Contains(page, "Takeaway coming up") || !strings.Contains(page, "Back on the screen at 11:30") || strings.Contains(page, "Seen · remind me") {
		t.Fatalf("after Seen:\n%s", page)
	}
	// An hour before pickup it's back: start cooking.
	e.srv.Now = func() time.Time { return time.Date(2026, 10, 26, 11, 30, 0, 0, fixedNow.Location()) }
	e.expect(e.do("GET", "/admin/kitchen", nil, true), 200, "Start now · pickup in 60 min", "Done")
	// Picked up: off the screen.
	e.do("POST", "/admin/bookings/"+ref+"/pickedup", url.Values{}, true)
	if page := e.do("GET", "/admin/kitchen", nil, true).Body.String(); strings.Contains(page, "Start now") || !strings.Contains(page, "No orders right now") {
		t.Fatal("still on the kitchen screen after pickup")
	}
}

func TestChefMarksADishSoldOut(t *testing.T) {
	e := newEnv(t, nil)
	code := tableCode(t, e, 5)
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	a := join(t, e, code, 1)
	lax := menuID(t, e, "Laxfilé")
	e.expect(e.do("GET", "/admin/kitchen", nil, true), 200, "Sold out?", "Laxfilé")
	// The guest's menu was loaded before the chef tapped.
	e.do("POST", "/admin/menu/"+lax+"/soldout", url.Values{"soldout": {"1"}}, true)
	if rec := a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + lax: {"1"}}); loc(rec) != "/g/"+a.key+"?msg=soldout" {
		t.Fatalf("ordering a sold-out dish: %s", loc(rec))
	}
	if strings.Contains(a.do("GET", "/g/"+a.key, nil).Body.String(), "Laxfilé") {
		t.Fatal("sold-out dish still on the phone menu")
	}
	if strings.Contains(e.do("GET", "/takeaway", nil, false).Body.String(), "Laxfilé") {
		t.Fatal("sold-out dish still on the takeaway menu")
	}
	e.expect(e.do("GET", "/admin/menu", nil, true), 200, "Sold out today", "Back on")
	// Back on.
	e.do("POST", "/admin/menu/"+lax+"/soldout", url.Values{"soldout": {"0"}}, true)
	e.expect(a.do("GET", "/g/"+a.key, nil), 200, "Laxfilé")
}

func TestWaiterChecksPhoneOrders(t *testing.T) {
	e := newEnv(t, nil)
	code := tableCode(t, e, 5)
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	if rec := e.do("POST", "/admin/phone-orders/check", url.Values{"on": {"1"}}, true); !strings.Contains(loc(rec), "msg=checkon") {
		t.Fatalf("turn on: %s", loc(rec))
	}
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Phone orders are checked first")
	a := join(t, e, code, 1)
	a.do("POST", "/g/"+a.key+"/name", url.Values{"name": {"Anna"}})
	lax := menuID(t, e, "Laxfilé")
	if rec := a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + lax: {"1"}}); loc(rec) != "/g/"+a.key+"?msg=waiting" {
		t.Fatalf("order: %s", loc(rec))
	}
	live := a.do("GET", "/g/"+a.key+"?part=live", nil)
	e.expect(live, 200, "Waiting for your waiter", "1×", "Laxfilé")
	if strings.Contains(live.Body.String(), "Pay my part") {
		t.Fatal("pay button while the order waits")
	}
	if strings.Contains(e.do("GET", "/admin/kitchen", nil, true).Body.String(), "Laxfilé</span>") {
		t.Fatal("the chef got it before a waiter accepted")
	}
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "New phone orders", "Guest 1 · Anna:", "1× Laxfilé", "Accept", "Reject")
	o := e.store.PendingPhoneOrders()[0]
	e.do("POST", fmt.Sprintf("/admin/phone-orders/%d/accept", o.ID), url.Values{}, true)
	e.expect(e.do("GET", "/admin/kitchen", nil, true), 200, "Laxfilé", "Guest 1 · Anna")
	e.expect(a.do("GET", "/g/"+a.key+"?part=live", nil), 200, "Being made", "Pay my part")

	// Nobody taps: the 15-second job lets it through after 5 minutes.
	a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + lax: {"1"}})
	e.srv.Now = func() time.Time { return fixedNow.Add(5*time.Minute + time.Second) }
	e.srv.SweepPayments(context.Background())
	if len(e.store.PendingPhoneOrders()) != 0 {
		t.Fatal("not let through after 5 minutes")
	}
}

func TestEndOfDayEmail(t *testing.T) {
	e := newEnv(t, nil) // Monday 26 Oct: open 10:30–21:00
	var mails []string
	e.srv.Mail.Capture = func(to, subject, body string) { mails = append(mails, to+"\n"+subject+"\n"+body) }
	e.srv.Cfg.ManagerEmail = "boss@example.fi"
	at := func(h, m int) {
		e.srv.Now = func() time.Time { return time.Date(2026, 10, 26, h, m, 0, 0, fixedNow.Location()) }
	}
	at(12, 0)
	burger, soda := menuID(t, e, "E-Meal-burgare"), menuID(t, e, "Läsk 33 cl")
	e.do("POST", "/admin/orders/2/add", url.Values{"bill": {"0"}, "qty_" + burger: {"2"}, "qty_" + soda: {"3"}}, true)
	b2, _ := e.store.OpenBill(2)
	e.do("POST", "/admin/orders/2/pay", url.Values{"bill": {fmt.Sprint(b2.ID)}, "method": {"card"}}, true)
	e.do("POST", "/admin/orders/3/add", url.Values{"bill": {"0"}, "qty_" + burger: {"1"}}, true) // left open

	at(21, 20) // closes 21:00: not yet
	e.srv.Tick()
	if len(mails) != 0 {
		t.Fatalf("sent before 21:30: %v", mails)
	}
	at(21, 31)
	e.srv.Tick()
	if len(mails) != 1 {
		t.Fatalf("%d emails", len(mails))
	}
	for _, want := range []string{"boss@example.fi", "50,30 € at the tables", "Card terminal:        50,30 €", "3 × Läsk 33 cl", "2 × E-Meal-burgare", "(1 bill)",
		"Table 3: bill still open, 19,90 € not paid", "/owner?date=2026-10-26"} {
		if !strings.Contains(mails[0], want) {
			t.Fatalf("email missing %q:\n%s", want, mails[0])
		}
	}
	e.srv.Tick() // never twice
	if len(mails) != 1 {
		t.Fatal("sent twice")
	}
	owner := httptest.NewRequest("GET", "http://example.test/owner/summary?date=2026-10-26", nil)
	owner.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleOwner, e.srv.Now())})
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, owner)
	e.expect(rec, 200, "PAID AT THE TABLES: 50,30 €")
}

func TestFeedback(t *testing.T) {
	e := newEnv(t, nil)
	var mails []string
	e.srv.Mail.Capture = func(to, subject, body string) { mails = append(mails, to+"\n"+subject+"\n"+body) }
	e.srv.Cfg.ManagerEmail = "boss@example.fi"
	e.srv.Cfg.GoogleReviewURL = "https://g.page/r/OZMO/review"
	guest := &phone{e: e, ip: "10.8.0.1"}
	e.expect(guest.do("GET", "/review?table=5", nil), 200, "How was it?", "5 stars")

	// 2 stars from table 5, with a comment and an email for a reply.
	rec := guest.do("POST", "/review", url.Values{"stars": {"2"}, "table": {"5"}})
	ref := strings.TrimPrefix(loc(rec), "/review/")
	if len(ref) != 32 {
		t.Fatalf("stars: %s", loc(rec))
	}
	e.expect(guest.do("GET", "/review/"+ref, nil), 200, "★★☆☆☆", "What could we have done better?")
	guest.do("POST", "/review/"+ref, url.Values{"text": {"The soup was cold."}, "email": {"lisa@example.fi"}})
	if len(mails) != 1 {
		t.Fatalf("%d emails", len(mails))
	}
	for _, want := range []string{"boss@example.fi", "Feedback: ★★☆☆☆ (2 of 5)", "The soup was cold.", "Table: 5", "lisa@example.fi", "not published"} {
		if !strings.Contains(mails[0], want) {
			t.Fatalf("email missing %q:\n%s", want, mails[0])
		}
	}
	// Every guest, also a 2-star one, is offered the Google link.
	e.expect(guest.do("GET", "/review/"+ref, nil), 200, "Thank you!", "https://g.page/r/OZMO/review")
	// Sent once only.
	guest.do("POST", "/review/"+ref, url.Values{"text": {"again"}})
	if len(mails) != 1 {
		t.Fatal("feedback sent twice")
	}

	// 5 stars, no comment.
	ref2 := strings.TrimPrefix(loc(guest.do("POST", "/review", url.Values{"stars": {"5"}})), "/review/")
	guest.do("POST", "/review/"+ref2, url.Values{"skip": {"1"}})
	if !strings.Contains(mails[1], "★★★★★ (5 of 5)") || !strings.Contains(mails[1], "No comment, just the stars.") {
		t.Fatalf("5 stars email:\n%s", mails[1])
	}
	// Robots filling in the hidden field get nowhere.
	n := len(e.store.Reviews(0))
	guest.do("POST", "/review", url.Values{"stars": {"1"}, "website": {"http://spam.example"}})
	if len(e.store.Reviews(0)) != n {
		t.Fatal("a robot's review was saved")
	}
	// The owner sees it all; nothing is shown on the public site.
	owner := httptest.NewRequest("GET", "http://example.test/owner/feedback", nil)
	owner.Header.Set("Accept-Language", "en")
	owner.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleOwner, e.srv.Now())})
	orec := httptest.NewRecorder()
	e.h.ServeHTTP(orec, owner)
	e.expect(orec, 200, "3,5", "The soup was cold.", "lisa@example.fi", "1 under 4 stars")
	if strings.Contains(e.do("GET", "/", nil, false).Body.String(), "soup was cold") {
		t.Fatal("feedback published on the site")
	}
	if line := e.srv.feedbackLine("2026-10-26"); line != "2 ratings, average 3,5 ★ (1 under 4 stars)" {
		t.Fatalf("daily email line %q", line)
	}
}
