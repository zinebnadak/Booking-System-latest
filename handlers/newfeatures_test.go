package handlers

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// Money today counts what the tables paid (card, cash…) and gift cards
// sold, not just the website; a bill counts on the day it's paid.
func TestTableMoneyOnMoneyToday(t *testing.T) {
	e := newEnv(t, nil)
	burger := e.menuItem("E-Meal-burgare")
	add := func(table int) db.Bill {
		location(t, e.do("POST", fmt.Sprintf("/admin/orders/%d/add", table), url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"2"}}, true), "msg=added")
		b, _ := e.store.OpenBill(table)
		return b
	}
	b3, b5 := add(3), add(5)
	if _, err := e.store.PayBill(b3.ID, db.PaidCard, e.srv.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.PayBill(b5.ID, db.PaidCash, e.srv.Now()); err != nil {
		t.Fatal(err)
	}
	total := b3.ToPay() + b5.ToPay()
	page := e.do("GET", "/owner", nil, true)
	e.expect(page, 200, "At the tables", "Bills paid", `<div class="kpi-big">`+euro(total), "Cash check at closing")
	if !strings.Contains(page.Body.String(), euro(b5.ToPay())) {
		t.Error("the cash should show")
	}
	// Charts: the money chart has it too.
	e.expect(e.do("GET", "/owner/charts", nil, true), 200, euro(total))
}

