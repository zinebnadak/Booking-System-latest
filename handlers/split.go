package handlers

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

// Splitting a bill, and receipts with a QR code.
//
//   - /admin/orders/{table}/split     who had what, then each guest pays their part
//   - /admin/orders/{table}/receipts  every receipt of a bill, to print in one go, each with a QR code
//   - /r/{code}                       a guest's own receipt, opened by scanning the QR code (no login)
//
// The table's bill stays one bill with the full amount; the guests' parts
// hang under it, so the table's history shows both.

// ---------- receipts ----------

// receiptLine is one row on a receipt.
type receiptLine struct {
	Qty      int
	SharedBy int // >1: shared with others
	Name     string
	Note     string
	Cents    int
	Each     int // price per item when Qty > 1
}

// receiptView is one receipt: the whole bill (Guest 0) or one guest's part.
type receiptView struct {
	Table, No, BillID int
	Guest, Guests     int
	When              string
	Lines             []receiptLine
	Food              int // food and drinks before the deposit
	Deposit           int
	Total             int // to pay
	Paid              bool
	PaidBy            string // db.PaidCard, db.PaidCash, db.PaidSplit
	PaidAt            string
	URL               string // the online copy (for the QR code)
	Restaurant        string
	Name              string // the guest's name, if they gave one at the table
	PaidByGuest       int    // another guest paid this part for them
	PaidFor           []int  // this guest paid for these guests too
	PaidForTotal      int    // what that one payment came to
	Tip               int    // a tip added when paying on the phone (not part of Total)
	VAT               []db.VATRow
	Payments          []db.Payment // paid in parts (whole bill)
	Left              int          // still to pay after them
	Address, Phone    string       // the restaurant's, printed at the top
	BusinessID        string       // FO-nummer, printed at the bottom
}

func clockOf(t time.Time) string { return t.In(schedule.Location).Format("15:04") }

// publicBase is the address a guest's phone can open: for receipts and for
// paying a deposit. On a demo running on this computer (BASE_URL is
// localhost), it's the address the page was opened with, e.g.
// http://192.168.1.23:8080 on the same Wi-Fi, or the https://….trycloudflare.com
// link from phone-test.command (which says https in X-Forwarded-Proto).
func (s *Server) publicBase(r *http.Request) string {
	u, err := url.Parse(s.Cfg.BaseURL)
	if err == nil && r != nil && r.Host != "" {
		h := u.Hostname()
		ip := net.ParseIP(h)
		if h == "localhost" || (ip != nil && ip.IsLoopback()) {
			scheme := "http"
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			return scheme + "://" + r.Host
		}
	}
	return s.Cfg.BaseURL
}

// receiptFor builds a receipt for the whole bill (guest 0) or one guest.
func (s *Server) receiptFor(b db.Bill, guest int, base string, now time.Time) receiptView {
	v := receiptView{Table: b.Table, BillID: b.ID, Guest: guest, Guests: len(b.Split), Restaurant: s.Cfg.RestaurantName,
		Address: s.Cfg.Address, Phone: s.Cfg.RestaurantPhone, BusinessID: s.Cfg.BusinessID}
	v.No = billNumbers(s.Store.BillsForTable(b.Table))[b.ID]
	at := now
	if guest == 0 {
		for _, l := range b.Lines {
			rl := receiptLine{Qty: l.Qty, Name: l.Name, Cents: l.Total()}
			if l.DiscountPct > 0 {
				rl.Name += fmt.Sprintf(" (−%d %%)", l.DiscountPct)
			}
			if l.Qty > 1 {
				rl.Each = l.PriceCents
			}
			v.Lines = append(v.Lines, rl)
		}
		v.Food, v.Deposit, v.Total, v.Tip = b.Total(), b.Deposit(), b.ToPay(), b.Tips()
		v.VAT = db.VATTable(b.VATByRate())
		v.Payments, v.Left = b.Payments, b.Left()
		if b.PaidAt != nil {
			v.Paid, v.PaidBy, at = true, b.PaidBy, *b.PaidAt
			v.PaidAt = clockOf(*b.PaidAt)
		}
		v.URL = base + "/r/" + b.Ref
	} else {
		pay, lines, dep := b.Parts()
		if guest >= 1 && guest <= len(b.Split) {
			for _, sl := range lines[guest-1] {
				v.Lines = append(v.Lines, receiptLine{Qty: sl.Qty, SharedBy: sl.SharedBy, Name: sl.Name, Cents: sl.Cents})
				v.Food += sl.Cents
			}
			v.Deposit, v.Total = dep[guest-1], pay[guest-1]
			v.VAT = db.VATTable(db.ShareVATByRate(lines[guest-1]))
			sh := b.Split[guest-1]
			v.Name, v.Tip = sh.Name, sh.TipCents
			v.Payments, v.Left = sh.Payments, b.ShareLeft(guest) // paid in parts (a gift card first)
			if sh.PaidAt != nil {
				v.Paid, v.PaidBy, at = true, sh.PaidBy, *sh.PaidAt
				v.PaidAt = clockOf(*sh.PaidAt)
				if sh.Payer != 0 && sh.Payer != guest {
					v.PaidByGuest = sh.Payer
				}
				if together := b.PaidTogether(guest); len(together) > 1 {
					for _, g := range together {
						v.PaidForTotal += pay[g-1]
						if g != guest {
							v.PaidFor = append(v.PaidFor, g)
						}
					}
				}
			}
			v.URL = base + "/r/" + sh.Ref
		}
	}
	v.When = shortDateL(langEN, schedule.FormatDate(at)) + " " + clockOf(at)
	return v
}

