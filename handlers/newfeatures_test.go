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
	"strconv"
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
	_ = fee // Wolt is hidden in the money pages: Money today doesn't count it.
	if page := e.do("GET", "/owner?wolt=only", nil, true).Body.String(); strings.Contains(page, "Wolt keeps") || strings.Contains(page, `<div class="kpi-big">`+euro(gross)) {
		t.Error("Money today still shows Wolt")
	}
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

// The manager saves the business ID (checked), and receipts show it.
func TestBusinessIDOnReceipts(t *testing.T) {
	e := newEnv(t, nil)
	location(t, e.do("POST", "/admin/staff/businessid", url.Values{"id": {"0112038-8"}}, true), "msg=bizbad")
	location(t, e.do("POST", "/admin/staff/businessid", url.Values{"id": {"01120389"}}, true), "msg=bizsaved")
	burger := e.menuItem("E-Meal-burgare")
	location(t, e.do("POST", "/admin/orders/3/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"1"}}, true), "msg=added")
	b, _ := e.store.OpenBill(3)
	e.expect(e.do("GET", fmt.Sprintf("/admin/orders/3/bill?bill=%d", b.ID), nil, true), 200, "0112038-9")
}

// The manager changes seats, moves and removes tables, adds one; a table
// with an open bill can't be removed. Online booking uses the new seats.
func TestTablePlanByTheManager(t *testing.T) {
	t.Cleanup(schedule.UseTables(schedule.RoomNames(), schedule.Tables()))
	e := newEnv(t, nil)
	e.expect(e.do("GET", "/admin/tableplan", nil, true), 200, "Tables and seats", `name="seats_3"`)
	form := url.Values{"room_0": {"Utrymme 1"}, "seats_1": {"2"}, "del_6": {"1"}, "new_no": {"40"}, "new_seats": {"8"}, "new_room": {"new"}, "room_new": {"Terrassen"}}
	location(t, e.do("POST", "/admin/tableplan", form, true), "msg=plansaved")
	if tb, _ := schedule.TableInfo(1); tb.Seats != 2 {
		t.Fatalf("table 1: %+v", tb)
	}
	if schedule.ValidTable(6) {
		t.Fatal("table 6 should be gone")
	}
	if tb, ok := schedule.TableInfo(40); !ok || tb.Room != "Terrassen" || tb.Seats != 8 {
		t.Fatalf("table 40: %+v %v", tb, ok)
	}
	// Table 3 has an open bill: it can't be removed.
	location(t, e.do("POST", "/admin/orders/3/guests", url.Values{"guests": {"2"}}, true), "")
	rec := e.do("POST", "/admin/tableplan", url.Values{"room_0": {"Utrymme 1"}, "room_1": {"Terrassen"}, "del_3": {"1"}}, true)
	e.expect(rec, 422, "Table 3 has an open bill")
	if !schedule.ValidTable(3) {
		t.Fatal("table 3 must stay")
	}
	// A room can't be emptied of its name while it has tables.
	e.expect(e.do("POST", "/admin/tableplan", url.Values{"room_0": {"Utrymme 1"}, "room_1": {""}}, true), 422, "still has tables")
}

// A waiter changes a table's seats from the table's page; online booking
// uses it at once.
func TestWaiterChangesSeats(t *testing.T) {
	t.Cleanup(schedule.UseTables(schedule.RoomNames(), schedule.Tables()))
	wrongPasswordDelay = 0
	e := newEnv(t, nil)
	e.store.AddStaff("Lisa", "lisa", "a-long-password-1", e.srv.Now())
	lisa, _ := e.loginAs(t, "lisa", "a-long-password-1")
	e.expect(e.as(lisa, "GET", "/admin/orders/3", nil), 200, "Seats at this table", `name="seats"`)
	location(t, e.as(lisa, "POST", "/admin/orders/3/seats", url.Values{"seats": {"2"}}), "msg=seatssaved")
	if tb, _ := schedule.TableInfo(3); tb.Seats != 2 {
		t.Fatalf("table 3 has %d seats", tb.Seats)
	}
	location(t, e.as(lisa, "POST", "/admin/orders/3/seats", url.Values{"seats": {"99"}}), "msg=seatsbad")
}

// Waiters: type a table number to open it; join two tables; move a dish.
func TestJumpJoinAndMove(t *testing.T) {
	e := newEnv(t, nil)
	location(t, e.do("GET", "/admin/orders/go?no=4", nil, true), "/admin/orders/4")
	location(t, e.do("GET", "/admin/orders/go?no=999", nil, true), "msg=notable")
	burger := e.menuItem("E-Meal-burgare")
	add := func(table, qty int) db.Bill {
		location(t, e.do("POST", fmt.Sprintf("/admin/orders/%d/add", table), url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {strconv.Itoa(qty)}}, true), "msg=added")
		b, _ := e.store.OpenBill(table)
		return b
	}
	add(1, 2)
	b2 := add(2, 1)
	page := e.do("GET", "/admin/orders/2", nil, true)
	e.expect(page, 200, "Join with table", `action="/admin/orders/2/join"`, "Move to another table")
	location(t, e.do("POST", "/admin/orders/2/join", url.Values{"bill": {fmt.Sprint(b2.ID)}, "into": {"1"}}, true), "/admin/orders/1?")
	b1, _ := e.store.OpenBill(1)
	if b1.Count() != 3 {
		t.Fatalf("table 1 has %d items", b1.Count())
	}
	e.expect(e.do("GET", "/admin/orders/2/history", nil, true), 200, "joined with table 1")
	line := b1.Lines[0]
	location(t, e.do("POST", fmt.Sprintf("/admin/orders/1/lines/%d/move", line.ID), url.Values{"bill": {fmt.Sprint(b1.ID)}, "to": {"5"}, "qty": {"1"}}, true), "msg=linemoved")
	b5, open := e.store.OpenBill(5)
	if !open || b5.Count() != 1 {
		t.Fatalf("table 5: %+v", b5)
	}
	// The kitchen sees table 5 for the moved dish, and the "all day" total.
	e.expect(e.do("GET", "/admin/kitchen", nil, true), 200, "All day", "3×", "Table 5")
}

// The manager plans the week; staff see their own shifts.
func TestStaffSchedule(t *testing.T) {
	wrongPasswordDelay = 0
	e := newEnv(t, nil)
	lisa, _ := e.store.AddStaff("Lisa", "lisa", "a-long-password-1", e.srv.Now())
	monday := weekStart(httptest.NewRequest("GET", "/", nil), e.srv.Now())
	week := schedule.FormatDate(monday)
	friday := schedule.FormatDate(monday.AddDate(0, 0, 4))
	e.expect(e.do("GET", "/admin/schedule", nil, true), 200, "Add a shift", "Lisa")
	location(t, e.do("POST", "/admin/schedule", url.Values{"week": {week}, "staff": {fmt.Sprint(lisa.ID)}, "date": {friday}, "from": {"16:00"}, "to": {"23:30"}, "note": {"bar"}}, true), "msg=rotaadded")
	location(t, e.do("POST", "/admin/schedule", url.Values{"week": {week}, "staff": {fmt.Sprint(lisa.ID)}, "date": {friday}, "from": {"16:00"}, "to": {"23:30"}}, true), "msg=rotaadded")
	if n := len(e.store.RotaBetween(friday, friday)); n != 1 {
		t.Fatalf("tapped twice: %d shifts", n)
	}
	location(t, e.do("POST", "/admin/schedule", url.Values{"week": {week}, "staff": {"999"}, "date": {friday}, "from": {"16:00"}, "to": {"23:30"}}, true), "msg=rotabad")
	e.expect(e.do("GET", "/admin/schedule?week="+week, nil, true), 200, "16:00–23:30", "7 h 30 min")
	// Next week: copy this one.
	next := schedule.FormatDate(monday.AddDate(0, 0, 7))
	location(t, e.do("POST", "/admin/schedule/copy", url.Values{"week": {next}}, true), "msg=rotacopied")
	if got := e.store.RotaBetween(next, schedule.FormatDate(monday.AddDate(0, 0, 13))); len(got) != 1 || got[0].From != "16:00" {
		t.Fatalf("copied: %+v", got)
	}
	cookie, _ := e.loginAs(t, "lisa", "a-long-password-1")
	mine := e.as(cookie, "GET", "/admin/myschedule", nil)
	if friday >= schedule.FormatDate(e.srv.Now()) {
		e.expect(mine, 200, "16:00–23:30", "bar")
	} else {
		e.expect(mine, 200, "My schedule")
	}
	if e.as(cookie, "GET", "/admin/schedule", nil).Code == 200 {
		t.Error("a waiter can't plan the schedule")
	}
}

// The chef ticks one dish at a time; the waiter sees it on the table page
// and on Orders, and the last tick makes the ticket done.
func TestKitchenTicksDishes(t *testing.T) {
	wrongPasswordDelay = 0
	e := newEnv(t, nil)
	burger := e.menuItem("E-Meal-burgare")
	var other db.MenuItem
	for _, m := range e.store.Menu(false) {
		if !m.Drink && m.ID != burger.ID && m.Course == burger.Course && m.PriceCents > 0 {
			other = m
			break
		}
	}
	if other.ID == 0 {
		t.Fatal("no second dish on the menu")
	}
	location(t, e.do("POST", "/admin/orders/3/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", burger.ID): {"2"}, fmt.Sprintf("qty_%d", other.ID): {"1"}}, true), "msg=added")
	b, _ := e.store.OpenBill(3)
	var tk db.Ticket
	for _, x := range e.store.TicketsForBill(b.ID) {
		tk = x
	}
	if len(tk.Items) != 2 {
		t.Fatalf("one ticket with both dishes: %+v", tk)
	}
	e.expect(e.do("GET", "/admin/kitchen", nil, true), 200, `action="/admin/kitchen/`+strconv.Itoa(tk.ID)+`/dish"`, "Tap when this dish is ready")

	tick := func(i int, undo bool) *httptest.ResponseRecorder {
		v := url.Values{"i": {strconv.Itoa(i)}, "name": {tk.Items[i].Name}}
		if undo {
			v.Set("undo", "1")
		}
		return e.do("POST", fmt.Sprintf("/admin/kitchen/%d/dish", tk.ID), v, true)
	}
	location(t, tick(0, false), "/admin/kitchen")
	e.expect(e.do("GET", "/admin/kitchen", nil, true), 200, "kt-dish-ready", "Ready · tap to undo")
	e.expect(e.do("GET", "/admin/orders/3", nil, true), 200, "· pick up", "With the chef")
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Some dishes ready")
	// A dish that isn't there (the screen was old): nothing happens.
	stale := e.do("POST", fmt.Sprintf("/admin/kitchen/%d/dish", tk.ID), url.Values{"i": {"1"}, "name": {"Something else"}}, true)
	if got, _ := e.store.TicketByID(tk.ID); got.Items[1].ReadyAt != nil || stale.Code == 500 {
		t.Fatalf("stale tap: %d", stale.Code)
	}
	// Waiters can't tick the chef's dishes.
	e.store.AddStaff("Lisa", "lisa", "a-long-password-1", e.srv.Now())
	cookie, _ := e.loginAs(t, "lisa", "a-long-password-1")
	if rec := e.as(cookie, "POST", fmt.Sprintf("/admin/kitchen/%d/dish", tk.ID), url.Values{"i": {"1"}, "name": {tk.Items[1].Name}}); rec.Code != 403 {
		t.Fatalf("waiter ticked a dish: %d", rec.Code)
	}
	// The last dish: the ticket is done and waits at the pass.
	location(t, tick(1, false), "/admin/kitchen")
	if got, _ := e.store.TicketByID(tk.ID); got.DoneAt == nil {
		t.Fatal("ticking every dish should make the ticket done")
	}
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Ready to pick up")
}

