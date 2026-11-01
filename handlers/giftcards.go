package handlers

import (
	"net/http"
	"net/url"
	"strings"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

// Gift cards (presentkort) and lunch coupon booklets (lunchkuponghäfte):
// selling them, printing the card, and looking up what's left on one.
// Paying with them is on the table's bill (paying.go).

type voucherView struct {
	db.Voucher
	Sold string // "30 Oct 2026, 18:05"
	Used int    // cents (gift card) or coupons (booklet) used so far
}

func (s *Server) voucherView(lang string, v db.Voucher) voucherView {
	out := voucherView{Voucher: v, Sold: shortDateL(lang, schedule.FormatDate(v.SoldAt)) + " " + clockOf(v.SoldAt)}
	for _, u := range v.Uses {
		if v.Kind == db.VoucherLunch {
			out.Used += u.Coupons
		} else {
			out.Used += u.Cents
		}
	}
	return out
}

func (s *Server) adminGiftCards(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	data := M{"Admin": true, "Nav": "orders", "Title": st(lang, "Gift cards and lunch booklets"), "Toast": flashText(r)}
	if code := strings.TrimSpace(r.URL.Query().Get("code")); code != "" {
		data["Code"] = code
		if v, ok := s.Store.VoucherByCode(code); ok {
			data["Found"] = s.voucherView(lang, v)
		} else {
			data["NotFound"] = true
		}
	}
	var list []voucherView
	for _, v := range s.Store.Vouchers(30) {
		list = append(list, s.voucherView(lang, v))
	}
	data["List"] = list
	data["Manager"] = rank(roleOf(r)) >= rank(roleManager)
	s.render(w, r, "admin_giftcards", http.StatusOK, data)
}

// adminGiftCardSell sells a gift card (kind=gift) or a lunch booklet (kind=lunch).
func (s *Server) adminGiftCardSell(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	now := s.Now()
	method := r.FormValue("method")
	note := clip(oneLine(r.FormValue("note")), 80)
	var v db.Voucher
	var err error
	switch r.FormValue("kind") {
	case db.VoucherLunch:
		coupon, err1 := parseEuro(r.FormValue("coupon"))
		price, err2 := parseEuro(r.FormValue("price"))
		if err1 != nil || err2 != nil {
			redirectMsg(w, r, "/admin/giftcards", nil, "invalid")
			return
		}
		v, err = s.Store.SellLunchBooklet(atoiDefault(r.FormValue("coupons"), 0), coupon, price, method, actor(r), note, now)
	default:
		cents, perr := parseEuro(r.FormValue("amount"))
		if perr != nil {
			redirectMsg(w, r, "/admin/giftcards", nil, "invalid")
			return
		}
		v, err = s.Store.SellGiftCard(cents, method, actor(r), note, now)
	}
	if err != nil {
		redirectMsg(w, r, "/admin/giftcards", nil, "invalid")
		return
	}
	what := "sold a gift card"
	if v.Kind == db.VoucherLunch {
		what = "sold a lunch booklet"
	}
	s.logActivity(r, 0, what, v.Code+" · "+euro(v.SoldCents)+" · "+methodName(langEN, v.SoldWith))
	http.Redirect(w, r, "/admin/giftcards/"+url.PathEscape(v.Code)+"/print", http.StatusSeeOther)
}

// adminGiftCardPrint is the card to print and hand over.
func (s *Server) adminGiftCardPrint(w http.ResponseWriter, r *http.Request) {
	v, ok := s.Store.VoucherByCode(r.PathValue("code"))
	if !ok {
		s.notFound(w, r)
		return
	}
	lang := langOf(r)
	s.render(w, r, "giftcard_print", http.StatusOK, M{
		"Admin": true, "Bare": true, "Title": st(lang, "Gift card"), "Card": s.voucherView(lang, v),
	})
}

// adminGiftCardVoid cancels a card sold by mistake (manager only).
func (s *Server) adminGiftCardVoid(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if err := s.Store.VoidVoucher(code); err != nil {
		redirectMsg(w, r, "/admin/giftcards", url.Values{"code": {code}}, "cardused")
		return
	}
	s.logActivity(r, 0, "voided a gift card", code)
	redirectMsg(w, r, "/admin/giftcards", url.Values{"code": {code}}, "cardvoided")
}