// receiptsPage shows every receipt of a bill, one after the other, each with
// its QR code: print them all at once before going to the table.
func (s *Server) receiptsPage(w http.ResponseWriter, r *http.Request) {
	table := tableFromPath(r)
	bill, err := s.Store.BillByID(atoiDefault(r.URL.Query().Get("bill"), 0))
	if table == 0 || err != nil || bill.Table != table || bill.Voided {
		s.notFound(w, r)
		return
	}
	lang := langOf(r)
	now := s.Now()
	base := s.publicBase(r)
	var list []receiptView
	only := atoiDefault(r.URL.Query().Get("guest"), 0) // one guest's receipt (they asked for a printed one)
	if bill.IsSplit() {
		for _, sh := range bill.Split {
			if only > 0 && sh.Guest != only {
				continue
			}
			v := s.receiptFor(bill, sh.Guest, base, now)
			v.When = shortDateL(lang, schedule.FormatDate(now)) + " " + clockOf(now)
			if sh.PaidAt != nil {
				v.When = shortDateL(lang, schedule.FormatDate(*sh.PaidAt)) + " " + clockOf(*sh.PaidAt)
			}
			list = append(list, v)
		}
	} else {
		v := s.receiptFor(bill, 0, base, now)
		at := now
		if bill.PaidAt != nil {
			at = *bill.PaidAt
		}
		v.When = shortDateL(lang, schedule.FormatDate(at)) + " " + clockOf(at)
		list = append(list, v)
	}
	s.render(w, r, "order_receipts", http.StatusOK, M{
		"Admin": true, "Bare": true, "QR": true, "Title": stf(lang, "Receipts, table %d", table),
		"Table": table, "Bill": bill, "Receipts": list,
	})
}

// publicReceipt is a guest's receipt, opened from the QR code. The code in
// the address is long and random, so nobody can guess another guest's.
func (s *Server) publicReceipt(w http.ResponseWriter, r *http.Request) {
	bill, guest, ok := s.Store.ReceiptByRef(r.PathValue("ref"))
	if !ok {
		s.notFound(w, r)
		return
	}
	v := s.receiptFor(bill, guest, "", s.Now())
	lang := langOf(r)
	at := s.Now()
	switch {
	case guest > 0 && bill.Split[guest-1].PaidAt != nil:
		at = *bill.Split[guest-1].PaidAt
	case guest == 0 && bill.PaidAt != nil:
		at = *bill.PaidAt
	}
	v.When = longDate(lang, schedule.FormatDate(at)) + ", " + clockOf(at)
	w.Header().Set("X-Robots-Tag", "noindex")
	s.render(w, r, "receipt", http.StatusOK, M{"Title": st(lang, "Your receipt"), "R": v})
}

// ---------- splitting ----------

