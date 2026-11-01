package db

import (
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Courses and messages to the kitchen, like the old till's buttons:
//
//   - "Vänta varmt": mains ordered together with starters wait. The chef sees
//     them greyed out under "Waiting for the go", so he can get ready.
//   - "Varma fram": the waiter gives the go for the mains (FireCourse).
//   - "Fram efterrätt": the same for desserts ordered with the meal.
//   - "Fritt köksmeddelande": a free-text message to the kitchen.
//
// The chef can also start a waiting course himself (FireTicket).

var ErrNothingHeld = errors.New("nothing is waiting for the go")

// MaxKitchenMessage is the longest message to the kitchen.
const MaxKitchenMessage = 200

// FireCourse gives the go for a table's waiting mains (CourseMain) or
// desserts (CourseDessert). It returns how many tickets went to the chef.
func (s *Store) FireCourse(billID, course int, now time.Time) (int, error) {
	n := 0
	err := s.changeOrders(func() (bool, error) {
		for _, t := range s.d.Tickets {
			if t.BillID == billID && t.Held && t.DoneAt == nil && (course == 0 || t.Course == course) {
				t.Held = false
				d := now
				t.FiredAt = &d
				n++
			}
		}
		if n == 0 {
			return false, ErrNothingHeld
		}
		return true, nil
	})
	return n, err
}

// FireTicket: the chef starts a waiting course himself.
func (s *Store) FireTicket(id int, now time.Time) error {
	return s.changeOrders(func() (bool, error) {
		t := s.ticketByID(id)
		if t == nil {
			return false, ErrNoTicket
		}
		if !t.Held {
			return false, nil
		}
		t.Held = false
		d := now
		t.FiredAt = &d
		return true, nil
	})
}

// HeldTickets are the courses waiting for the go, oldest first.
func (s *Store) HeldTickets() []Ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Ticket
	for _, t := range s.d.Tickets {
		if t.Held && t.DoneAt == nil && t.Live() {
			out = append(out, t.clone())
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// HeldCourses: which courses of a bill wait for the go (CourseMain,
// CourseDessert), each once, in order.
func (s *Store) HeldCourses(billID int) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	have := map[int]bool{}
	for _, t := range s.d.Tickets {
		if t.BillID == billID && t.Held && t.DoneAt == nil && t.Live() {
			have[t.Course] = true
		}
	}
	var out []int
	for _, c := range []int{CourseMain, CourseDessert} {
		if have[c] {
			out = append(out, c)
		}
	}
	return out
}

// KitchenMessage sends a waiter's message to the kitchen screen.
func (s *Store) KitchenMessage(table int, text string, now time.Time) (Ticket, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > MaxKitchenMessage {
		return Ticket{}, errors.New("message empty or too long")
	}
	var out Ticket
	err := s.changeOrders(func() (bool, error) {
		if !validTable(table) {
			return false, ErrNoTable
		}
		billID := 0
		if b := s.openBillAt(table); b != nil {
			billID = b.ID
		}
		s.d.NextTicketID++
		t := &Ticket{ID: s.d.NextTicketID, Table: table, BillID: billID, At: now, Message: text}
		s.d.Tickets = append(s.d.Tickets, t)
		out = t.clone()
		return true, nil
	})
	return out, err
}
