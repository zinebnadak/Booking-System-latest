package handlers

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"emeal-booking/db"
	"emeal-booking/payments"
)

func TestTipWhenPayingOnThePhone(t *testing.T) {
	e := newEnv(t, nil) // demo payments
	code := tableCode(t, e, 5)
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	a, b := join(t, e, code, 1), join(t, e, code, 2)
	lax, burger := menuID(t, e, "Laxfilé"), menuID(t, e, "E-Meal-burgare")
	a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + lax: {"1"}})    // 26,90
	b.do("POST", "/g/"+b.key+"/order", url.Values{"qty_" + burger: {"1"}}) // 19,90

	// "Pay my part" leads to the tip choice, with exact amounts. Nothing is held yet.
	e.expect(a.do("GET", "/g/"+a.key, nil), 200, `href="/g/`+a.key+`/tip"`, `href="/g/`+a.key+`/tip?all=1"`)
	e.expect(a.do("GET", "/g/"+a.key+"/tip", nil), 200, "Add a tip?", "Your part is 26,90 €.",
		"No tip", "Pay 26,90 €", "5 % tip · 1,35 €", "Pay 28,25 €", "10 % tip · 2,69 €", "Pay 29,59 €")
	if len(e.store.Holds(1, e.srv.Now())) != 0 {
		t.Fatal("the tip page shouldn't hold anything")
	}
	e.expect(a.do("GET", "/g/"+a.key+"/tip?all=1", nil), 200, "paying 46,80 € for the whole table.", "Pay 51,48 €")

	// A made-up tip counts as none.
	rec := b.do("POST", "/g/"+b.key+"/pay", url.Values{"all": {"0"}, "tip": {"50"}})
	att, _ := e.store.AttemptByRef(strings.TrimPrefix(loc(rec), "/dev/tab/"))
	if att.TipCents != 0 {
		t.Fatalf("tip of 50 %% accepted: %+v", att)
	}
	b.do("POST", "/dev/tab/"+att.Ref, url.Values{"action": {"cancel"}})

	// Guest 1 pays their part with 10 %.
	rec = a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"0"}, "tip": {"10"}})
	ref := strings.TrimPrefix(loc(rec), "/dev/tab/")
	att, _ = e.store.AttemptByRef(ref)
	if att.Cents != 2690 || att.TipCents != 269 || att.Total() != 2959 {
		t.Fatalf("attempt %+v", att)
	}
	e.expect(a.do("GET", "/dev/tab/"+ref, nil), 200, "29,59 €", "26,90 € and a 2,69 € tip")
	a.do("POST", "/dev/tab/"+ref, url.Values{"action": {"pay"}})

	// It's on their page and their receipt, but not in their part or the bill.
	e.expect(a.do("GET", "/g/"+a.key, nil), 200, "Paid online", "with a 2,69 € tip. Thank you!")
	bill, _, _ := e.store.GuestByKey(a.key)
	if bill.Tips() != 269 || bill.ToPay() != 4680 {
		t.Fatalf("tips %d, to pay %d", bill.Tips(), bill.ToPay())
	}
	e.expect(e.do("GET", "/r/"+bill.Split[0].Ref, nil, false), 200, "Tip for the staff", "2,69 €")

	// Guest 2 pays at the terminal; the day's takings show the tip apart from the sales.
	e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {itoa(bill.ID)}, "guest": {"2"}, "method": {"card"}}, true)
	e.expect(e.do("GET", "/owner", nil, true), 200, "added on phones", "2,69 €", "46,80 €")
	_, report := e.srv.dailyReport(e.srv.Now(), e.srv.Now().Add(6*3600e9))
	if !strings.Contains(report, "Tips on phones:       2,69 €") || !strings.Contains(report, "PAID AT THE TABLES: 46,80 €") {
		t.Fatalf("report:\n%s", report)
	}
}

func TestTipIsSentToStripeAsItsOwnLine(t *testing.T) {
	fs := &tabStripe{refs: map[string]string{}, state: map[string]string{}, amounts: map[string]string{}}
	api := httptest.NewServer(fs)
	defer api.Close()
	e := newEnv(t, &payments.Client{SecretKey: "sk_test_x", WebhookSecret: "whsec_x", APIBase: api.URL})
	e.do("POST", "/admin/orders/6/qr", url.Values{"open": {"1"}}, true)
	a := join(t, e, tableCode(t, e, 6), 1)
	a.do("POST", "/g/"+a.key+"/order", url.Values{"qty_" + menuID(t, e, "Laxfilé"): {"1"}})
	a.do("POST", "/g/"+a.key+"/pay", url.Values{"all": {"0"}, "tip": {"5"}})
	if fs.amounts["cs_tab_1"] != "2690" || fs.tips["cs_tab_1"] != "135 Tip for the staff (5 %)" {
		t.Fatalf("amount %s, tip %q", fs.amounts["cs_tab_1"], fs.tips["cs_tab_1"])
	}
	// Stripe says it's paid: the tip is recorded with it.
	fs.pay("cs_tab_1")
	webhook(t, e, "checkout.session.completed", "cs_tab_1", fs.refs["cs_tab_1"])
	bill, _, _ := e.store.GuestByKey(a.key)
	if bill.Split[0].PaidBy != db.PaidOnline || bill.Tips() != 135 {
		t.Fatalf("paid by %q, tips %d", bill.Split[0].PaidBy, bill.Tips())
	}
}