type splitChip struct {
	G      int
	On     bool
	Locked bool // that guest already paid
}

type splitLine struct {
	db.BillLine
	Chips  []splitChip
	Locked bool // part of a paid guest's share: can't change
}

type splitGuest struct {
	Guest    int
	Pay      int
	Deposit  int
	Items    []db.ShareLine
	Paid     bool
	PaidBy   string
	PaidAt   string
	Payer    int   // paid together: the guest who paid for them all
	Together []int // this guest paid for these guests (themselves included)
	Label    string
	Phone    bool         // joined with the table's QR code
	Held     bool         // someone is paying this part on their phone right now
	Parts    []db.Payment // paid some of it already (a gift card first)
	Left     int          // still to pay of their part
}

func (s *Server) splitPage(w http.ResponseWriter, r *http.Request) {
	table := tableFromPath(r)
	if table == 0 {
		s.notFound(w, r)
		return
	}
	bill, open := s.Store.OpenBill(table)
	if !open {
		redirectMsg(w, r, orderPath(table), nil, "billstale")
		return
	}
	lang := langOf(r)
	now := s.Now()
	paid := map[int]bool{}
	var guests []splitGuest
	if bill.IsSplit() {
		pay, items, dep := bill.Parts()
		for i, sh := range bill.Split {
			g := splitGuest{Guest: sh.Guest, Pay: pay[i], Deposit: dep[i], Items: items[i], Paid: sh.PaidAt != nil, PaidBy: sh.PaidBy,
				Payer: sh.Payer, Together: bill.PaidTogether(sh.Guest), Label: guestLabel(lang, sh), Phone: sh.Key != "",
				Parts: sh.Payments, Left: bill.ShareLeft(sh.Guest)}
			for _, a := range s.Store.Holds(bill.ID, now) {
				for _, x := range a.Guests {
					if x == sh.Guest && (now.Before(a.Until) || a.SessionID != "") {
						g.Held = true
					}
				}
			}
			if sh.PaidAt != nil {
				g.PaidAt = clockOf(*sh.PaidAt)
				paid[sh.Guest] = true
			}
			if g.Held {
				paid[sh.Guest] = true // locked while they pay, like a paid part
			}
			guests = append(guests, g)
		}
	}
	var lines []splitLine
	for _, l := range bill.Lines {
		v := splitLine{BillLine: l}
		for _, g := range l.Guests {
			v.Locked = v.Locked || paid[g]
		}
		for g := 1; g <= len(bill.Split); g++ {
			v.Chips = append(v.Chips, splitChip{G: g, On: l.Had(g), Locked: paid[g]})
		}
		lines = append(lines, v)
	}
	unassigned := 0
	for _, l := range bill.Unassigned() {
		unassigned += l.Total()
	}
	counts := []int{2, 3, 4, 5, 6, 7, 8}
	var more []int
	for n := 9; n <= db.MaxGuests; n++ {
		more = append(more, n)
	}
	unpaid := 0 // guests who can still pay together (not those who paid some already)
	for _, g := range guests {
		if !g.Paid && !g.Held && len(g.Parts) == 0 {
			unpaid++
		}
	}
	s.render(w, r, "admin_split", http.StatusOK, M{
		"Admin": true, "Nav": "orders", "Title": stf(lang, "Split the bill, table %d", table), "Unpaid": unpaid,
		"Table": table, "Bill": bill, "Guests": guests, "Lines": lines,
		"Unassigned": len(bill.Unassigned()), "UnassignedCents": unassigned,
		"AnyPaid": bill.AnySharePaid(), "Counts": counts, "More": more, "Max": db.MaxGuests,
		"Toast": flashText(r), "Holds": s.holdViews(lang, bill, now), "Phones": bill.QRGuests(),
	})
}

func splitPath(table int) string { return orderPath(table) + "/split" }