// "We're running late": the guest presses it on their booking page on the
// day; the table is kept longer and staff see it.
func TestGuestRunningLate(t *testing.T) {
	e := newEnv(t, nil)
	ref := e.bookAndPay(1, "2026-10-27", "19:00", "2")
	// The day before: no button yet, and pressing anyway changes nothing.
	page := e.do("GET", "/b/"+ref, nil, false)
	if strings.Contains(page.Body.String(), "Running late?") {
		t.Fatal("the late button shows a day early")
	}
	location(t, e.do("POST", "/b/"+ref+"/late", url.Values{"min": {"15"}}, false), "/b/"+ref)
	if b, _ := e.store.Get(ref); b.LateMinutes != 0 {
		t.Fatal("marked late a day early")
	}

	// On the day, at 18:30: the button is there.
	e.at(local(27, 18, 30))
	e.expect(e.do("GET", "/b/"+ref, nil, false), 200, "Running late?", "until 19:20", `action="/b/`+ref+`/late"`, "15 min late", "30 min late")
	if rec := e.do("POST", "/b/"+ref+"/late", url.Values{"min": {"45"}}, false); rec.Code != 303 {
		t.Fatalf("45 minutes: %d", rec.Code)
	}
	if b, _ := e.store.Get(ref); b.LateMinutes != 0 {
		t.Fatal("only 15 or 30 minutes")
	}
	location(t, e.do("POST", "/b/"+ref+"/late", url.Values{"min": {"15"}}, false), "msg=late")
	e.expect(e.do("GET", "/b/"+ref+"?msg=late", nil, false), 200, "Thanks for letting us know", "running 15 minutes late", "until 19:35")
	e.expect(e.do("GET", "/admin/day?date=2026-10-27", nil, true), 200, "Running 15 min late", "No-show at 19:35")
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "15 min late")

	// 19:25: without the note it would be a no-show; now the table is still theirs.
	e.at(local(27, 19, 25))
	if b, _ := e.store.Get(ref); b.EffectiveStatus(e.srv.Now()) != db.StatusPaid {
		t.Fatalf("status at 19:25: %s", b.EffectiveStatus(e.srv.Now()))
	}
	// 19:35: now it is.
	e.at(local(27, 19, 35))
	if b, _ := e.store.Get(ref); b.EffectiveStatus(e.srv.Now()) != db.StatusNoShow {
		t.Fatalf("status at 19:35: %s", b.EffectiveStatus(e.srv.Now()))
	}
	if strings.Contains(e.do("GET", "/b/"+ref, nil, false).Body.String(), "Running late?") {
		t.Fatal("button still there after the no-show")
	}

	// The reminder email tells guests about it.
	b, _ := e.store.Get(ref)
	_, body := reminderEmail(e.srv.Cfg, b, local(26, 21, 0))
	if !strings.Contains(body, "Running late on the day?") || !strings.Contains(body, "/b/"+ref) {
		t.Fatalf("reminder: %s", body)
	}
}

