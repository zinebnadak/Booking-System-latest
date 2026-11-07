package handlers

import (
	"net/http"
	"sort"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// The manager's Hours page: who clocked in and out at the till ("Klockkort"),
// a week at a time, with the total per person. Forgotten clock-outs are
// fixed here.

type shiftView struct {
	db.Shift
	Day      string // "Mon 2 Nov"
	InClock  string
	OutClock string // "" while still on shift
	Hours    string // "7 h 05 min"
	Open     bool
	Long     bool // over 12 hours: probably a forgotten clock-out
}

type personHours struct {
	Name  string
	Hours string
	Mins  int
}

func (s *Server) adminHours(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	monday := schedule.Today(now)
	if d, err := schedule.ParseDate(r.URL.Query().Get("week")); err == nil {
		monday = d
	}
	for monday.Weekday() != time.Monday {
		monday = monday.AddDate(0, 0, -1)
	}
	end := monday.AddDate(0, 0, 7)
	var list []shiftView
	mins := map[string]int{}
	for _, sh := range s.Store.ShiftsBetween(monday, end) {
		v := shiftView{Shift: sh, Day: capitalize(shortDateL(lang, schedule.FormatDate(sh.In))), InClock: clockOf(sh.In), Open: sh.Out == nil}
		if sh.Out != nil {
			v.OutClock = clockOf(*sh.Out)
		}
		d := sh.Length(now)
		v.Hours, v.Long = hoursText(d), d > 12*time.Hour
		mins[sh.Name] += int(d.Round(time.Minute) / time.Minute)
		list = append(list, v)
	}
	var people []personHours
	for name, m := range mins {
		people = append(people, personHours{Name: name, Mins: m, Hours: hoursText(time.Duration(m) * time.Minute)})
	}
	sort.Slice(people, func(i, j int) bool { return people[i].Name < people[j].Name })
	s.render(w, r, "admin_hours", http.StatusOK, M{
		"Admin": true, "Nav": "staff", "Title": st(lang, "Hours"), "Toast": flashText(r),
		"Shifts": list, "People": people,
		"Week": capitalize(shortDateL(lang, schedule.FormatDate(monday))) + " – " + shortDateL(lang, schedule.FormatDate(end.AddDate(0, 0, -1))),
		"Prev": schedule.FormatDate(monday.AddDate(0, 0, -7)), "Next": schedule.FormatDate(end),
	})
}

// adminHoursFix sets a shift's times: in and out as "16:00" and "23:30" on
// the shift's day (an out time before the in time is the next morning).
func (s *Server) adminHoursFix(w http.ResponseWriter, r *http.Request) {
	id := atoiDefault(r.PathValue("id"), 0)
	day, err := schedule.ParseDate(r.FormValue("day"))
	in, err1 := schedule.At(day, r.FormValue("in"))
	out, err2 := schedule.At(day, r.FormValue("out"))
	if err != nil || err1 != nil || err2 != nil {
		redirectMsg(w, r, "/admin/hours", nil, "shiftbad")
		return
	}
	if !out.After(in) {
		out = out.AddDate(0, 0, 1)
	}
	if err := s.Store.FixShift(id, in, out); err != nil {
		redirectMsg(w, r, "/admin/hours", nil, "shiftbad")
		return
	}
	s.logActivity(r, 0, "changed a shift", r.FormValue("name")+" · "+r.FormValue("in")+"–"+r.FormValue("out"))
	week := schedule.FormatDate(day)
	http.Redirect(w, r, "/admin/hours?week="+week+"&msg=shiftfixed", http.StatusSeeOther)
}
