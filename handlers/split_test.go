package handlers

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"emeal-booking/db"
)

// Four friends at table 5 each pay for their own dish; two of them shared
// the wine. One trip to the table: everyone pays in turn, each gets their own
// receipt (printed, or by scanning its QR code), and table 5's history keeps
// the full bill.
func TestSplitBillEachPaysTheirOwn(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	id := func(name string) string { return fmt.Sprint(e.menuItem(name).ID) }
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"},
		"qty_" + id("E-Meal-burgare"): {"1"}, "qty_" + id("Fisksoppa"): {"1"}, "qty_" + id("Laxfilé"): {"1"},
		"qty_" + id("Svamprisotto"): {"1"}, "qty_" + id("Husets vin, glas"): {"1"},
	}, true), "msg=addedfood")
	bill, _ := e.store.OpenBill(5)
	b := fmt.Sprint(bill.ID)
	lineOf := map[string]string{}
	for _, l := range bill.Lines {
		lineOf[l.Name] = fmt.Sprint(l.ID)
	}

	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "Split the bill", "Paid at the terminal")
	e.expect(e.do("GET", "/admin/orders/5/split", nil, true), 200, "How many are paying?")
	location(t, e.do("POST", "/admin/orders/5/split/guests", url.Values{"bill": {b}, "guests": {"4"}}, true), "msg=splitset")

	// Nobody can pay until every dish has an owner.
	location(t, e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {b}, "guest": {"1"}, "method": {"card"}}, true), "msg=unassigned")
	for name, who := range map[string][]string{
		"E-Meal-burgare": {"1"}, "Fisksoppa": {"2"}, "Laxfilé": {"3"}, "Svamprisotto": {"4"}, "Husets vin, glas": {"1", "2"},
	} {
		location(t, e.do("POST", "/admin/orders/5/split/assign", url.Values{"bill": {b}, "line": {lineOf[name]}, "g": who}, true), "msg=assigned")
	}
	page := e.do("GET", "/admin/orders/5/split", nil, true)
	// Burger 19,90 + half a glass of wine (8,50) = 24,15; soup 15,50 + 4,25 = 19,75.
	e.expect(page, 200, "Guest 1", "24,15 €", "Guest 2", "19,75 €", "26,90 €", "21,90 €", "Husets vin, glas (shared by 2)")

	// The whole table can't be paid in one go any more.
	location(t, e.do("POST", "/admin/orders/5/pay", url.Values{"bill": {b}, "method": {"card"}}, true), "msg=billsplit")

	// Every receipt at once, each with its own QR code.
	rc := e.do("GET", "/admin/orders/5/receipts?bill="+b, nil, true)
	e.expect(rc, 200, "Guest 1 of 4", "Guest 4 of 4", "Print all 4", "/static/qrcode.js", `data-qr="http://example.test/r/`)
	codes := regexp.MustCompile(`data-qr="http://example\.test/r/([0-9a-f]{32})"`).FindAllStringSubmatch(rc.Body.String(), -1)
	if len(codes) != 4 {
		t.Fatalf("%d QR codes, want 4", len(codes))
	}

	// They pay one after the other.
	location(t, e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {b}, "guest": {"1"}, "method": {"card"}}, true), "msg=guestpaid")
	location(t, e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {b}, "guest": {"2"}, "method": {"cash"}}, true), "msg=guestpaid")
	// Guest 1 paid, so the wine can't move any more.
	location(t, e.do("POST", "/admin/orders/5/split/assign", url.Values{"bill": {b}, "line": {lineOf["Husets vin, glas"]}, "g": {"3"}}, true), "msg=sharepaid")
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, "Split · 2 of 4 paid")
	location(t, e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {b}, "guest": {"3"}, "method": {"card"}}, true), "msg=guestpaid")
	location(t, e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {b}, "guest": {"4"}, "method": {"card"}}, true), "msg=allpaid")
	if _, open := e.store.OpenBill(5); open {
		t.Fatal("table 5 should be free")
	}

	// The day's takings count every part by how it was paid.
	e.expect(e.do("GET", "/owner", nil, true), 200, "In cash</dt><dd>19,75 €", "92,70 €")

	// Guest 2 scans their QR code: only what they had, no login needed.
	guest2 := codes[1][1]
	pub := e.do("GET", "/r/"+guest2, nil, false)
	e.expect(pub, 200, "Guest 2 of 4", "Fisksoppa", "Husets vin, glas", "shared by 2", "19,75 €", "Your part", "Paid in cash")
	for _, other := range []string{"E-Meal-burgare", "Laxfilé", "Svamprisotto", "Guest 1 of 4"} {
		if strings.Contains(pub.Body.String(), other) {
			t.Fatalf("guest 2's receipt shows %q", other)
		}
	}
	if pub.Header().Get("Cache-Control") != "no-store" || pub.Header().Get("X-Robots-Tag") != "noindex" {
		t.Fatalf("receipt headers: %v", pub.Header())
	}
	e.expect(e.do("GET", "/r/0123456789abcdef0123456789abcdef", nil, false), 404)
	e.expect(e.do("GET", "/r/short", nil, false), 404)

	// Table 5's history: one bill with the full amount, and each guest's part.
	h := e.do("GET", "/admin/orders/5/history", nil, true)
	e.expect(h, 200, "Bill 1", "92,70 €", "Split: 4 guests paid separately", "Guest 3", "26,90 €", "Show the receipts")

	// In Swedish too.
	e.expect(e.do("GET", "/r/"+guest2+"?lang=sv", nil, false), 200, "Gäst 2 av 4", "Din del", "delas av 2")
}

