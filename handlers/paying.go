package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"ozmo-booking/db"
)

// Ways to pay on the staff screens: their names, paying a bill in parts
// ("Kvar att betala"), and the activity list lines.

// methodName is how a way of paying is shown to staff ("At the terminal").
func methodName(lang, m string) string {
	switch m {
	case db.PaidCard:
		return st(lang, "At the terminal")
	case db.PaidCash:
		return st(lang, "In cash")
	case db.PaidOnline:
		return st(lang, "Online at the table")
	case db.PaidEpassi:
		return "ePassi"
	case db.PaidSmartum:
		return "Smartum"
	case db.PaidEdenred:
		return "Edenred"
	case db.PaidGiftCard:
		return st(lang, "Gift card")
	case db.PaidLunchCoupon:
		return st(lang, "Lunch coupons")
	case db.PaidMixed:
		return st(lang, "Several ways")
	case db.PaidNothing:
		return st(lang, "Nothing to pay")
	}
	return m
}

// paidWhat is the activity list's words for a way of paying.
func paidWhat(m string) string {
	switch m {
	case db.PaidCash:
		return "paid in cash"
	case db.PaidEpassi, db.PaidSmartum, db.PaidEdenred:
		return "paid with a lunch card"
	case db.PaidGiftCard:
		return "paid with a gift card"
	case db.PaidLunchCoupon:
		return "paid with lunch coupons"
	case db.PaidMixed:
		return "paid in several ways"
	}
	return "paid at the terminal"
}

// takingRow is one way of paying in "Paid today" and the day report.
type takingRow struct {
	Method, Name string
	Cents        int
	Prepaid      bool // gift card or lunch coupon: the money came in when it was sold
}

func takingRows(lang string, t map[string]int) []takingRow {
	var out []takingRow
	for _, m := range db.Methods(t) {
		out = append(out, takingRow{Method: m, Name: methodName(lang, m), Cents: t[m], Prepaid: db.PrepaidMethod(m)})
	}
	return out
}

// payPart: part of a bill paid on its own (a gift card, a lunch card, half
// in cash…). The rest stays on the bill until it's paid too.
func (s *Server) payPart(w http.ResponseWriter, r *http.Request, table, billID int) {
	now := s.Now()
	p, err := readPayment(r)
	if err != nil {
		redirectMsg(w, r, orderPath(table), nil, "invalid")
		return
	}
	out, closed, err := s.Store.AddPayment(billID, p, now)
	if err != nil {
		redirectMsg(w, r, orderPath(table), nil, payError(err))
		return
	}
	last := out.Payments[len(out.Payments)-1]
	detail := euro(last.Cents)
	if last.Code != "" {
		detail += " · " + last.Code
	}
	s.logActivity(r, table, paidWhat(last.Method), detail+" ("+st(langEN, "part of the bill")+")")
	s.live.publish()
	if closed {
		redirectMsg(w, r, "/admin/orders", nil, "billpaid")
		return
	}
	redirectMsg(w, r, orderPath(table), nil, "partpaid")
}

// readPayment reads the "pay part" form: how, how much (empty: what's on
// the gift card, up to what's left), the card's code, how many coupons.
func readPayment(r *http.Request) (db.Payment, error) {
	p := db.Payment{Method: r.FormValue("method"), Code: strings.TrimSpace(r.FormValue("code")), Coupons: atoiDefault(r.FormValue("coupons"), 1)}
	if v := strings.TrimSpace(r.FormValue("amount")); v != "" {
		c, err := parseEuro(v)
		if err != nil {
			return p, db.ErrBadAmount
		}
		p.Cents = c
	}
	return p, nil
}

func (s *Server) undoPart(w http.ResponseWriter, r *http.Request, table, billID int) {
	p, err := s.Store.UndoPayment(billID, s.Now())
	if err != nil {
		redirectMsg(w, r, orderPath(table), nil, payError(err))
		return
	}
	s.logActivity(r, table, "took back a payment", fmt.Sprintf("%s · %s", methodName(langEN, p.Method), euro(p.Cents)))
	s.live.publish()
	redirectMsg(w, r, orderPath(table), nil, "partundone")
}

func payError(err error) string {
	switch {
	case errors.Is(err, db.ErrNoVoucher):
		return "nocard"
	case errors.Is(err, db.ErrVoucherUsed):
		return "cardempty"
	case errors.Is(err, db.ErrBadVoucher):
		return "toomanycoupons"
	case errors.Is(err, db.ErrExtraCoupons):
		return "extracoupons"
	case errors.Is(err, db.ErrBadAmount), errors.Is(err, db.ErrBadMethod):
		return "badamount"
	case errors.Is(err, db.ErrNothingLeft):
		return "billstale"
	case errors.Is(err, db.ErrPartlyPaid):
		return "partlypaid"
	}
	return orderError(err)
}