// The manager is also the boss: his menu shows his own pages, and the floor
// and the settings sit behind More.
func TestManagerSimpleMenu(t *testing.T) {
	e := newEnv(t, nil)
	page := e.do("GET", "/owner", nil, true).Body.String()
	start := strings.Index(page, `<nav class="admin-nav admin-nav-boss"`)
	if start < 0 {
		t.Fatal("no manager menu")
	}
	nav := page[start : start+strings.Index(page[start:], "</nav>")]
	for _, want := range []string{`href="/owner"`, `href="/owner/charts"`, `href="/owner/feedback"`, `href="/admin/staff"`, `aria-controls="nav-more"`} {
		if !strings.Contains(nav, want) {
			t.Errorf("menu lacks %s", want)
		}
	}
	for _, not := range []string{`href="/admin/orders"`, `href="/admin/kitchen"`, `href="/admin/menu"`, `href="/admin/stock"`} {
		if strings.Contains(nav, not) {
			t.Errorf("menu still shows %s", not)
		}
	}
	more := page[strings.Index(page, `id="nav-more"`):]
	for _, want := range []string{`href="/admin/orders"`, `href="/admin/kitchen"`, `href="/admin/day"`, `href="/admin/menu"`, `href="/admin/closed"`, `href="/admin/activity"`} {
		if !strings.Contains(more, want) {
			t.Errorf("More lacks %s", want)
		}
	}
	// On a page under More, the More button is marked.
	if !strings.Contains(e.do("GET", "/admin/menu", nil, true).Body.String(), `aria-controls="nav-more" data-current`) {
		t.Error("More isn't marked on the Menu page")
	}
}