func TestSplitEvenlyAndUndo(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	id := func(name string) string { return fmt.Sprint(e.menuItem(name).ID) }
	e.do("POST", "/admin/orders/2/add", url.Values{"bill": {"0"}, "qty_" + id("Läsk 33 cl"): {"3"}, "qty_" + id("Fisksoppa"): {"1"}}, true)
	bill, _ := e.store.OpenBill(2)
	b := fmt.Sprint(bill.ID)
	e.do("POST", "/admin/orders/2/split/guests", url.Values{"bill": {b}, "guests": {"3"}}, true)
	location(t, e.do("POST", "/admin/orders/2/split/even", url.Values{"bill": {b}}, true), "msg=spliteven")
	// 3 × 3,50 + 15,50 = 26,00 between 3: 8,67 + 8,67 + 8,66.
	e.expect(e.do("GET", "/admin/orders/2/split", nil, true), 200, "8,67 €", "8,66 €")

	// Three sodas on one line, made into three lines.
	var soda db.BillLine
	for _, l := range bill.Lines {
		if l.Qty == 3 {
			soda = l
		}
	}
	location(t, e.do("POST", "/admin/orders/2/split/separate", url.Values{"bill": {b}, "line": {fmt.Sprint(soda.ID)}}, true), "msg=separated")
	got, _ := e.store.OpenBill(2)
	if len(got.Lines) != 4 || len(got.Unassigned()) != 3 {
		t.Fatalf("after separating: %d lines, %d unassigned", len(got.Lines), len(got.Unassigned()))
	}

	// A tap by mistake can be taken back; then not split after all.
	e.do("POST", "/admin/orders/2/split/even", url.Values{"bill": {b}}, true)
	e.do("POST", "/admin/orders/2/split/pay", url.Values{"bill": {b}, "guest": {"1"}, "method": {"card"}}, true)
	location(t, e.do("POST", "/admin/orders/2/split/guests", url.Values{"bill": {b}, "guests": {"1"}}, true), "msg=sharepaid")
	location(t, e.do("POST", "/admin/orders/2/split/undo", url.Values{"bill": {b}, "guest": {"1"}}, true), "msg=guestundone")
	location(t, e.do("POST", "/admin/orders/2/split/guests", url.Values{"bill": {b}, "guests": {"1"}}, true), "msg=unsplit")
	location(t, e.do("POST", "/admin/orders/2/pay", url.Values{"bill": {b}, "method": {"card"}}, true), "msg=billpaid")
}

// On a demo on the Mac (BASE_URL is localhost), the QR code points at the
// address the waiter's phone used, so guests on the same Wi-Fi can open it.
func TestReceiptAddressOnTheMac(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Cfg.BaseURL = "http://localhost:8080"
	req := httptest.NewRequest("GET", "http://192.168.1.23:8080/admin/orders/1", nil)
	if got := e.srv.publicBase(req); got != "http://192.168.1.23:8080" {
		t.Fatalf("demo address %q", got)
	}
	e.srv.Cfg.BaseURL = "https://book.ozmo.ax"
	req.Host = "evil.example"
	if got := e.srv.publicBase(req); got != "https://book.ozmo.ax" {
		t.Fatalf("live address %q", got)
	}
}

