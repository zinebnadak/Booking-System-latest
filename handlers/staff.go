package handlers

import (
	"errors"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
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
	r.ParseForm()
	b, _, err := s.Store.MarkPickedUpWith(r.PathValue("ref"), r.FormValue("method"), s.Now())
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
func tableOptions() []schedule.Table { return schedule.Tables() }
