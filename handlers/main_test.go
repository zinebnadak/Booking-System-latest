package handlers

import (
	"os"
	"testing"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// Most tests were written for a small restaurant: 6 tables of 6 seats in one
// room. They run with that plan; the tests for the real plan (rooms, seats)
// swap it in themselves with realTables.
var smallPlan = []schedule.Table{
	{No: 1, Room: "Utrymme 1", Seats: 6}, {No: 2, Room: "Utrymme 1", Seats: 6}, {No: 3, Room: "Utrymme 1", Seats: 6},
	{No: 4, Room: "Utrymme 1", Seats: 6}, {No: 5, Room: "Utrymme 1", Seats: 6}, {No: 6, Room: "Utrymme 1", Seats: 6},
}

var realRooms, realPlan = schedule.RoomNames(), schedule.Tables()

// realTables switches to the restaurant's real rooms and tables for one test.
func realTables(t *testing.T) {
	t.Helper()
	t.Cleanup(schedule.UseTables(realRooms, realPlan))
}

func TestMain(m *testing.M) {
	schedule.UseTables([]string{"Utrymme 1"}, smallPlan)
	db.UseStartMenu(db.DemoMenu) // the tests were written against the small demo menu
	os.Exit(m.Run())
}
