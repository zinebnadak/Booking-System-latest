package handlers

import (
	"encoding/json"
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

// outbox collects the emails the server sends during a test.
type outbox struct {
	mu   sync.Mutex
	mail []sentMail
}

type sentMail struct{ To, Subject, Body string }

func (o *outbox) capture(to, subject, body string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.mail = append(o.mail, sentMail{to, subject, body})
}

// find returns the emails to `to` whose subject or body contains text.
func (o *outbox) find(to, text string) []sentMail {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []sentMail
	for _, m := range o.mail {
		if m.To == to && (strings.Contains(m.Subject, text) || strings.Contains(m.Body, text)) {
			out = append(out, m)
		}
	}
	return out
}

func (o *outbox) reset() {
	o.mu.Lock()
	o.mail = nil
	o.mu.Unlock()
}

func withOutbox(e *env) *outbox {
	o := &outbox{}
	e.srv.Mail.Capture = o.capture
	return o
}

func (e *env) at(t time.Time) { e.srv.Now = func() time.Time { return t } }

func itoa(n int) string { return strconv.Itoa(n) }

// doHeader is a guest request with one extra header.
func (e *env) doHeader(method, path string, form url.Values, key, value string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(key, value)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func local(day, h, m int) time.Time {
	return time.Date(2026, 10, day, h, m, 0, 0, schedule.Location)
}

// bookAndPay books a table as guest i and pays the demo deposit.
func (e *env) bookAndPay(i int, date, clock, size string) string {
	e.t.Helper()
	f := guestForm(i, clock, size)
	f.Set("date", date)
	rec := e.do("POST", "/book", f, false)
	if rec.Code != 303 {
		e.t.Fatalf("booking %s %s: %d\n%s", date, clock, rec.Code, rec.Body.String())
	}
	ref := refFromLocation(e.t, rec.Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	return ref
}

func TestStaffPhoneBookings(t *testing.T) {
	e := newEnv(t, nil)
	out := withOutbox(e)

	// Phone booking with an email: confirmed at once, no deposit, guest gets a link.
	rec := e.do("POST", "/admin/bookings/new", url.Values{
		"date": {"2026-10-26"}, "time": {"18:00"}, "size": {"4"}, "name": {"Phone Guest"},
		"phone": {"040 111 2222"}, "email": {"phone@example.com"}, "occasion": {"birthday"}, "lang": {"en"},
	}, true)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "msg=phonebooked") {
		t.Fatalf("phone booking: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	day := e.store.BookingsOn("2026-10-26", fixedNow)
	if len(day) != 1 || day[0].Source != db.SourcePhone || day[0].DepositCents != 0 || day[0].Status != db.StatusPaid || day[0].TableNo != 1 {
		t.Fatalf("phone booking saved as %+v", day)
	}
	if m := out.find("phone@example.com", "/b/"+day[0].Ref); len(m) != 1 || strings.Contains(m[0].Body, "deposit of") {
		t.Fatalf("phone confirmation email: %+v", m)
	}
	// Without a name it's refused.
	rec = e.do("POST", "/admin/bookings/new", url.Values{"date": {"2026-10-26"}, "time": {"18:00"}, "size": {"2"}}, true)
	if !strings.Contains(rec.Header().Get("Location"), "msg=invalid") {
		t.Fatalf("phone booking without a name: %s", rec.Header().Get("Location"))
	}

	// There's no walk-in button any more: walk-ins are seated on Orders.
	e.at(local(26, 12, 7))
	if strings.Contains(e.do("GET", "/admin/day", nil, true).Body.String(), "dlg-walkin") {
		t.Fatal("the walk-in button should be gone")
	}
	e.expect(e.do("GET", "/admin/day", nil, true), 200, ">Phone<", "Birthday", "no deposit")

	// Reports: counted as a booking, but not as a deposit paid.
	st := computeStats(e.store.BookingsBetween("2026-10-26", "2026-10-26"), e.srv.Now())
	if st.TableBookings != 1 || st.ByPhone != 1 || st.DepositsPaid != 0 || st.Revenue != 0 {
		t.Fatalf("stats = %+v", st)
	}

	// Cancelling a phone booking: no money involved.
	rec = e.do("POST", "/admin/bookings/"+day[0].Ref+"/cancel", url.Values{"refund": {"no"}}, true)
	if !strings.Contains(rec.Header().Get("Location"), "msg=cancelfree") {
		t.Fatalf("cancel phone booking: %s", rec.Header().Get("Location"))
	}
	if m := out.find("phone@example.com", "cancelled"); len(m) != 1 || strings.Contains(m[0].Body, "0,00") {
		t.Fatalf("cancel email: %+v", m)
	}
	st = computeStats(e.store.BookingsBetween("2026-10-26", "2026-10-26"), e.srv.Now())
	if st.CancelledFree != 1 || st.TableCancelledKept != 0 {
		t.Fatalf("stats after cancel = %+v", st)
	}
}

func TestTakeawayPickedUp(t *testing.T) {
	e := newEnv(t, nil)
	menu := e.store.Menu(true)
	f := url.Values{"date": {"2026-10-26"}, "time": {"12:30"}, "name": {"Erik"}, "email": {"erik@example.com"}, "phone": {"0401234567"},
		"qty_" + itoa(menu[0].ID): {"2"}}
	ref := refFromLocation(t, e.do("POST", "/takeaway", f, false).Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	e.expect(e.do("GET", "/admin/day", nil, true), 200, "Picked up", "to pay at pickup")

	e.do("POST", "/admin/bookings/"+ref+"/pickedup", url.Values{}, true)
	b, _ := e.store.Get(ref)
	if b.EffectiveStatus(fixedNow) != db.StatusPickedUp {
		t.Fatalf("status %s", b.EffectiveStatus(fixedNow))
	}
	// A collected order can't be cancelled.
	rec := e.do("POST", "/admin/bookings/"+ref+"/cancel", url.Values{"refund": {"no"}}, true)
	if !strings.Contains(rec.Header().Get("Location"), "msg=pickedfinal") {
		t.Fatalf("cancel after pickup: %s", rec.Header().Get("Location"))
	}
	st := computeStats(e.store.BookingsBetween("2026-10-26", "2026-10-26"), fixedNow)
	if st.PickedUp != 1 || st.ToCollect != 0 || st.TakeawayOrders != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestGuestChangesAndCancels(t *testing.T) {
	e := newEnv(t, nil)
	out := withOutbox(e)

	// Wednesday 28 Oct 18:00 for 2 (10 €), booked on Monday: more than 24h ahead.
	ref := e.bookAndPay(1, "2026-10-28", "18:00", "2")
	e.expect(e.do("GET", "/b/"+ref, nil, false), 200, "Change booking", "Cancel booking", "refunded in full")

	// The change page only offers sizes with the same deposit (1–2).
	page := e.do("GET", "/b/"+ref+"/change", nil, false)
	e.expect(page, 200, "Change your booking", `data-size="1"`, `data-size="2"`, `value="18:00" checked`)
	if strings.Contains(page.Body.String(), `data-size="3"`) {
		t.Fatal("change page offers a size with a different deposit")
	}
	e.expect(e.do("GET", "/b/"+ref+"/change/times?date=2026-10-29&size=1", nil, false), 200, "19:00")
	rec := e.do("POST", "/b/"+ref+"/change", url.Values{"date": {"2026-10-29"}, "time": {"19:00"}, "size": {"4"}}, false)
	e.expect(rec, 422, "1 to 6")

	rec = e.do("POST", "/b/"+ref+"/change", url.Values{"date": {"2026-10-29"}, "time": {"19:00"}, "size": {"1"}}, false)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "msg=changed") {
		t.Fatalf("change: %d %s\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	b, _ := e.store.Get(ref)
	if b.Date != "2026-10-29" || b.Time != "19:00" || b.PartySize != 1 {
		t.Fatalf("after change: %+v", b)
	}
	if len(out.find("guest1@example.com", "has been changed")) != 1 || len(out.find("manager@example.test", "Booking changed by guest")) != 1 {
		t.Fatalf("change emails: %+v", out.mail)
	}
	e.expect(e.do("GET", "/b/"+ref+"?msg=changed", nil, false), 200, "Your booking is changed", "Thursday 29 October")

	// Cancel more than 24 hours before: refunded automatically.
	rec = e.do("POST", "/b/"+ref+"/cancel", url.Values{}, false)
	if !strings.Contains(rec.Header().Get("Location"), "msg=refunded") {
		t.Fatalf("cancel: %s", rec.Header().Get("Location"))
	}
	if b, _ := e.store.Get(ref); b.Status != db.StatusRefunded || b.CancelledBy != "guest" {
		t.Fatalf("after cancel: %+v", b)
	}
	if len(out.find("guest1@example.com", "has been refunded")) != 1 || len(out.find("manager@example.test", "Cancelled by guest")) != 1 {
		t.Fatalf("cancel emails: %+v", out.mail)
	}

	// Today at 18:00 (less than 24h): no online change, cancelling keeps the deposit.
	late := e.bookAndPay(2, "2026-10-26", "18:00", "4")
	e.expect(e.do("GET", "/b/"+late, nil, false), 200, "please call us", "the 20,00 € deposit is kept")
	e.expect(e.do("GET", "/b/"+late+"/change", nil, false), 409, "be changed online")
	e.do("POST", "/b/"+late+"/cancel", url.Values{}, false)
	if b, _ := e.store.Get(late); b.Status != db.StatusCancelled {
		t.Fatalf("late cancel: %+v", b)
	}

	// Another site can't cancel for the guest.
	third := e.bookAndPay(3, "2026-10-30", "18:00", "2")
	rec = e.doHeader("POST", "/b/"+third+"/cancel", url.Values{}, "Origin", "https://evil.example")
	if rec.Code != 403 {
		t.Fatalf("cross-site cancel: %d", rec.Code)
	}
}

func TestWaitingList(t *testing.T) {
	e := newEnv(t, nil)
	out := withOutbox(e)

	// Tuesday 27 Oct: all 6 tables taken at 18:00.
	var refs []string
	for i := 1; i <= 6; i++ {
		refs = append(refs, e.bookAndPay(i, "2026-10-27", "18:00", "2"))
	}
	e.expect(e.do("GET", "/book?date=2026-10-27&size=2", nil, false), 200, "Join the waiting list", "/waitlist?date=2026-10-27")
	e.expect(e.do("GET", "/waitlist?date=2026-10-27&size=2", nil, false), 200, "Waiting list", "18:00")
	// A day with nothing full goes straight to booking.
	if rec := e.do("GET", "/waitlist?date=2026-10-28", nil, false); rec.Code != 303 {
		t.Fatalf("waitlist for a free day: %d", rec.Code)
	}

	rec := e.do("POST", "/waitlist", url.Values{"date": {"2026-10-27"}, "size": {"3"}, "time": {"18:00"},
		"name": {"Wanda"}, "email": {"wanda@example.com"}, "phone": {"040 999 8888"}}, false)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/w/") {
		t.Fatalf("join: %d %s\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	wref := refFromLocation(t, rec.Header().Get("Location"))
	e.expect(e.do("GET", "/w/"+wref, nil, false), 200, "on the waiting list", "Around 18:00")
	if len(out.find("wanda@example.com", "waiting list")) != 1 {
		t.Fatalf("joined email: %+v", out.mail)
	}
	e.expect(e.do("GET", "/admin/day?date=2026-10-27", nil, true), 200, "Waiting list", "Wanda")

	// Someone cancels: Wanda is told at once, and only once.
	e.do("POST", "/admin/bookings/"+refs[0]+"/cancel", url.Values{"refund": {"yes"}}, true)
	free := out.find("wanda@example.com", "A table is free")
	if len(free) != 1 || !strings.Contains(free[0].Body, "18:00") || !strings.Contains(free[0].Body, "/book?date=2026-10-27&size=3") {
		t.Fatalf("free-table email: %+v", free)
	}
	e.srv.Tick()
	if n := len(out.find("wanda@example.com", "A table is free")); n != 1 {
		t.Fatalf("told %d times", n)
	}
	e.expect(e.do("GET", "/w/"+wref, nil, false), 200, "A table became free", "Book now")

	// Booking takes her off the list.
	f := guestForm(9, "18:00", "3")
	f.Set("date", "2026-10-27")
	f.Set("email", "wanda@example.com")
	ref := refFromLocation(t, e.do("POST", "/book", f, false).Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	if w := e.store.Waitlist("2026-10-27"); len(w) != 0 {
		t.Fatalf("still waiting after booking: %+v", w)
	}

	// Leaving the list by hand.
	rec = e.do("POST", "/waitlist", url.Values{"date": {"2026-10-27"}, "size": {"2"},
		"name": {"Olle"}, "email": {"olle@example.com"}, "phone": {"040 555 1212"}}, false)
	oref := refFromLocation(t, rec.Header().Get("Location"))
	e.do("POST", "/w/"+oref+"/remove", url.Values{}, false)
	e.expect(e.do("GET", "/w/"+oref, nil, false), 200, "off the waiting list")
}

func TestRemindersAndPrivacyCleanUp(t *testing.T) {
	e := newEnv(t, nil)
	out := withOutbox(e)

	// Booked Monday 09:00 for Tuesday 19:00, and Monday 20:00 for Tuesday 12:00.
	early := e.bookAndPay(1, "2026-10-27", "19:00", "2")
	e.at(local(26, 20, 0))
	recent := e.bookAndPay(2, "2026-10-27", "12:00", "2")

	// Monday 21:30: the early booking is under 24h away and 12h old: reminder.
	// The recent one was only just booked: no reminder yet.
	e.at(local(26, 21, 30))
	e.srv.Tick()
	if m := out.find("guest1@example.com", "See you tomorrow at 19:00"); len(m) != 1 || !strings.Contains(m[0].Body, "/b/"+early) {
		t.Fatalf("reminder: %+v", out.mail)
	}
	if len(out.find("guest2@example.com", "See you")) != 0 {
		t.Fatal("reminded a booking made an hour ago")
	}
	e.srv.Tick()
	if n := len(out.find("guest1@example.com", "See you")); n != 1 {
		t.Fatalf("reminded %d times", n)
	}
	_ = recent

	// Privacy: details go after the retention time; the money stays.
	e.srv.Cfg.RetentionDays = 30
	e.at(time.Date(2026, 12, 1, 10, 0, 0, 0, schedule.Location))
	e.srv.Tick()
	b, _ := e.store.Get(early)
	if !b.Anonymized || b.Email != "" || b.Phone != "" || b.DepositCents != 1000 {
		t.Fatalf("after clean-up: %+v", b)
	}
}

func TestTermsPrivacyAndApps(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Cfg.RetentionDays = 365
	e.expect(e.do("GET", "/terms", nil, false), 200, "Booking terms", "24 hours", "20 minutes", "non-refundable")
	e.expect(e.do("GET", "/privacy", nil, false), 200, "Privacy", "1 year after your visit", "Stripe")
	e.expect(e.do("GET", "/privacy?lang=sv", nil, false), 200, "Integritet", "1 år efter besöket")
	e.expect(e.do("GET", "/book", nil, false), 200, `href="/terms"`, `href="/privacy"`, `rel="manifest"`)

	for path, start := range map[string]string{"/manifest.webmanifest": "/", "/staff.webmanifest": "/admin/orders"} {
		rec := e.do("GET", path, nil, false)
		var m struct {
			StartURL string `json:"start_url"`
			Icons    []struct{ Src string }
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m.StartURL != start || len(m.Icons) != 3 {
			t.Fatalf("%s: %+v %v", path, m, err)
		}
		for _, ic := range m.Icons {
			if rec := e.do("GET", ic.Src, nil, false); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
				t.Fatalf("icon %s: %d %s", ic.Src, rec.Code, rec.Header().Get("Content-Type"))
			}
		}
	}
}
