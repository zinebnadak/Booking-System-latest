package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// The Stock page: any staff member (waiters, the kitchen) marks what has
// run out, or types how many are left. Waiters see it the moment they take
// an order, so they can tell a guest "sorry, that's finished" before the
// guest has chosen it. Guests can't order it on their phones.

func (s *Server) adminStock(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	today := schedule.FormatDate(schedule.Today(now))
	var items []db.MenuItem
	for _, m := range s.Store.Menu(false) {
		if m.Active(today) {
			items = append(items, m)
		}
	}
	out, low := s.Store.StockLists(now)
	data := M{
		"Admin": true, "Nav": "stock", "Title": st(lang, "Stock"),
		"Menu": groupMenu(items), "Out": out, "Low": low, "Max": db.MaxStock,
		"Today": today, "Toast": flashText(r), "Ingredients": s.ingredientRows(),
	}
	if r.URL.Query().Get("part") == "list" {
		s.renderBlock(w, r, "admin_stock", "stock-list", http.StatusOK, data)
		return
	}
	s.render(w, r, "admin_stock", http.StatusOK, data)
}

// adminStockSet: "Out of stock", "Back in stock", or how many are left
// (an empty box: stop counting).
func (s *Server) adminStockSet(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	lang := langOf(r)
	id := atoiDefault(r.PathValue("id"), 0)
	left, what := db.NotCounted, ""
	err := error(nil)
	switch r.FormValue("do") {
	case "out":
		left, what = 0, "marked out of stock"
	case "back":
		what = "put back in stock"
	case "count":
		what = "counted the stock"
		if v := strings.TrimSpace(r.FormValue("left")); v != "" {
			if left, err = strconv.Atoi(v); err != nil || left < 0 {
				err = db.ErrBadStock // (a typed -1 isn't "stop counting")
			}
		}
	default:
		err = db.ErrBadStock
	}
	var m db.MenuItem
	if err == nil {
		m, err = s.Store.SetStock(id, left)
	}
	if err != nil {
		msg := "stockbad"
		if errors.Is(err, db.ErrNotFound) {
			msg = "billstale"
		}
		if r.Header.Get("X-Live") != "" {
			http.Error(w, st(lang, flashes[msg]), http.StatusBadRequest)
			return
		}
		redirectMsg(w, r, "/admin/stock", nil, msg)
		return
	}
	detail := m.Name
	switch {
	case what == "counted the stock" && m.Counted:
		detail = m.Name + ": " + strconv.Itoa(m.Left)
	case what == "counted the stock":
		detail = m.Name + ": –"
	}
	s.logActivity(r, 0, what, detail)
	s.live.publish() // waiters' screens and guests' menus change at once
	if r.Header.Get("X-Live") != "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/admin/stock#s-"+strconv.Itoa(id), http.StatusSeeOther)
}

// soldOutQuery: for "Only 2 left of Pinot Grigio" on the next page.
func soldOutQuery(err error) (url.Values, bool) {
	var so *db.SoldOutError
	if !errors.As(err, &so) {
		return nil, false
	}
	q := url.Values{"item": {so.Name}, "left": {strconv.Itoa(so.Left)}}
	if so.Short != "" {
		q.Set("short", so.Short)
	}
	return q, true
}

// soldOutText is the message for msg=soldout on a staff page.
func soldOutText(r *http.Request) string {
	lang := langOf(r)
	q := r.URL.Query()
	item := clip(oneLine(q.Get("item")), maxNameLen)
	short := clip(oneLine(q.Get("short")), maxNameLen)
	switch left := atoiDefault(q.Get("left"), 0); {
	case item == "":
		return st(lang, "Something you picked has run out. Nothing was added.")
	case short != "" && left > 0:
		return stf(lang, "Not enough %s: only %d more %s can be made. Nothing was added.", short, left, item)
	case short != "":
		return stf(lang, "%s is out of stock: no %s left. Nothing was added.", item, short)
	case left > 0:
		return stf(lang, "Only %d left of %s. Nothing was added.", left, item)
	default:
		return stf(lang, "%s is out of stock. Nothing was added.", item)
	}
}

// stockStrip: what's out and what's running low, for the top of the
// waiters' screens.
type stockStrip struct {
	Out []db.MenuItem
	Low []db.MenuItem
}

func (s *Server) stockStrip() *stockStrip {
	out, low := s.Store.StockLists(s.Now())
	if len(out) == 0 && len(low) == 0 {
		return nil
	}
	return &stockStrip{Out: out, Low: low}
}

