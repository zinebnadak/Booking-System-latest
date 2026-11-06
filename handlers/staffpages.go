package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

// The manager's Staff page (waiters' logins) and Activity page (who did
// what with money, day by day).

func (s *Server) adminStaff(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	list := s.Store.StaffList()
	s.render(w, r, "admin_staff", http.StatusOK, M{
		"Admin": true, "Nav": "staff", "Title": st(lang, "Staff"),
		"List": list, "Min": db.MinStaffPassword, "Toast": flashText(r),
		"IsTill": s.isTill(r), "Served": s.servedBy(list, s.Now()),
	})
}

// servedRow is one waiter on "Who served the most tables".
type servedRow struct {
	Name              string
	Nobody            bool // tables nobody took
	Today, TodayCents int
	Month, MonthCents int
	MonthPct          float64 // bar length, against the busiest waiter
}

// servedBy: tables each waiter had today and this month (paid bills), the
// busiest first.
func (s *Server) servedBy(staff []db.StaffMember, now time.Time) []servedRow {
	today := schedule.Today(now)
	day := schedule.FormatDate(today)
	first := schedule.FormatDate(time.Date(today.Year(), today.Month(), 1, 12, 0, 0, 0, schedule.Location))
	names := map[int]string{}
	for _, m := range staff {
		names[m.ID] = m.Name
	}
	rows := map[int]*servedRow{}
	var order []int
	row := func(t db.WaiterTotal) *servedRow {
		r := rows[t.ID]
		if r == nil {
			r = &servedRow{Name: t.Name, Nobody: t.ID == 0}
			if n := names[t.ID]; n != "" {
				r.Name = n
			}
			rows[t.ID] = r
			order = append(order, t.ID)
		}
		return r
	}
	for _, t := range s.Store.WaiterTotals(first, day) { // busiest first
		r := row(t)
		r.Month, r.MonthCents = t.Tables, t.Cents
	}
	for _, t := range s.Store.WaiterTotals(day, day) {
		r := row(t)
		r.Today, r.TodayCents = t.Tables, t.Cents
	}
	out := make([]servedRow, 0, len(order))
	most := 0
	for _, id := range order {
		out = append(out, *rows[id])
		if !rows[id].Nobody && rows[id].Month > most {
			most = rows[id].Month
		}
	}
	for i := range out {
		if most > 0 && !out[i].Nobody {
			out[i].MonthPct = float64(out[i].Month) / float64(most) * 100
		}
	}
	return out
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
