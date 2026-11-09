package handlers

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// ---------- The staff schedule: the manager plans, staff see their own ----------

type rotaDay struct {
	Date   string
	Label  string // "Fri 13 Nov"
	Today  bool
	Shifts []rotaView
}

type rotaView struct {
	db.RotaShift
	Hours string
	Day   string // "Friday 13 November"
}

type rotaPerson struct {
	Name  string
	Hours string
	Count int
}

// weekStart is the Monday of the week in ?week= (or of this week).
func weekStart(r *http.Request, now time.Time) time.Time {
	monday := schedule.Today(now)
	if d, err := schedule.ParseDate(r.URL.Query().Get("week")); err == nil {
		monday = d
	}
	for monday.Weekday() != time.Monday {
		monday = monday.AddDate(0, 0, -1)
	}
	return monday
}

func (s *Server) adminSchedule(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	monday := weekStart(r, now)
	sunday := monday.AddDate(0, 0, 6)
	today := schedule.FormatDate(schedule.Today(now))
	days := make([]rotaDay, 7)
	for i := range days {
		d := monday.AddDate(0, 0, i)
		days[i] = rotaDay{Date: schedule.FormatDate(d), Label: capitalize(shortDateL(lang, schedule.FormatDate(d)))}
		days[i].Today = days[i].Date == today
	}
	mins, count := map[string]int{}, map[string]int{}
	for _, sh := range s.Store.RotaBetween(schedule.FormatDate(monday), schedule.FormatDate(sunday)) {
		for i := range days {
			if days[i].Date == sh.Date {
				days[i].Shifts = append(days[i].Shifts, rotaView{RotaShift: sh, Hours: hoursText(time.Duration(sh.Minutes()) * time.Minute)})
			}
		}
		mins[sh.Name] += sh.Minutes()
		count[sh.Name]++
	}
	var people []rotaPerson
	for _, m := range s.Store.StaffList() {
		if m.Active {
			people = append(people, rotaPerson{Name: m.Name, Count: count[m.Name], Hours: hoursText(time.Duration(mins[m.Name]) * time.Minute)})
		}
	}
	week := schedule.FormatDate(monday)
	s.render(w, r, "admin_schedule", http.StatusOK, M{
		"Admin": true, "Nav": "staff", "Title": st(lang, "Schedule"), "Toast": flashText(r),
		"Days": days, "People": people, "Staff": s.Store.StaffList(), "WeekKey": week,
		"Week": capitalize(shortDateL(lang, week)) + " – " + shortDateL(lang, schedule.FormatDate(sunday)),
		"Prev": schedule.FormatDate(monday.AddDate(0, 0, -7)), "Next": schedule.FormatDate(monday.AddDate(0, 0, 7)),
		"PrevLabel": shortDateL(lang, schedule.FormatDate(monday.AddDate(0, 0, -7))),
	})
}

func (s *Server) adminScheduleAdd(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	week := r.FormValue("week")
	back := url.Values{"week": {week}}
	sh, err := s.Store.AddRota(db.RotaShift{StaffID: atoiDefault(r.FormValue("staff"), 0), Date: r.FormValue("date"),
		From: r.FormValue("from"), To: r.FormValue("to"), Note: clip(oneLine(r.FormValue("note")), 120)})
	if err != nil {
		redirectMsg(w, r, "/admin/schedule", back, "rotabad")
		return
	}
	s.logActivity(r, 0, "planned a shift", sh.Name+" "+sh.Date+" "+sh.From+"–"+sh.To)
	redirectMsg(w, r, "/admin/schedule", back, "rotaadded")
}

func (s *Server) adminScheduleRemove(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	s.Store.RemoveRota(atoiDefault(r.PathValue("id"), 0))
	redirectMsg(w, r, "/admin/schedule", url.Values{"week": {r.FormValue("week")}}, "rotaremoved")
}

// adminScheduleCopy plans this week like last week.
func (s *Server) adminScheduleCopy(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	to, err := schedule.ParseDate(r.FormValue("week"))
	if err != nil {
		http.Redirect(w, r, "/admin/schedule", http.StatusSeeOther)
		return
	}
	n, _ := s.Store.CopyRotaWeek(to.AddDate(0, 0, -7), to)
	if n > 0 {
		s.logActivity(r, 0, "copied last week's schedule", strconv.Itoa(n)+" shifts")
	}
	redirectMsg(w, r, "/admin/schedule", url.Values{"week": {r.FormValue("week")}, "n": {strconv.Itoa(n)}}, "rotacopied")
}

// mySchedule is a waiter's or the chef's own shifts: today and the next
// two weeks.
func (s *Server) mySchedule(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	me := loginOf(r)
	today := schedule.Today(now)
	var list []rotaView
	for _, sh := range s.Store.RotaBetween(schedule.FormatDate(today), schedule.FormatDate(today.AddDate(0, 0, 13))) {
		if sh.StaffID == me.StaffID && me.StaffID != 0 {
			list = append(list, rotaView{RotaShift: sh, Hours: hoursText(time.Duration(sh.Minutes()) * time.Minute), Day: capitalize(longDate(lang, sh.Date))})
		}
	}
	s.render(w, r, "my_schedule", http.StatusOK, M{
		"Admin": true, "Nav": "myschedule", "Title": st(lang, "My schedule"),
		"Shifts": list, "Today": schedule.FormatDate(today),
	})
}
