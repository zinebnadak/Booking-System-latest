package handlers

import (
	"errors"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

// Bookings the staff make themselves: a guest who calls, or guests who walk
// in. They don't pay a deposit, and they show up in the same lists, tables
// and calendar as online bookings, so the floor plan is always complete.

// staffSlot is one start time in the "Phone booking" dialog.
type staffSlot struct {
	Time string
	Free int
}

// staffSlots lists the start times staff can book on a date: all of the
// day's times, except ones that are already over today.
func (s *Server) staffSlots(date string, now time.Time) []staffSlot {
	d, err := schedule.ParseDate(date)
	if err != nil {
		return nil
	}
	h, open := schedule.HoursFor(d)
	if !open {
		return nil
	}
	var clocks []string
	last := h.Close - int(schedule.BookingDuration/time.Minute)
	for m := h.Open; m <= last; m += int(schedule.TableSlotStep / time.Minute) {
		c := schedule.Clock(m)
		start, _ := schedule.At(d, c)
		if start.Add(schedule.NoShowAfter).After(now) {
			clocks = append(clocks, c)
		}
	}
	free := s.Store.FreeTables(date, clocks, 0, false, now)
	out := make([]staffSlot, len(clocks))
	for i, c := range clocks {
		out[i] = staffSlot{Time: c, Free: free[c]}
	}
	return out
}

func anyFree(slots []staffSlot) bool {
	for _, s := range slots {
		if s.Free > 0 {
			return true
		}
	}
	return false
}

// walkInClock is "now" rounded down to 5 minutes, e.g. 18:07 -> "18:05".
func walkInClock(now time.Time) string {
	t := now.In(schedule.Location)
	return schedule.Clock(t.Hour()*60 + t.Minute()/5*5)
}

// openNow: the restaurant is open at this moment (for walk-ins).
func (s *Server) openNow(now time.Time) bool {
	d := schedule.Today(now)
	h, open := schedule.HoursFor(d)
	if _, closed := s.Store.ClosedReason(schedule.FormatDate(d)); !open || closed {
		return false
	}
	m, _ := schedule.ParseClock(walkInClock(now))
	return m >= h.Open && m < h.Close
}

// minWalkIn: a table must be free for at least this long to seat a walk-in.
const minWalkIn = 30

// walkInTables are the tables free right now, and until when: a full
// 2 hours, or less if the table is booked later or the restaurant closes.
func (s *Server) walkInTables(now time.Time) []db.TableWindow {
	if !s.openNow(now) {
		return nil
	}
	d := schedule.Today(now)
	h, _ := schedule.HoursFor(d)
	clock := walkInClock(now)
	start, _ := schedule.ParseClock(clock)
	var out []db.TableWindow
	for _, w := range s.Store.FreeNow(schedule.FormatDate(d), clock, minWalkIn, now) {
		if start+w.Minutes > h.Close {
			w.Minutes = h.Close - start
			w.Until = schedule.Clock(h.Close)
		}
		if w.Minutes >= minWalkIn {
			out = append(out, w)
		}
	}
	return out
}

// staffContact checks the few details staff type in. Only a name is needed
// for a phone booking; a walk-in needs nothing.
func staffContact(r *http.Request, needName bool) (name, phone, email, notes string, ok bool) {
	name = oneLine(r.FormValue("name"))
	phone = oneLine(r.FormValue("phone"))
	email = strings.TrimSpace(r.FormValue("email"))
	notes = strings.TrimSpace(r.FormValue("notes"))
	if (needName && name == "") || len([]rune(name)) > 80 || len([]rune(notes)) > 500 || len(phone) > 30 {
		return "", "", "", "", false
	}
	if email != "" {
		if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
			return "", "", "", "", false
		}
	}
	return name, phone, email, notes, true
}