// adminIngredientAdd puts a new ingredient in stock (Stock page).
func (s *Server) adminIngredientAdd(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	left, err1 := strconv.Atoi(strings.TrimSpace(r.FormValue("left")))
	lowAt := 0
	var err2 error
	if v := strings.TrimSpace(r.FormValue("lowat")); v != "" {
		lowAt, err2 = strconv.Atoi(v)
	}
	if err1 != nil || err2 != nil {
		s.stockDone(w, r, "ingredientbad")
		return
	}
	ing, err := s.Store.AddIngredient(r.FormValue("name"), r.FormValue("unit"), left, lowAt)
	switch {
	case errors.Is(err, db.ErrIngredientName):
		s.stockDone(w, r, "ingredienttaken")
		return
	case err != nil:
		s.stockDone(w, r, "ingredientbad")
		return
	}
	s.logActivity(r, 0, "added an ingredient", fmt.Sprintf("%s: %d %s", ing.Name, ing.Left, ing.Unit))
	s.live.publish()
	s.stockDone(w, r, "")
}

// adminIngredientAction: how much is left and the warning level, or remove.
func (s *Server) adminIngredientAction(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	id := atoiDefault(r.PathValue("id"), 0)
	switch r.FormValue("do") {
	case "remove":
		ing, err := s.Store.RemoveIngredient(id)
		if err != nil {
			s.stockDone(w, r, "billstale")
			return
		}
		s.logActivity(r, 0, "removed an ingredient", ing.Name)
	default:
		left, err1 := strconv.Atoi(strings.TrimSpace(r.FormValue("left")))
		lowAt := 0
		var err2 error
		if v := strings.TrimSpace(r.FormValue("lowat")); v != "" {
			lowAt, err2 = strconv.Atoi(v)
		}
		if err1 != nil || err2 != nil {
			s.stockDone(w, r, "ingredientbad")
			return
		}
		ing, err := s.Store.SetIngredient(id, left, lowAt)
		if errors.Is(err, db.ErrNotFound) {
			s.stockDone(w, r, "billstale")
			return
		}
		if err != nil {
			s.stockDone(w, r, "ingredientbad")
			return
		}
		s.logActivity(r, 0, "counted an ingredient", fmt.Sprintf("%s: %d %s", ing.Name, ing.Left, ing.Unit))
	}
	s.live.publish()
	s.stockDone(w, r, "")
}

// stockDone answers a Stock page form: in the background (X-Live) or with
// a redirect back to the page. msg is an error message key ("" = done).
func (s *Server) stockDone(w http.ResponseWriter, r *http.Request, msg string) {
	if r.Header.Get("X-Live") != "" {
		if msg != "" {
			http.Error(w, st(langOf(r), flashes[msg]), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if msg != "" {
		redirectMsg(w, r, "/admin/stock", nil, msg)
		return
	}
	http.Redirect(w, r, "/admin/stock#ingredients", http.StatusSeeOther)
}

// ingredientRow is an ingredient on the Stock page, with the dishes using it.
type ingredientRow struct {
	db.Ingredient
	Dishes []string
}

func (s *Server) ingredientRows() []ingredientRow {
	using := s.Store.DishesUsing()
	var out []ingredientRow
	for _, i := range s.Store.Ingredients() {
		out = append(out, ingredientRow{Ingredient: i, Dishes: using[i.ID]})
	}
	return out
}

// ---------- "Paid on phones" (Orders and Tables screens) ----------

// onlinePaidView is a phone payment the waiters haven't seen yet.
type onlinePaidView struct {
	Ref        string
	Table      int
	Who        string
	Cents, Tip int
	All        bool // paid for the whole table
	Others     bool // paid for some other guests too
	Free       bool // nothing left to pay: the table is free
	At         string
}

func (s *Server) onlinePaid(lang string, now time.Time) []onlinePaidView {
	var out []onlinePaidView
	for _, a := range s.Store.PaidOnlineToSee(now) {
		b, err := s.Store.BillByID(a.BillID)
		if err != nil {
			continue
		}
		v := onlinePaidView{Ref: a.Ref, Table: b.Table, Cents: a.Cents, Tip: a.TipCents,
			All: a.All, Others: !a.All && len(a.Guests) > 1, Free: !b.Open(), At: clockOf(*a.PaidAt)}
		v.Who = stf(lang, "Guest %d", a.Payer)
		if a.Payer >= 1 && a.Payer <= len(b.Split) {
			v.Who = guestLabel(lang, b.Split[a.Payer-1])
		}
		out = append(out, v)
	}
	return out
}

// adminPaymentSeen: a waiter tapped OK on "Paid on phones".
func (s *Server) adminPaymentSeen(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	err := s.Store.SeePayment(r.PathValue("ref"))
	if err == nil {
		s.live.publish()
	}
	if r.Header.Get("X-Live") != "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/admin/orders", http.StatusSeeOther)
}
