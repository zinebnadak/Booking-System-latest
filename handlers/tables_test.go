package handlers

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"emeal-booking/payments"
	"emeal-booking/schedule"
)

// Saturday 31 October 2026, 18:00: a "3 meals" day.
var saturday = time.Date(2026, 10, 31, 18, 0, 0, 0, schedule.Location)

func (e *env) token(table int) string {
	for _, b := range e.store.Buttons() {
		if b.Table == table {
			return b.Token
		}
	}
	e.t.Fatalf("no button on table %d", table)
	return ""
}

// press sends a button press the way the real button does.
func (e *env) press(token, body, contentType string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "http://example.test/api/press", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	req.RemoteAddr = "192.0.2.10:5000"
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func pressReply(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %s", rec.Body.String())
	}
	return m
}

func TestButtonAPI(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }

	// No code, a wrong code, or a code in the address: refused.
	e.expect(e.press("", "", "", nil), 401, `"ok":false`)
	e.expect(e.press("0123456789abcdef0123456789abcdef", "", "", nil), 401)
	req := httptest.NewRequest("POST", "http://example.test/api/press?token="+e.token(1), nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	e.expect(rec, 401)
	e.expect(e.do("GET", "/api/press", nil, false), 405)

	// The right code (as the real button sends it) calls a waiter today.
	rec = e.press(e.token(2), `{"battery":76}`, "application/json", nil)
	e.expect(rec, 200)
	if m := pressReply(t, rec); m["ok"] != true || m["table"] != float64(2) || m["kind"] != "waiter" || m["again"] != false {
		t.Fatalf("reply: %v", m)
	}
	if b := e.store.Buttons()[1]; b.Battery != 76 || b.LastSeen == nil {
		t.Fatalf("battery not saved: %+v", b)
	}
	// Other ways to send the code: a header, or a form field.
	e.expect(e.press("", "", "", map[string]string{"X-Button-Token": e.token(3)}), 200, `"table":3`)
	e.expect(e.press("", "token="+e.token(5)+"&battery=40", "application/x-www-form-urlencoded", nil), 200, `"table":5`)

	// The daily check-in only says "alive".
	creq := httptest.NewRequest("POST", "http://example.test/api/checkin", strings.NewReader(`{"battery":55}`))
	creq.Header.Set("Content-Type", "application/json")
	creq.Header.Set("Authorization", "Bearer "+e.token(6))
	crec := httptest.NewRecorder()
	e.h.ServeHTTP(crec, creq)
	e.expect(crec, 200, `"table":6`)
	if tb := e.store.TablesNow()[5]; tb.Call != nil {
		t.Fatal("a check-in called a waiter")
	}

	// Too many wrong codes from one address: "try later". A right code from
	// the same address (the restaurant's Wi-Fi) still works.
	for i := 0; i < limitButton.n; i++ {
		e.press("ffffffffffffffffffffffffffffffff", "", "", nil)
	}
	e.expect(e.press("ffffffffffffffffffffffffffffffff", "", "", nil), 429)
	e.expect(e.press(e.token(1), "", "", nil), 200, `"table":1`)
}

func TestTablesScreenThreeMeals(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }

	// Staff only.
	rec := e.do("GET", "/admin/orders", nil, false)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Fatalf("not logged in: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// It's all on Orders: the day's note there, and each table's own page has the buttons.
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "3 meals runs today", "Table 6")
	e.expect(e.do("GET", "/admin/orders/4", nil, true), 200, "Start 3 meals")
	e.expect(e.do("GET", "/admin/orders/4?lang=sv", nil, true), 200, "Starta 3 rätter", "Bord 4")

	// Table 4 starts the deal from its page; meal 1 is eaten; they press the button.
	location(t, e.do("POST", "/admin/tables/4/start", url.Values{"back": {"table"}}, true), "/admin/orders/4?msg=dealstarted")
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Eating meal 1 of 3")
	e.expect(e.do("GET", "/admin/orders/4", nil, true), 200, "Eating meal 1 of 3", "Meal 2 served", "Stop 3 meals")
	e.srv.Now = func() time.Time { return saturday.Add(22 * time.Minute) }
	rec = e.press(e.token(4), "", "", nil)
	if m := pressReply(t, rec); m["kind"] != "next" || m["meal"] != float64(1) {
		t.Fatalf("press: %v", m)
	}
	e.srv.Now = func() time.Time { return saturday.Add(25 * time.Minute) }
	tiles := e.do("GET", "/admin/orders?part=tiles", nil, true)
	e.expect(tiles, 200, "Ready for meal 2", "ot-call", "data-alert-key=")
	e.expect(e.do("GET", "/admin/orders/4", nil, true), 200, "Ready for meal 2", "waiting 3 min", "Meal 2 served", "Not yet, still eating")
	if strings.Contains(tiles.Body.String(), "<html") {
		t.Fatal("the tiles part should not be a whole page")
	}

	// Meal 2 served. A second waiter's out-of-date tap is refused, not a 3rd meal.
	rec = e.do("POST", "/admin/tables/4/serve", url.Values{"meal": {"2"}}, true)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "msg=mealserved") {
		t.Fatalf("serve: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec = e.do("POST", "/admin/tables/4/serve", url.Values{"meal": {"2"}}, true)
	if !strings.Contains(rec.Header().Get("Location"), "msg=tablestale") {
		t.Fatalf("double serve: %s", rec.Header().Get("Location"))
	}
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Eating meal 2 of 3")

	// The screen's script gets plain answers instead of redirects.
	live := func(path string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://example.test"+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Live", "1")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleManager, e.srv.Now())})
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	e.expect(live("/admin/tables/4/serve", url.Values{"meal": {"3"}}), 204)
	e.expect(live("/admin/tables/4/serve", url.Values{"meal": {"4"}}), 409)
	e.press(e.token(4), "", "", nil)
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Finished all meals")
	e.expect(e.do("GET", "/admin/orders/4", nil, true), 200, "Finished all meals", "Table cleared")
	e.expect(live("/admin/tables/4/end", url.Values{}), 204)
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "3 meals today", "Tables with 3 meals")

	// A press with no deal running: "wants a waiter", answered with Done.
	e.press(e.token(1), "", "", nil)
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Wants a waiter")
	e.expect(live("/admin/tables/1/answer", url.Values{}), 204)
	if tb := e.store.TablesNow()[0]; tb.Call != nil {
		t.Fatal("call not answered")
	}

	// Another site can't press staff buttons for them.
	req := httptest.NewRequest("POST", "http://example.test/admin/tables/2/start", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleManager, e.srv.Now())})
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	e.expect(rec, 403)
}