// splitAction: how many guests, who had what, split evenly, one line each,
// a guest paid, or undo a payment.
func (s *Server) splitAction(w http.ResponseWriter, r *http.Request) {
	table := tableFromPath(r)
	if table == 0 {
		s.notFound(w, r)
		return
	}
	if !s.parseStaffForm(w, r) {
		return
	}
	now := s.Now()
	billID := atoiDefault(r.FormValue("bill"), 0)
	back := splitPath(table)
	var err error
	var msg string
	switch r.PathValue("action") {
	case "guests":
		n := atoiDefault(r.FormValue("guests"), 0)
		before, _ := s.Store.BillByID(billID)
		err, msg = s.Store.SplitBill(billID, n), "splitset"
		if before.IsSplit() && n > len(before.Split) {
			msg = "guestadded"
		}
		if n <= 1 {
			back, msg = orderPath(table), "unsplit"
		}
	case "assign":
		var gs []int
		for _, v := range r.Form["g"] {
			if g, convErr := strconv.Atoi(v); convErr == nil {
				gs = append(gs, g)
			}
		}
		err, msg = s.Store.AssignLine(billID, atoiDefault(r.FormValue("line"), 0), gs), "assigned"
	case "even":
		err, msg = s.Store.SplitEvenly(billID), "spliteven"
	case "separate":
		err, msg = s.Store.SeparateLine(billID, atoiDefault(r.FormValue("line"), 0)), "separated"
	case "pay":
		var gs []int
		for _, v := range r.Form["guest"] {
			if g, convErr := strconv.Atoi(v); convErr == nil {
				gs = append(gs, g)
			}
		}
		var closed bool
		before, _ := s.Store.BillByID(billID)
		closed, err = s.Store.PayShares(billID, gs, r.FormValue("method"), now)
		if err == nil && len(gs) > 0 {
			sum := 0
			var who []int
			for _, g := range gs {
				if g >= 1 && g <= len(before.Split) && before.Split[g-1].PaidAt == nil {
					sum += before.ShareLeft(g) // what's paid now (after a gift card: the rest)
					who = append(who, g)
				}
			}
			s.logActivity(r, table, paidWhat(r.FormValue("method")), capitalize(guestList(langEN, who))+" · "+euro(sum))
		}
		msg = "guestpaid"
		if len(gs) > 1 {
			msg = "paidtogether"
		}
		if len(gs) == 0 {
			err = db.ErrBadGuest
		}
		if err == nil && closed {
			back, msg = "/admin/orders", "allpaid"
		}
	case "undo":
		g := atoiDefault(r.FormValue("guest"), 0)
		err, msg = s.Store.UndoShare(billID, g), "guestundone"
		if err == nil {
			s.logActivity(r, table, "took back a payment", stf(langEN, "Guest %d", g))
		}
	case "part": // a gift card, lunch coupon or some of one guest's part
		g := atoiDefault(r.FormValue("guest"), 0)
		var p db.Payment
		if p, err = readPayment(r); err != nil {
			break
		}
		var out db.Bill
		var closed bool
		out, closed, err = s.Store.AddSharePayment(billID, g, p, now)
		if err != nil {
			break
		}
		for _, sh := range out.Split {
			if sh.Guest != g || len(sh.Payments) == 0 {
				continue
			}
			last := sh.Payments[len(sh.Payments)-1]
			detail := stf(langEN, "Guest %d", g) + " · " + euro(last.Cents)
			if last.Code != "" {
				detail += " · " + last.Code
			}
			s.logActivity(r, table, paidWhat(last.Method), detail)
			msg = "guestpart"
			if sh.PaidAt != nil {
				msg = "guestpaid"
			}
		}
		if closed {
			back, msg = "/admin/orders", "allpaid"
		}
	case "undopart":
		g := atoiDefault(r.FormValue("guest"), 0)
		var p db.Payment
		if p, err = s.Store.UndoSharePart(billID, g); err == nil {
			msg = "partundone"
			s.logActivity(r, table, "took back a payment", fmt.Sprintf("%s · %s · %s", stf(langEN, "Guest %d", g), methodName(langEN, p.Method), euro(p.Cents)))
		}
	default:
		s.notFound(w, r)
		return
	}
	if err != nil {
		msg = payError(err)
		if errors.Is(err, db.ErrPartlyPaid) {
			msg = "guestpartly"
		}
		back = splitPath(table)
	} else {
		s.live.publish()
	}
	if r.Header.Get("X-Live") == "1" {
		if err != nil {
			http.Error(w, st(langOf(r), flashes[msg]), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	redirectMsg(w, r, back, nil, msg)
}
