package handlers

import (
	"strconv"

	"emeal-booking/schedule"
)

// Tables are shown room by room (Utrymme 1, Utrymme 2, Terrassen), with tabs
// on top to show one room at a time, like the old till. The chosen tab is
// remembered on each screen (static/app.js, data-room-view).

// roomGroup is one room's tables on a staff screen.
type roomGroup[T any] struct {
	ID    string // "r1": what the tabs and sections are matched by
	Name  string
	Tiles []T
	Busy  int // tables with something going on (the number on the tab)
}

// roomID: the tab id of a room ("r1"), or of the room a table is in.
func roomID(room string) string {
	for i, r := range schedule.Rooms {
		if r == room {
			return "r" + strconv.Itoa(i+1)
		}
	}
	return ""
}

func roomIDOfTable(n int) string {
	t, ok := schedule.TableInfo(n)
	if !ok {
		return ""
	}
	return roomID(t.Room)
}

// byRoom sorts tiles into their rooms, in the plan's order.
func byRoom[T any](tiles []T, room func(T) string, busy func(T) bool) []roomGroup[T] {
	groups := make([]roomGroup[T], len(schedule.Rooms))
	idx := map[string]int{}
	for i, r := range schedule.Rooms {
		groups[i] = roomGroup[T]{ID: roomID(r), Name: r}
		idx[r] = i
	}
	for _, t := range tiles {
		i, ok := idx[room(t)]
		if !ok {
			continue
		}
		groups[i].Tiles = append(groups[i].Tiles, t)
		if busy(t) {
			groups[i].Busy++
		}
	}
	return groups
}
