package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"emeal-booking/schedule"
)

// ---------- Tables and seats (the manager changes the table plan) ----------

// planRoom is one room on the Tables and seats page.
type planRoom struct {
	Name   string
	Tables []schedule.Table
}

func planRooms() []planRoom {
	var out []planRoom
	for _, r := range schedule.RoomNames() {
		pr := planRoom{Name: r}
		for _, t := range schedule.Tables() {
			if t.Room == r {
				pr.Tables = append(pr.Tables, t)
			}
		}
		out = append(out, pr)
	}
	return out
}

func (s *Server) adminTablePlan(w http.ResponseWriter, r *http.Request) {
	s.renderTablePlan(w, r, http.StatusOK, "")
}

func (s *Server) renderTablePlan(w http.ResponseWriter, r *http.Request, status int, problem string) {
	lang := langOf(r)
	seats := 0
	for _, t := range schedule.Tables() {
		seats += t.Seats
	}
	s.render(w, r, "admin_tableplan", status, M{
		"Admin": true, "Nav": "staff", "Title": st(lang, "Tables and seats"),
		"Rooms": planRooms(), "Count": schedule.TableCount(), "Seats": seats,
		"MaxSeats": schedule.MaxTableSeats, "MaxNo": schedule.MaxTableNo,
		"Problem": problem, "Toast": flashText(r),
	})
}

// adminTablePlanSave reads the whole plan from the form: each room's (new)
// name, each table's room and seats (or remove), and one new table.
func (s *Server) adminTablePlanSave(w http.ResponseWriter, r *http.Request) {
	if !s.parseStaffForm(w, r) {
		return
	}
	lang := langOf(r)
	// Rooms: renamed in place, emptied ones dropped, a new one added.
	rename := map[string]string{}
	var rooms []string
	for i, old := range schedule.RoomNames() {
		name := clip(oneLine(r.FormValue("room_"+strconv.Itoa(i))), 40)
		rename[old] = name
		if name != "" {
			rooms = append(rooms, name)
		}
	}
	if n := clip(oneLine(r.FormValue("room_new")), 40); n != "" {
		rooms = append(rooms, n)
	}
	var tables []schedule.Table
	for _, t := range schedule.Tables() {
		id := strconv.Itoa(t.No)
		if r.FormValue("del_"+id) == "1" {
			continue
		}
		room := rename[t.Room]
		if v := r.FormValue("room_of_" + id); v != "" {
			if i, err := strconv.Atoi(v); err == nil && i >= 0 && i < len(schedule.RoomNames()) {
				room = rename[schedule.RoomNames()[i]]
			}
		}
		if room == "" {
			s.renderTablePlan(w, r, http.StatusUnprocessableEntity, stf(lang, "Room %s still has tables. Move or remove them first.", t.Room))
			return
		}
		seats := atoiDefault(r.FormValue("seats_"+id), t.Seats)
		tables = append(tables, schedule.Table{No: t.No, Room: room, Seats: seats})
	}
	if no := strings.TrimSpace(r.FormValue("new_no")); no != "" {
		n, err := strconv.Atoi(no)
		if err != nil {
			n = -1
		}
		room := ""
		if i, err := strconv.Atoi(r.FormValue("new_room")); err == nil && i >= 0 && i < len(schedule.RoomNames()) {
			room = rename[schedule.RoomNames()[i]]
		} else if len(rooms) > 0 {
			room = rooms[len(rooms)-1] // the new room
		}
		tables = append(tables, schedule.Table{No: n, Room: room, Seats: atoiDefault(r.FormValue("new_seats"), 4)})
	}
	if err := s.Store.SetTablePlan(rooms, tables, s.Now()); err != nil {
		var pe schedule.PlanError
		msg := st(lang, "Something went wrong. Try again.")
		if errors.As(err, &pe) {
			msg = st(lang, pe.Msg)
			if pe.Arg != nil {
				msg = stf(lang, pe.Msg, pe.Arg)
			}
		}
		s.renderTablePlan(w, r, http.StatusUnprocessableEntity, msg)
		return
	}
	s.logActivity(r, 0, "changed the tables and seats", strconv.Itoa(schedule.TableCount())+" tables")
	s.live.publish()
	redirectMsg(w, r, "/admin/tableplan", nil, "plansaved")
}