// adminNewBooking saves a booking taken on the phone.
func (s *Server) adminNewBooking(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	now := s.Now()
	date := r.FormValue("date")
	back := url.Values{"date": {date}}
	name, phone, email, notes, ok := staffContact(r, true)
	size := atoiDefault(r.FormValue("size"), 0)
	clock := r.FormValue("time")
	validTime := false
	for _, sl := range s.staffSlots(date, now) {
		validTime = validTime || sl.Time == clock
	}
	if !ok || !validTime || size < 1 || size > schedule.MaxPartySize {
		redirectMsg(w, r, "/admin/day", back, "invalid")
		return
	}
	occ := r.FormValue("occasion")
	if !validOccasion(occ) {
		occ = ""
	}
	b, err := s.Store.CreateStaffBooking(db.Booking{
		Source: db.SourcePhone, Name: name, Phone: phone, Email: email, Notes: notes,
		Occasion: occ, Lang: staffLang(r.FormValue("lang")),
		Date: date, Time: clock, PartySize: size,
	}, atoiDefault(r.FormValue("table"), 0), now)
	if msg := staffBookingError(err); msg != "" {
		redirectMsg(w, r, "/admin/day", back, msg)
		return
	}
	if email != "" {
		subject, body := confirmationEmail(s.Cfg, b)
		s.Mail.SendAsync(b.Email, subject, body)
	}
	back.Set("hl", b.Ref)
	redirectMsg(w, r, "/admin/day", back, "phonebooked")
}

// adminWalkIn seats guests who came in without a booking.
func (s *Server) adminWalkIn(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	now := s.Now()
	today := schedule.FormatDate(schedule.Today(now))
	back := url.Values{"date": {today}}
	name, phone, _, notes, ok := staffContact(r, false)
	size := atoiDefault(r.FormValue("size"), 0)
	table := atoiDefault(r.FormValue("table"), 0)
	if !ok || size < 1 || size > schedule.MaxPartySize {
		redirectMsg(w, r, "/admin/day", back, "invalid")
		return
	}
	// Use the table asked for, or else the smallest one with enough seats
	// (the one free the longest among those).
	var pick *db.TableWindow
	windows := s.walkInTables(now)
	better := func(w, than *db.TableWindow) bool {
		if than == nil {
			return true
		}
		if (w.Seats >= size) != (than.Seats >= size) {
			return w.Seats >= size
		}
		if w.Seats >= size && w.Seats != than.Seats {
			return w.Seats < than.Seats
		}
		if w.Seats < size && w.Seats != than.Seats {
			return w.Seats > than.Seats
		}
		return w.Minutes > than.Minutes
	}
	for i := range windows {
		if windows[i].No == table || (table == 0 && better(&windows[i], pick)) {
			pick = &windows[i]
		}
	}
	if pick == nil {
		redirectMsg(w, r, "/admin/day", back, "nowalkin")
		return
	}
	if name == "" {
		name = st(langOf(r), "Walk-in")
	}
	minutes := pick.Minutes
	if minutes >= int(schedule.BookingDuration/time.Minute) {
		minutes = 0 // the normal 2 hours
	}
	b, err := s.Store.CreateStaffBooking(db.Booking{
		Source: db.SourceWalkIn, Name: name, Phone: phone, Notes: notes,
		Date: today, Time: walkInClock(now), PartySize: size, Minutes: minutes,
	}, pick.No, now)
	if msg := staffBookingError(err); msg != "" {
		redirectMsg(w, r, "/admin/day", back, msg)
		return
	}
	s.openOnArrival(pick.No, now) // they can order and pay with the table's QR code
	back.Set("hl", b.Ref)
	redirectMsg(w, r, "/admin/day", back, "seated")
}

func staffBookingError(err error) string {
	var dup db.ErrDuplicate
	switch {
	case err == nil:
		return ""
	case errors.As(err, &dup):
		return "duplicate"
	case errors.Is(err, db.ErrFull):
		return "tablebusy"
	}
	log.Printf("staff booking: %v", err)
	return "invalid"
}

func staffLang(l string) string {
	if validLang(l) {
		return l
	}
	return defaultLang
}

// adminPickedUp marks a takeaway order as collected.
func (s *Server) adminPickedUp(w http.ResponseWriter, r *http.Request) {
	b, _, err := s.Store.MarkPickedUp(r.PathValue("ref"), s.Now())
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.Store.TakeawayCollected(b.Ref, s.Now()); err == nil { // off the kitchen screen
		s.live.publish()
	}
	redirectMsg(w, r, "/admin/day", url.Values{"date": {b.Date}}, "pickedup")
}

// adminWaitRemove takes someone off the waiting list.
func (s *Server) adminWaitRemove(w http.ResponseWriter, r *http.Request) {
	e, err := s.Store.RemoveWait(r.PathValue("ref"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	redirectMsg(w, r, "/admin/day", url.Values{"date": {e.Date}}, "waitremoved")
}

// tableOptions is every table for the "which table" pickers.
func tableOptions() []schedule.Table { return schedule.TablePlan }