// The manager counts the drawer at closing: expected = change at the start
// + cash taken; a recount replaces the count and says so.
func TestCashCheck(t *testing.T) {
	e := newEnv(t, nil)
	burger := e.menuItem("E-Meal-burgare")
	location(t, e.do("POST", "/admin/orders/3/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"1"}}, true), "msg=added")
	b, _ := e.store.OpenBill(3)
	if _, err := e.store.PayBill(b.ID, db.PaidCash, e.srv.Now()); err != nil {
		t.Fatal(err)
	}
	today := schedule.FormatDate(e.srv.Now())
	cash := b.ToPay()
	location(t, e.do("POST", "/owner/cash", url.Values{"date": {today}, "float": {"abc"}, "counted": {"1"}}, true), "msg=cashbad")
	counted := cash + 15000 - 200 // 2 € missing
	location(t, e.do("POST", "/owner/cash", url.Values{"date": {today}, "float": {"150"}, "counted": {fmt.Sprintf("%d,%02d", counted/100, counted%100)}, "note": {"till roll"}}, true), "msg=cashsaved")
	c, ok := e.store.CashCountOn(today)
	if !ok || c.Expected() != cash+15000 || c.Diff() != -200 || c.Float != 15000 {
		t.Fatalf("count %+v", c)
	}
	e.expect(e.do("GET", "/owner?date="+today, nil, true), 200, "Missing", "−2,00 €", "150,00")
	location(t, e.do("POST", "/owner/cash", url.Values{"date": {today}, "float": {"150"}, "counted": {fmt.Sprintf("%d,%02d", (cash+15000)/100, (cash+15000)%100)}}, true), "msg=cashsaved")
	c, _ = e.store.CashCountOn(today)
	if c.Diff() != 0 || c.Previous != 1 {
		t.Fatalf("recount %+v", c)
	}
	if e.store.CashFloat() != 15000 {
		t.Error("the float should be remembered for tomorrow")
	}
}

// Wolt's cut: the manager sets it, and the pages show what Wolt keeps and
// what we keep; Money today counts what we keep.
func TestWoltCut(t *testing.T) {
	e := newEnv(t, nil)
	burger := e.menuItem("E-Meal-burgare")
	page := loc(e.do("POST", "/admin/wolt", url.Values{"name": {"Lisa"}}, true))
	location(t, e.do("POST", page+"/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"2"}}, true), "msg=added")
	b, _ := e.store.OpenBill(schedule.WoltFirst)
	location(t, e.do("POST", page+"/woltpickup", url.Values{"bill": {fmt.Sprint(b.ID)}}, true), "msg=woltdone")
	location(t, e.do("POST", "/owner/woltpct", url.Values{"pct": {"75"}}, true), "msg=woltpctbad")
	location(t, e.do("POST", "/owner/woltpct", url.Values{"pct": {"30"}}, true), "msg=woltpctsaved")
	gross := b.Total()
	fee := (gross*30 + 50) / 100
	e.expect(e.do("GET", "/owner?wolt=only", nil, true), 200, `<div class="kpi-big">`+euro(gross-fee), "Wolt keeps", euro(fee), "30 % of each order")
}

// Waiters and the chef start and end their shift from their own login;
// it lands on the clock card the manager sees.
func TestShiftFromOwnLogin(t *testing.T) {
	wrongPasswordDelay = 0
	e := newEnv(t, nil)
	if _, err := e.store.AddStaff("Lisa", "lisa", "a-long-password-1", e.srv.Now()); err != nil {
		t.Fatal(err)
	}
	lisa, _ := e.loginAs(t, "lisa", "a-long-password-1")
	e.expect(e.as(lisa, "GET", "/admin/orders", nil), 200, "Start shift")
	location(t, e.as(lisa, "POST", "/admin/shift", url.Values{"do": {"start"}, "back": {"/admin/orders"}}), "msg=shiftstarted")
	e.expect(e.as(lisa, "GET", "/admin/orders", nil), 200, "On shift since")
	start := e.srv.Now()
	e.srv.Now = func() time.Time { return start.Add(3 * time.Hour) }
	location(t, e.as(lisa, "POST", "/admin/shift", url.Values{"do": {"end"}, "back": {"//evil.example"}}), "/admin/orders?msg=shiftended")
	e.expect(e.do("GET", "/admin/hours", nil, true), 200, "Lisa")
	location(t, e.as(lisa, "POST", "/admin/shift", url.Values{"do": {"end"}}), "msg=shiftnone")
}

// Phones that turned notifications on get them: the chef a new order, the
// waiter their table's food when it's ready. A dead subscription is dropped.
func TestPushNotifications(t *testing.T) {
	wrongPasswordDelay = 0
	e := newEnv(t, nil)
	var mu sync.Mutex
	got := map[string]int{}
	gone := map[string]bool{}
	svc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if gone[r.URL.Path] {
			w.WriteHeader(http.StatusGone)
			return
		}
		if r.Header.Get("Content-Encoding") == "aes128gcm" && strings.HasPrefix(r.Header.Get("Authorization"), "vapid t=") {
			got[r.URL.Path]++
		}
		w.WriteHeader(201)
	}))
	defer svc.Close()
	p, err := e.srv.pusher()
	if err != nil {
		t.Fatal(err)
	}
	p.Client = svc.Client()

	lisa, _ := e.store.AddStaff("Lisa", "lisa", "a-long-password-1", e.srv.Now())
	kalle, _ := e.store.AddStaff("Kalle", "kalle", "a-long-password-2", e.srv.Now())
	e.store.SetStaffChef(kalle.ID, true)
	lisaC, _ := e.loginAs(t, "lisa", "a-long-password-1")
	kalleC, _ := e.loginAs(t, "kalle", "a-long-password-2")

	key := e.as(lisaC, "GET", "/admin/push/key", nil)
	var k struct{ Key string }
	if json.Unmarshal(key.Body.Bytes(), &k); len(k.Key) < 80 {
		t.Fatalf("public key %q", key.Body.String())
	}
	subscribe := func(c *http.Cookie, path string) {
		t.Helper()
		ua, _ := ecdh.P256().GenerateKey(rand.Reader)
		auth := make([]byte, 16)
		rand.Read(auth)
		body := fmt.Sprintf(`{"endpoint":%q,"keys":{"p256dh":%q,"auth":%q}}`, svc.URL+path,
			base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(auth))
		req := httptest.NewRequest("POST", "http://example.test/admin/push/subscribe", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 204 {
			t.Fatalf("subscribe: %d %s", rec.Code, rec.Body.String())
		}
	}
	subscribe(lisaC, "/lisa")
	subscribe(kalleC, "/kalle")

	burger := e.menuItem("E-Meal-burgare")
	location(t, e.as(lisaC, "POST", "/admin/orders/3/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"1"}}), "msg=added")
	e.srv.pushWG.Wait()
	mu.Lock()
	if got["/kalle"] != 1 || got["/lisa"] != 0 {
		t.Fatalf("new order: %v", got)
	}
	mu.Unlock()

	b, _ := e.store.OpenBill(3)
	if b.Waiter != lisa.ID {
		t.Fatalf("Lisa should have the table, has %d", b.Waiter)
	}
	var tk int
	for id := range e.store.TicketsForBill(b.ID) {
		tk = id
	}
	location(t, e.as(kalleC, "POST", fmt.Sprintf("/admin/kitchen/%d/done", tk), url.Values{}), "")
	e.srv.pushWG.Wait()
	mu.Lock()
	if got["/lisa"] != 1 {
		t.Fatalf("food ready: %v", got)
	}
	gone["/lisa"] = true
	mu.Unlock()

	// Lisa's phone dropped its subscription: the next message forgets it.
	location(t, e.as(kalleC, "POST", fmt.Sprintf("/admin/kitchen/%d/undo", tk), url.Values{}), "")
	location(t, e.as(kalleC, "POST", fmt.Sprintf("/admin/kitchen/%d/done", tk), url.Values{}), "")
	e.srv.pushWG.Wait()
	for _, s := range e.store.PushSubs() {
		if strings.HasSuffix(s.Endpoint, "/lisa") {
			t.Fatal("a dead subscription should be removed")
		}
	}
	e.expect(e.do("GET", "/sw.js", nil, false), 200, "showNotification")
}

// Walk-ins are seated on Orders: an open table is taken for online
// booking (the normal 2 hours from when it opened, or longer while the bill
// is still open), and Bookings shows it as seated.
func TestOrdersTableIsTakenOnline(t *testing.T) {
	e := newEnv(t, nil)
	e.at(local(26, 12, 7))
	date := "2026-10-26"
	free := func(clock string) int {
		return e.store.FreeTables(date, []string{clock}, 0, true, e.srv.Now())[clock]
	}
	before, later := free("12:30"), free("15:00")
	location(t, e.do("POST", "/admin/orders/3/guests", url.Values{"guests": {"2"}}, true), "")
	if free("12:30") != before-1 {
		t.Fatalf("table 3 should be taken at 12:30: %d → %d", before, free("12:30"))
	}
	if free("15:00") != later {
		t.Fatal("after the normal stay the table is free again for booking")
	}
	e.expect(e.do("GET", "/admin/day", nil, true), 200, "Seated on Orders", `href="/admin/orders/3"`)
	// Still there at 14:30 with the bill open: taken for the next half hour.
	e.at(local(26, 14, 30))
	if free("14:30") != before-1 {
		t.Fatal("an open bill keeps its table")
	}
	b, _ := e.store.OpenBill(3)
	e.store.VoidBill(b.ID)
	if free("14:30") != before {
		t.Fatal("once the bill is closed the table is free")
	}
}

// After a table pays, Orders asks whether the guest wants a receipt: one
// for the whole bill, printed straight away. On the split page each guest's
// own bill (or receipt) can be printed.
func TestReceiptAfterPaying(t *testing.T) {
	e := newEnv(t, nil)
	burger := e.menuItem("E-Meal-burgare")
	location(t, e.do("POST", "/admin/orders/3/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"2"}}, true), "msg=added")
	b, _ := e.store.OpenBill(3)
	rec := e.do("POST", "/admin/orders/3/pay", url.Values{"bill": {fmt.Sprint(b.ID)}, "method": {"card"}}, true)
	location(t, rec, fmt.Sprintf("rc=%d", b.ID))
	page := e.do("GET", loc(rec), nil, true)
	e.expect(page, 200, "Does the guest want a receipt?", fmt.Sprintf("/admin/orders/3/bill?bill=%d&amp;print=1", b.ID), "No receipt")
	e.expect(e.do("GET", fmt.Sprintf("/admin/orders/3/bill?bill=%d&print=1", b.ID), nil, true), 200, "data-autoprint", "Paid by card", "Back to orders")
	if strings.Contains(e.do("GET", "/admin/orders", nil, true).Body.String(), "Does the guest want a receipt?") {
		t.Error("without rc there's no question")
	}

	// Split between two: each guest's bill can be printed on its own.
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"2"}}, true), "msg=added")
	b5, _ := e.store.OpenBill(5)
	if err := e.store.SplitBill(b5.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SplitEvenly(b5.ID); err != nil {
		t.Fatal(err)
	}
	split := e.do("GET", "/admin/orders/5/split", nil, true)
	e.expect(split, 200, "Print bill", fmt.Sprintf("receipts?bill=%d&amp;guest=1&amp;print=1", b5.ID), fmt.Sprintf("receipts?bill=%d&amp;guest=2&amp;print=1", b5.ID))
	one := e.do("GET", fmt.Sprintf("/admin/orders/5/receipts?bill=%d&guest=2&print=1", b5.ID), nil, true)
	e.expect(one, 200, "data-autoprint", "Pro forma · not a receipt")
	if n := strings.Count(one.Body.String(), `class="guest-bill rc"`); n != 1 {
		t.Fatalf("one guest's bill only, got %d", n)
	}
}
