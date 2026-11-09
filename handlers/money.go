package handlers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// ---------- Money today: the cash drawer and Wolt's cut ----------

// cashView is the cash drawer panel on Money today.
type cashView struct {
	Taken   int // cash that came in on the day (bills and gift cards)
	Float   int // what to start the form with
	Count   *db.CashCount
	History []cashRow
}

type cashRow struct {
	db.CashCount
	Day string
}

func (s *Server) cashPanel(lang, date string) cashView {
	v := cashView{Float: s.Store.CashFloat()}
	if d := s.Store.TillByDate(date, date)[date]; d != nil {
		v.Taken = d.Cash
	}
	if c, ok := s.Store.CashCountOn(date); ok {
		v.Count = &c
		v.Float = c.Float
	}
	for _, c := range s.Store.RecentCashCounts(date, 10) {
		v.History = append(v.History, cashRow{CashCount: c, Day: capitalize(shortDateL(lang, c.Date))})
	}
	return v
}

// ownerCash saves the cash drawer count for a day (the manager counts it at
// closing). What the system expected is fixed at that moment.
func (s *Server) ownerCash(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	date := r.FormValue("date")
	back := "/owner?" + url.Values{"date": {date}, "wolt": {woltMode(r)}}.Encode() + "#cash"
	if _, err := schedule.ParseDate(date); err != nil {
		http.Redirect(w, r, "/owner", http.StatusSeeOther)
		return
	}
	float, err1 := parseEuro(orZero(r.FormValue("float")))
	counted, err2 := parseEuro(r.FormValue("counted"))
	if err1 != nil || err2 != nil {
		http.Redirect(w, r, strings.Replace(back, "#cash", "&msg=cashbad#cash", 1), http.StatusSeeOther)
		return
	}
	taken := 0
	if d := s.Store.TillByDate(date, date)[date]; d != nil {
		taken = d.Cash
	}
	c, err := s.Store.SaveCashCount(db.CashCount{Date: date, Float: float, Taken: taken, Counted: counted,
		Note: clip(oneLine(r.FormValue("note")), 200), By: actor(r), At: s.Now()})
	if err != nil {
		http.Redirect(w, r, strings.Replace(back, "#cash", "&msg=cashbad#cash", 1), http.StatusSeeOther)
		return
	}
	s.logActivity(r, 0, "counted the cash drawer", "expected "+euro(c.Expected())+", counted "+euro(c.Counted)+", difference "+signedEuro(c.Diff()))
	http.Redirect(w, r, strings.Replace(back, "#cash", "&msg=cashsaved#cash", 1), http.StatusSeeOther)
}

// ownerWoltPct sets how many percent Wolt keeps of each order.
func (s *Server) ownerWoltPct(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	back := "/owner?" + url.Values{"date": {r.FormValue("date")}, "wolt": {woltMode(r)}}.Encode()
	pct, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(r.FormValue("pct")), "%")))
	if err != nil || s.Store.SetWoltPct(pct) != nil {
		http.Redirect(w, r, back+"&msg=woltpctbad#wolt", http.StatusSeeOther)
		return
	}
	s.logActivity(r, 0, "set Wolt's cut", strconv.Itoa(pct)+" %")
	http.Redirect(w, r, back+"&msg=woltpctsaved#wolt", http.StatusSeeOther)
}

// signedEuro: "+2,00 €", "−5,50 €", "0,00 €".
func signedEuro(c int) string {
	switch {
	case c > 0:
		return "+" + euro(c)
	case c < 0:
		return "−" + euro(-c)
	}
	return euro(0)
}

func orZero(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0"
	}
	return s
}