// Split for 4, two have paid, then a fifth friend turns out to be paying too.
func TestAddAFifthGuestAfterSomePaid(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	id := func(name string) string { return fmt.Sprint(e.menuItem(name).ID) }
	e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"},
		"qty_" + id("E-Meal-burgare"): {"1"}, "qty_" + id("Fisksoppa"): {"1"}, "qty_" + id("Laxfilé"): {"1"},
		"qty_" + id("Svamprisotto"): {"1"}, "qty_" + id("Pannacotta"): {"1"},
	}, true)
	bill, _ := e.store.OpenBill(5)
	b := fmt.Sprint(bill.ID)
	line := map[string]string{}
	for _, l := range bill.Lines {
		line[l.Name] = fmt.Sprint(l.ID)
	}
	e.do("POST", "/admin/orders/5/split/guests", url.Values{"bill": {b}, "guests": {"4"}}, true)
	for name, g := range map[string]string{"E-Meal-burgare": "1", "Fisksoppa": "2", "Laxfilé": "3", "Svamprisotto": "4", "Pannacotta": "4"} {
		e.do("POST", "/admin/orders/5/split/assign", url.Values{"bill": {b}, "line": {line[name]}, "g": {g}}, true)
	}
	e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {b}, "guest": {"1"}, "method": {"card"}}, true)
	e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {b}, "guest": {"2"}, "method": {"card"}}, true)

	// "+ Add a guest" is there even though two have paid.
	e.expect(e.do("GET", "/admin/orders/5/split", nil, true), 200, "+ Add a guest")
	location(t, e.do("POST", "/admin/orders/5/split/guests", url.Values{"bill": {b}, "guests": {"5"}}, true), "msg=guestadded")
	// The pannacotta was the fifth person's, not guest 4's.
	location(t, e.do("POST", "/admin/orders/5/split/assign", url.Values{"bill": {b}, "line": {line["Pannacotta"]}, "g": {"5"}}, true), "msg=assigned")
	e.expect(e.do("GET", "/admin/orders/5/split", nil, true), 200, "Guest 5", "1× Pannacotta", "8,50 €")
	// Guests who paid can't be taken away.
	location(t, e.do("POST", "/admin/orders/5/split/guests", url.Values{"bill": {b}, "guests": {"1"}}, true), "msg=sharepaid")

	for _, g := range []string{"3", "4"} {
		location(t, e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {b}, "guest": {g}, "method": {"card"}}, true), "msg=guestpaid")
	}
	location(t, e.do("POST", "/admin/orders/5/split/pay", url.Values{"bill": {b}, "guest": {"5"}, "method": {"cash"}}, true), "msg=allpaid")
	paid, _ := e.store.BillByID(bill.ID)
	card, cash, _ := paid.PaidWith()
	if len(paid.Split) != 5 || cash != 850 || card+cash != paid.Total() {
		t.Fatalf("after 5 paid: %d guests, card %d cash %d of %d", len(paid.Split), card, cash, paid.Total())
	}
}

// Six at the table, each had one dish. Guest 1 pays for guests 1, 2 and 3;
// then "the rest pay in one go". Receipts say who paid for whom.
func TestOnePaymentForSeveralGuests(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	id := func(name string) string { return fmt.Sprint(e.menuItem(name).ID) }
	e.do("POST", "/admin/orders/6/add", url.Values{"bill": {"0"}, "qty_" + id("E-Meal-burgare"): {"6"}}, true)
	bill, _ := e.store.OpenBill(6)
	b := fmt.Sprint(bill.ID)
	e.do("POST", "/admin/orders/6/split/guests", url.Values{"bill": {b}, "guests": {"6"}}, true)
	e.do("POST", "/admin/orders/6/split/even", url.Values{"bill": {b}}, true) // 6 burgers, one each: 19,90 each
	e.expect(e.do("GET", "/admin/orders/6/split", nil, true), 200, "One payment for several guests", "Guest 6 · 19,90 €")

	location(t, e.do("POST", "/admin/orders/6/split/pay", url.Values{"bill": {b}, "guest": {"3", "1", "2"}, "method": {"card"}}, true), "msg=paidtogether")
	page := e.do("GET", "/admin/orders/6/split", nil, true)
	e.expect(page, 200, "Paid for guests 1, 2 and 3 together", "Guest 1 paid for them")

	// Guest 2's receipt: guest 1 paid it. Guest 1's: paid for 2 and 3 as well, 59,70 € in all.
	paid, _ := e.store.BillByID(bill.ID)
	e.expect(e.do("GET", "/r/"+paid.Split[1].Ref, nil, false), 200, "Guest 1 paid this for you.")
	e.expect(e.do("GET", "/r/"+paid.Split[0].Ref, nil, false), 200, "You also paid for guests 2 and 3: 59,70 € in total.")

	// Undo takes back the whole payment for the three of them.
	location(t, e.do("POST", "/admin/orders/6/split/undo", url.Values{"bill": {b}, "guest": {"2"}}, true), "msg=guestundone")
	if got, _ := e.store.BillByID(bill.ID); got.AnySharePaid() {
		t.Fatal("undo left some of the group paid")
	}
	// Again, then everyone left pays in one go (cash): the table is free.
	e.do("POST", "/admin/orders/6/split/pay", url.Values{"bill": {b}, "guest": {"1", "2", "3"}, "method": {"card"}}, true)
	location(t, e.do("POST", "/admin/orders/6/split/pay", url.Values{"bill": {b}, "guest": {"4", "5", "6"}, "method": {"cash"}}, true), "msg=allpaid")
	done, _ := e.store.BillByID(bill.ID)
	card, cash, _ := done.PaidWith()
	if card != 5970 || cash != 5970 || done.Open() {
		t.Fatalf("card %d cash %d open %v", card, cash, done.Open())
	}
	// Nobody picked: nothing happens.
	e.do("POST", "/admin/orders/6/add", url.Values{"bill": {"0"}, "qty_" + id("Kaffe"): {"2"}}, true)
	nb, _ := e.store.OpenBill(6)
	e.do("POST", "/admin/orders/6/split/guests", url.Values{"bill": {fmt.Sprint(nb.ID)}, "guests": {"2"}}, true)
	location(t, e.do("POST", "/admin/orders/6/split/pay", url.Values{"bill": {fmt.Sprint(nb.ID)}, "method": {"card"}}, true), "msg=billstale")
}