func TestNoDealOnWeekdays(t *testing.T) {
	e := newEnv(t, nil) // Monday
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "3 meals runs on Saturday and Sunday")
	// The table's page still has 3 meals, but says when it runs, and starting it asks first.
	page := e.do("GET", "/admin/orders/2", nil, true).Body.String()
	if strings.Contains(page, ">Start 3 meals<") || !strings.Contains(page, "Start 3 meals today anyway") || !strings.Contains(page, "3 meals runs on Saturday and Sunday.") {
		t.Fatal("Monday: no plain start, but a start-anyway")
	}
	rec := e.do("POST", "/admin/tables/2/start", url.Values{}, true)
	if !strings.Contains(rec.Header().Get("Location"), "msg=tablestale") {
		t.Fatalf("started on a Monday by itself: %s", rec.Header().Get("Location"))
	}
	location(t, e.do("POST", "/admin/tables/2/start", url.Values{"back": {"table"}, "anyway": {"1"}}, true), "/admin/orders/2?msg=dealstarted")
	e.expect(e.do("GET", "/admin/orders/2", nil, true), 200, "Eating meal 1 of 3", "Meal 2 served")
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Eating meal 1 of 3")
	e.expect(e.do("GET", "/admin/orders?lang=sv", nil, true), 200, "3 rätter gäller lördag och söndag")
}

func TestTablesLiveEvents(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	ts := httptest.NewServer(e.h)
	defer ts.Close()
	defer e.srv.StopStreams()

	req, _ := http.NewRequest("GET", ts.URL+"/admin/tables/events", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleManager, e.srv.Now())})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	lines := make(chan string, 20)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	waitChange := func() {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatal("stream closed")
				}
				if l == "event: change" {
					return
				}
			case <-deadline:
				t.Fatal("no change event")
			}
		}
	}
	waitChange() // sent straight away on connecting
	pr, _ := http.NewRequest("POST", ts.URL+"/api/press", nil)
	pr.Header.Set("Authorization", "Bearer "+e.token(3))
	if r, err := http.DefaultClient.Do(pr); err != nil || r.StatusCode != 200 {
		t.Fatalf("press: %v", err)
	}
	waitChange() // the press reaches the open screen
}

func TestOwnerButtonCodes(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Cfg.OwnerUser, e.srv.Cfg.OwnerPassword = "owner", "pw2"
	asOwner := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://example.test"+path, nil)
		req.Header.Set("Accept-Language", "en")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleOwner, e.srv.Now())})
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	// The manager sees the codes.
	e.expect(e.do("GET", "/owner/buttons", nil, true), 200)
	old := e.token(2)
	e.expect(asOwner("GET", "/owner/buttons"), 200, old, "http://example.test/api/press", "never: not set up yet")

	rec := asOwner("POST", "/owner/buttons/2/new")
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "show=2") {
		t.Fatalf("new code: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if e.token(2) == old {
		t.Fatal("code not changed")
	}
	e.expect(e.press(old, "", "", nil), 401)
	e.expect(e.press(e.token(2), "", "", nil), 200)
}

func TestDemoButtons(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	e.expect(e.do("GET", "/demo/buttons", nil, false), 200, "Test buttons", "/demo/buttons/6")
	rec := e.do("POST", "/demo/buttons/3", url.Values{}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/demo/buttons?r=waiter&t=3" {
		t.Fatalf("demo press: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	e.expect(e.do("GET", "/demo/buttons?r=waiter&t=3", nil, false), 200, "Table 3: staff see that you&#39;d like a waiter.")
	if tb := e.store.TablesNow()[2]; tb.Call == nil {
		t.Fatal("demo press didn't reach the table")
	}
	e.expect(e.do("POST", "/demo/buttons/99", url.Values{}, false), 404)

	// With real payments (a live restaurant) the test buttons don't exist.
	live := newEnv(t, &payments.Client{SecretKey: "sk_test_123"})
	live.expect(live.do("GET", "/demo/buttons", nil, false), 404)
	live.expect(live.do("POST", "/demo/buttons/3", url.Values{}, false), 404)
}
