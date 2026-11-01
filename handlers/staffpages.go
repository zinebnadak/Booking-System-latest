package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

// The manager's Staff page (waiters' logins) and Activity page (who did
// what with money, day by day).

func (s *Server) adminStaff(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	s.render(w, r, "admin_staff", http.StatusOK, M{
		"Admin": true, "Nav": "staff", "Title": st(lang, "Staff"),
		"List": s.Store.StaffList(), "Min": db.MinStaffPassword, "Toast": flashText(r),
		"IsTill": s.isTill(r),
	})
}

func (s *Server) adminStaffAdd(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	m, err := s.Store.AddStaff(r.FormValue("name"), r.FormValue("username"), r.FormValue("password"), s.Now())
	switch {
	case errors.Is(err, db.ErrUsernameTaken):
		redirectMsg(w, r, "/admin/staff", nil, "usertaken")
	case err != nil:
		redirectMsg(w, r, "/admin/staff", nil, "staffbad")
	default:
		s.logActivity(r, 0, "added a waiter", m.Name+" ("+m.Username+")")
		redirectMsg(w, r, "/admin/staff", nil, "staffadded")
	}
}

func (s *Server) adminStaffAction(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	id := atoiDefault(r.PathValue("id"), 0)
	name := ""
	for _, m := range s.Store.StaffList() {
		if m.ID == id {
			name = m.Name
		}
	}
	switch r.PathValue("action") {
	case "password":
		if err := s.Store.NewStaffPassword(id, r.FormValue("password")); err != nil {
			redirectMsg(w, r, "/admin/staff", nil, "staffbad")
			return
		}
		s.logActivity(r, 0, "gave a waiter a new password", name)
		redirectMsg(w, r, "/admin/staff", nil, "staffpassword")
	case "pin":
		if err := s.Store.SetStaffPIN(id, strings.TrimSpace(r.FormValue("pin"))); err != nil {
			redirectMsg(w, r, "/admin/staff", nil, "pinbad")
			return
		}
		s.logActivity(r, 0, "gave a waiter a PIN for the till", name)
		redirectMsg(w, r, "/admin/staff", nil, "pinset")
	case "remove":
		if err := s.Store.RemoveStaff(id); err != nil {
			s.notFound(w, r)
			return
		}
		s.logActivity(r, 0, "removed a waiter", name)
		redirectMsg(w, r, "/admin/staff", nil, "staffremoved")
	default:
		s.notFound(w, r)
	}
}

// activityWords are the things the activity list can say (each has Swedish).
var activityWords = []string{
	"paid at the terminal", "paid by card", "paid in cash", "took back a payment", "voided the bill", "moved the bill",
	"changed a price", "took off the bill", "rejected a phone order", "cancelled a booking",
	"refunded a deposit", "added a waiter", "gave a waiter a new password", "removed a waiter",
	"closed a room for online bookings", "opened a room for online bookings",
	"gave a discount", "gave a discount on the whole bill",
	"paid with a lunch card", "paid with a gift card", "paid with lunch coupons", "paid in several ways",
	"sold a gift card", "sold a lunch booklet", "voided a gift card",
	"gave a waiter a PIN for the till", "changed a shift",
	"marked out of stock", "put back in stock", "counted the stock",
	"added a special", "added an ingredient", "counted an ingredient", "removed an ingredient",
}

// logActivity records something staff did with money, with who did it.
func (s *Server) logActivity(r *http.Request, table int, what, detail string) {
	s.Store.LogActivity(db.Activity{At: s.Now(), Actor: actor(r), Table: table, What: what, Detail: detail})
}

func (s *Server) adminActivity(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	day, err := schedule.ParseDate(r.URL.Query().Get("date"))
	if err != nil {
		day = schedule.Today(now)
	}
	date := schedule.FormatDate(day)
	type row struct {
		db.Activity
		Time string
	}
	var rows []row
	who := strings.TrimSpace(r.URL.Query().Get("who"))
	actors := map[string]bool{}
	for _, a := range s.Store.ActivityOn(date) {
		actors[a.Actor] = true
		if who != "" && a.Actor != who {
			continue
		}
		rows = append(rows, row{Activity: a, Time: clockOf(a.At)})
	}
	var names []string
	for n := range actors {
		names = append(names, n)
	}
	s.render(w, r, "admin_activity", http.StatusOK, M{
		"Admin": true, "Nav": "activity", "Title": st(lang, "Activity"),
		"Date": date, "Prev": schedule.FormatDate(day.AddDate(0, 0, -1)), "Next": schedule.FormatDate(day.AddDate(0, 0, 1)),
		"Rows": rows, "Who": who, "Names": names, "WhoQ": url.QueryEscape(who),
	})
}

// offlinePlan is the printable "When the internet is down" sheet.
func (s *Server) offlinePlan(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "admin_offline", http.StatusOK, M{
		"Admin": true, "Nav": "orders", "Title": st(langOf(r), "When the internet is down"),
	})
}
