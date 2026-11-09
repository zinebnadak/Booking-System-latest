package handlers

import (
	"errors"
	"net/http"
	"strings"

	"emeal-booking/db"
)

// staffShift: a waiter or the chef starts or ends their shift from their
// own login (the top bar), like the clock card at the till. The manager sees
// the hours under Staff → Hours.
func (s *Server) staffShift(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	l := loginOf(r)
	back := safeBack(r.FormValue("back"))
	var me *db.StaffMember
	for _, m := range s.Store.StaffList() {
		if m.ID == l.StaffID {
			m := m
			me = &m
		}
	}
	if me == nil {
		redirectMsg(w, r, back, nil, "invalid") // the manager's login has no clock card
		return
	}
	now := s.Now()
	switch r.FormValue("do") {
	case "start":
		if _, err := s.Store.ClockIn(*me, now); err != nil && !errors.Is(err, db.ErrClockedIn) {
			redirectMsg(w, r, back, nil, "invalid")
			return
		}
		s.logActivity(r, 0, "started the shift", "")
		redirectMsg(w, r, back, nil, "shiftstarted")
	case "end":
		sh, err := s.Store.ClockOut(*me, now)
		if err != nil {
			redirectMsg(w, r, back, nil, "shiftnone")
			return
		}
		s.logActivity(r, 0, "ended the shift", hoursText(sh.Length(now)))
		redirectMsg(w, r, back, nil, "shiftended")
	default:
		redirectMsg(w, r, back, nil, "invalid")
	}
}

// safeBack keeps "back" on this site's staff pages.
func safeBack(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if len(p) > 1 && p[0] == '/' && p[1] != '/' && p[1] != '\\' && len(p) < 200 {
		return p
	}
	return "/admin/orders"
}
