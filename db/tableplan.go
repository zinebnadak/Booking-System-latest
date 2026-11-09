package db

import (
	"time"

	"emeal-booking/schedule"
)

// ---------- The table plan, changed by the manager ----------

// PlanSetting is the manager's table plan, kept in the data file. Without
// one the restaurant uses schedule.DefaultTablePlan.
type PlanSetting struct {
	Rooms  []string    `json:"rooms"`
	Tables []PlanTable `json:"tables"`
}

// PlanTable is one table in the saved plan.
type PlanTable struct {
	No    int    `json:"no"`
	Room  string `json:"room"`
	Seats int    `json:"seats"`
}

func (p *PlanSetting) tables() []schedule.Table {
	out := make([]schedule.Table, len(p.Tables))
	for i, t := range p.Tables {
		out[i] = schedule.Table{No: t.No, Room: t.Room, Seats: t.Seats}
	}
	return out
}

// usePlan puts the saved plan in use (when the data file is opened).
func (s *Store) usePlan() error {
	if p := s.d.Settings.Plan; p != nil {
		return schedule.SetPlan(p.Rooms, p.tables())
	}
	return nil
}

// SetTableSeats changes how many seats one table has (waiters may do this
// from the table's page: chairs moved, tables pushed together).
func (s *Store) SetTableSeats(no, seats int, now time.Time) error {
	var tables []schedule.Table
	found := false
	for _, t := range schedule.Tables() {
		if t.No == no {
			t.Seats, found = seats, true
		}
		tables = append(tables, t)
	}
	if !found {
		return ErrNoTable
	}
	return s.SetTablePlan(schedule.RoomNames(), tables, now)
}

// SetTablePlan saves a new table plan and puts it in use. A table with an
// open bill or a booking still to come can't be removed.
func (s *Store) SetTablePlan(rooms []string, tables []schedule.Table, now time.Time) error {
	if err := schedule.CheckPlan(rooms, tables); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := map[int]bool{}
	for _, t := range tables {
		keep[t.No] = true
	}
	for _, b := range s.d.Bills {
		if b.Open() && schedule.ValidTable(b.Table) && !keep[b.Table] {
			return schedule.PlanError{Msg: "Table %d has an open bill. Close it first.", Arg: b.Table}
		}
	}
	today := schedule.FormatDate(now)
	for _, b := range s.d.Bookings {
		if b.TableNo > 0 && !keep[b.TableNo] && b.Date >= today && b.HoldsTable(now) {
			return schedule.PlanError{Msg: "Table %d is booked. Move the booking first.", Arg: b.TableNo}
		}
	}
	old, oldRooms, oldTables := s.d.Settings.Plan, schedule.RoomNames(), schedule.Tables()
	p := &PlanSetting{Rooms: append([]string(nil), rooms...)}
	if err := schedule.SetPlan(rooms, tables); err != nil {
		return err
	}
	for _, t := range schedule.Tables() { // room by room, as SetPlan ordered them
		p.Tables = append(p.Tables, PlanTable{No: t.No, Room: t.Room, Seats: t.Seats})
	}
	s.d.Settings.Plan = p
	s.ensureTableQRs() // new tables get a QR code and a button code
	s.ensureButtons()
	if err := s.save(); err != nil {
		s.d.Settings.Plan = old
		schedule.UseTables(oldRooms, oldTables)
		return err
	}
	return nil
}
