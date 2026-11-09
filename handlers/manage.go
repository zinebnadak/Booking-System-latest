package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// Guests can cancel or change their own booking from the link in their
// confirmation email (/b/<code>). This saves the restaurant phone calls,
// and a guest who can cancel in two taps is much less likely to just not
// turn up.

// ---------- Occasions ----------

// occasions guests can mention when booking (word keys in i18n.go: occ_<name>).
var occasions = []string{"birthday", "anniversary", "business"}

func validOccasion(o string) bool {
	if o == "" {
		return true
	}
	for _, x := range occasions {
		if x == o {
			return true
		}
	}
	return false
}

func occasionText(lang, o string) string {
	if o == "" || !validOccasion(o) {
		return ""
	}
	return tr(lang)["occ_"+o]
}

// ---------- What a guest may do ----------

// canCancel: a confirmed booking the guest hasn't come to yet.
func canCancel(b db.Booking, now time.Time) bool {
	return b.EffectiveStatus(now) == db.StatusPaid && now.Before(b.Start())
}

// canChange: a confirmed table booking, until the free-cancellation deadline
// (24 hours before). Later than that, the guest calls the restaurant.
func canChange(b db.Booking, now time.Time) bool {
	return b.Kind == db.KindTable && b.EffectiveStatus(now) == db.StatusPaid &&
		b.Start().Sub(now) >= schedule.RefundWindow
}

// guestFlash is the short message shown after a guest action.
func guestFlash(lang, msg string) string {
	switch msg {
	case "cancelled", "refunded", "changed", "waitlist_removed", "late":
		return tr(lang)["fl_"+msg]
	}
	return ""
}

// changeSizes are the party sizes a guest can switch to without a different
// deposit (1–2 or 3–6). Bookings without a deposit can pick any size.
func changeSizes(b db.Booking) []int {
	var out []int
	for n := 1; n <= schedule.MaxPartySize; n++ {
		if b.DepositCents == 0 || schedule.TableDeposit(n) == b.DepositCents {
			out = append(out, n)
		}
	}
	return out
}

// ---------- Cancelling ----------

var errRefundFailed = errors.New("refund failed")

// cancelBooking cancels a confirmed booking for staff or the guest.
// refund pays a table deposit back through Stripe (takeaway never). The guest
// gets an email; when the guest cancelled, the manager does too. A freed
// table is offered to the waiting list.
func (s *Server) cancelBooking(ctx context.Context, b db.Booking, refund bool, by string) (db.Booking, error) {
	refund = refund && b.Kind == db.KindTable && b.DepositCents > 0
	if refund && s.Pay.Enabled() && b.PaymentIntentID != "" && !strings.HasPrefix(b.PaymentIntentID, "demo_") {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := s.Pay.Refund(ctx, b.PaymentIntentID)
		cancel()
		if err != nil {
			log.Printf("refund %s: %v", b.Ref, err)
			return b, errRefundFailed
		}
	}
	updated, changed, err := s.Store.Cancel(b.Ref, refund, by, s.Now())
	if err != nil {
		return b, err
	}
	if changed {
		subject, body := cancellationEmail(s.Cfg, updated, refund)
		s.Mail.SendAsync(updated.Email, subject, body)
		if by == "guest" {
			subject, body = managerCancelEmail(s.Cfg, updated, refund)
			s.Mail.SendAsync(s.Cfg.ManagerEmail, subject, body)
		}
		if updated.Kind == db.KindTable {
			s.notifyWaitlist(updated.Date)
		}
		if updated.Kind == db.KindTakeaway { // struck through on the kitchen screen
			if err := s.Store.TakeawayCancelled(updated.Ref); err == nil {
				s.live.publish()
			}
		}
	}
	return updated, nil
}

// guestCancel: the guest cancels from their booking page.
func (s *Server) guestCancel(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	ref := r.PathValue("ref")
	b, err := s.Store.Get(ref)
	if err != nil {
		s.notFound(w, r)
		return
	}
	now := s.Now()
	if !canCancel(b, now) {
		http.Redirect(w, r, "/b/"+ref, http.StatusSeeOther)
		return
	}
	refund := b.Kind == db.KindTable && schedule.RefundEligible(b.Date, b.Time, now)
	updated, err := s.cancelBooking(r.Context(), b, refund, "guest")
	if err != nil {
		s.message(w, r, http.StatusBadGateway, "m_cancelfail_t", "m_cancelfail_x", "/b/"+ref, "m_back_booking")
		return
	}
	msg := "cancelled"
	if updated.Status == db.StatusRefunded {
		msg = "refunded"
	}
	http.Redirect(w, r, "/b/"+ref+"?msg="+msg, http.StatusSeeOther)
}

// ---------- Running late ----------

// guestLate: "We're running late" on the booking page, 15 or 30 minutes.
// The table is kept that much longer and the staff get a message.
func (s *Server) guestLate(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	ref := r.PathValue("ref")
	if _, err := s.Store.Get(ref); err != nil {
		s.notFound(w, r)
		return
	}
	b, changed, err := s.Store.RunningLate(ref, atoiDefault(r.FormValue("min"), 0), s.Now())
	if err != nil {
		http.Redirect(w, r, "/b/"+ref, http.StatusSeeOther)
		return
	}
	if changed {
		s.notifyLate(b)
		s.live.publish()
	}
	http.Redirect(w, r, "/b/"+ref+"?msg=late", http.StatusSeeOther)
}

// ---------- Changing ----------

func (s *Server) changeData(r *http.Request, b db.Booking, date string, size int, form, errs map[string]string) M {
	lang := langOf(r)
	data := s.bookData(r, date, size, form, errs, b.Ref)
	data["Title"] = tr(lang)["ch_title"]
	data["B"] = b
	data["Sizes"] = changeSizes(b)
	data["Current"] = capitalize(longDate(lang, b.Date)) + " " + tr(lang)["at"] + " " + b.Time
	return data
}

func (s *Server) changeGuard(w http.ResponseWriter, r *http.Request) (db.Booking, bool) {
	b, err := s.Store.Get(r.PathValue("ref"))
	if err != nil {
		s.notFound(w, r)
		return b, false
	}
	if !canChange(b, s.Now()) {
		s.render(w, r, "message", http.StatusConflict, M{
			"Title": tr(langOf(r))["ch_title"], "Heading": tr(langOf(r))["m_nochange_t"],
			"Text": tr(langOf(r))["m_nochange_x"], "LinkHref": "/b/" + b.Ref, "LinkText": tr(langOf(r))["m_back_booking"],
		})
		return b, false
	}
	return b, true
}

// sizeFor keeps the party size inside what the booking may change to.
func sizeFor(b db.Booking, n int) int {
	sizes := changeSizes(b)
	for _, x := range sizes {
		if x == n {
			return n
		}
	}
	if b.PartySize >= sizes[0] && b.PartySize <= sizes[len(sizes)-1] {
		return b.PartySize
	}
	return sizes[0]
}

func (s *Server) changePage(w http.ResponseWriter, r *http.Request) {
	b, ok := s.changeGuard(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	date := q.Get("date")
	if date == "" {
		date = b.Date
	}
	size := sizeFor(b, atoiDefault(q.Get("size"), b.PartySize))
	form := map[string]string{}
	if date == b.Date {
		form["time"] = b.Time
	}
	s.render(w, r, "change", 200, s.changeData(r, b, date, size, form, map[string]string{}))
}

func (s *Server) changeTimes(w http.ResponseWriter, r *http.Request) {
	b, err := s.Store.Get(r.PathValue("ref"))
	if err != nil || !canChange(b, s.Now()) {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	size := sizeFor(b, atoiDefault(q.Get("size"), b.PartySize))
	form := map[string]string{}
	if q.Get("date") == b.Date {
		form["time"] = b.Time
	}
	w.Header().Set("Cache-Control", "no-store")
	s.renderBlock(w, r, "change", "times", 200, s.changeData(r, b, q.Get("date"), size, form, map[string]string{}))
}

func (s *Server) changeSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	b, ok := s.changeGuard(w, r)
	if !ok {
		return
	}
	t := tr(langOf(r))
	now := s.Now()
	form := formValues(r, "date", "size", "time")
	errs := map[string]string{}
	size := atoiDefault(form["size"], 0)
	if sizeFor(b, size) != size {
		errs["size"] = t["err_size"]
		size = sizeFor(b, size)
	}
	data := s.changeData(r, b, form["date"], size, form, errs)
	if data["DayMessage"] != nil {
		s.render(w, r, "change", http.StatusUnprocessableEntity, data)
		return
	}
	d, _ := schedule.ParseDate(form["date"])
	valid := false
	for _, c := range schedule.TableSlots(d, now) {
		valid = valid || c == form["time"]
	}
	if !valid {
		errs["time"] = t["err_time"]
	}
	if len(errs) > 0 {
		s.render(w, r, "change", http.StatusUnprocessableEntity, data)
		return
	}
	if form["date"] == b.Date && form["time"] == b.Time && size == b.PartySize {
		http.Redirect(w, r, "/b/"+b.Ref, http.StatusSeeOther) // nothing changed
		return
	}
	moved, err := s.Store.MoveBooking(b.Ref, form["date"], form["time"], size, now)
	var dup db.ErrDuplicate
	switch {
	case errors.As(err, &dup):
		errs["time"] = fmt.Sprintf(t["err_duplicate"], dup.Time)
	case errors.Is(err, db.ErrFull):
		errs["time"] = t["err_taken"]
	case err != nil:
		log.Printf("change %s: %v", b.Ref, err)
		s.message(w, r, 500, "m_wrong_t", "m_wrong_book", "/b/"+b.Ref, "m_back_booking")
		return
	}
	if len(errs) > 0 {
		s.render(w, r, "change", http.StatusConflict, s.changeData(r, b, form["date"], size, form, errs))
		return
	}
	subject, body := changedEmail(s.Cfg, moved)
	s.Mail.SendAsync(moved.Email, subject, body)
	subject, body = managerChangeEmail(s.Cfg, b, moved)
	s.Mail.SendAsync(s.Cfg.ManagerEmail, subject, body)
	s.notifyWaitlist(b.Date) // the old time is free now
	http.Redirect(w, r, "/b/"+b.Ref+"?msg=changed", http.StatusSeeOther)
}

// ---------- Waiting list ----------

// waitDay checks that guests may wait for a table on date: an open day in
// the booking window that has at least one full time. It returns the day's
// times and how many tables each has free.
func (s *Server) waitDay(date string, size int, now time.Time) ([]string, map[string]int, bool) {
	d, err := schedule.ParseDate(date)
	today := schedule.Today(now)
	if err != nil || d.Before(today) || d.After(today.AddDate(0, 0, schedule.TableBookingDaysAhead)) {
		return nil, nil, false
	}
	if _, closed := s.Store.ClosedReason(date); closed {
		return nil, nil, false
	}
	clocks := schedule.TableSlots(d, now)
	if len(clocks) == 0 {
		return nil, nil, false
	}
	free := s.Store.FreeTables(date, clocks, size, true, now)
	for _, c := range clocks {
		if free[c] <= 0 {
			return clocks, free, true
		}
	}
	return clocks, free, false
}

func (s *Server) waitData(r *http.Request, date string, size int, form, errs map[string]string) M {
	lang := langOf(r)
	clocks, free, _ := s.waitDay(date, size, s.Now())
	var full []string
	for _, c := range clocks {
		if free[c] <= 0 {
			full = append(full, c)
		}
	}
	sizes := make([]int, schedule.MaxPartySize)
	for i := range sizes {
		sizes[i] = i + 1
	}
	return M{
		"Title":  tr(lang)["wl_title"],
		"Date":   date,
		"When":   longDate(lang, date),
		"Times":  clocks,
		"Full":   full,
		"Size":   size,
		"Sizes":  sizes,
		"Form":   form,
		"Errors": errs,
	}
}

func (s *Server) waitPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	date := q.Get("date")
	if _, _, ok := s.waitDay(date, clampSize(atoiDefault(q.Get("size"), 2)), s.Now()); !ok {
		http.Redirect(w, r, "/book?date="+date, http.StatusSeeOther)
		return
	}
	size := clampSize(atoiDefault(q.Get("size"), 2))
	s.render(w, r, "waitlist", 200, s.waitData(r, date, size, map[string]string{"time": q.Get("time")}, map[string]string{}))
}

func (s *Server) waitSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	lang := langOf(r)
	t := tr(lang)
	now := s.Now()
	form := formValues(r, "date", "size", "time", "name", "email", "phone")
	clocks, _, ok := s.waitDay(form["date"], clampSize(atoiDefault(form["size"], 2)), now)
	if !ok {
		http.Redirect(w, r, "/book?date="+form["date"], http.StatusSeeOther)
		return
	}
	errs := map[string]string{}
	c := checkContact(form, errs, t)
	size := atoiDefault(form["size"], 0)
	if size < 1 || size > schedule.MaxPartySize {
		errs["size"] = t["err_size"]
		size = clampSize(size)
	}
	if form["time"] != "" {
		valid := false
		for _, x := range clocks {
			valid = valid || x == form["time"]
		}
		if !valid {
			form["time"] = ""
		}
	}
	if len(errs) > 0 {
		s.render(w, r, "waitlist", http.StatusUnprocessableEntity, s.waitData(r, form["date"], size, form, errs))
		return
	}
	e, err := s.Store.AddWait(db.WaitEntry{
		Date: form["date"], Time: form["time"], PartySize: size,
		Name: c.Name, Email: c.Email, Phone: c.Phone, Lang: lang,
	}, now)
	if err != nil {
		log.Printf("add to waitlist: %v", err)
		s.message(w, r, 500, "m_wrong_t", "m_wrong_book", "/book", "m_try_again")
		return
	}
	subject, body := waitJoinedEmail(s.Cfg, e)
	s.Mail.SendAsync(e.Email, subject, body)
	http.Redirect(w, r, "/w/"+e.Ref, http.StatusSeeOther)
}

// waitStatus shows a guest their place on the waiting list.
func (s *Server) waitStatus(w http.ResponseWriter, r *http.Request) {
	e, err := s.Store.GetWait(r.PathValue("ref"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	lang := langOf(r)
	s.render(w, r, "waitlist", 200, M{
		"Title": tr(lang)["wl_title"], "Entry": e,
		"When":  longDate(lang, e.Date),
		"Day":   capitalize(longDate(lang, e.Date)),
		"Toast": guestFlash(lang, r.URL.Query().Get("msg")),
	})
}

func (s *Server) waitRemove(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	e, err := s.Store.RemoveWait(r.PathValue("ref"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	http.Redirect(w, r, "/w/"+e.Ref+"?msg=waitlist_removed", http.StatusSeeOther)
}

// nearTime: is a free start time close enough to what the guest asked for?
// No preference means any time is fine; otherwise up to an hour either way.
func nearTime(want, got string) bool {
	if want == "" {
		return true
	}
	a, err1 := schedule.ParseClock(want)
	b, err2 := schedule.ParseClock(got)
	if err1 != nil || err2 != nil {
		return false
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= 60
}

// notifyWaitlist emails everyone waiting for a date as soon as a table is
// free at a time that suits them. Each guest is told once.
func (s *Server) notifyWaitlist(date string) {
	now := s.Now()
	d, err := schedule.ParseDate(date)
	if err != nil {
		return
	}
	if _, closed := s.Store.ClosedReason(date); closed {
		return
	}
	clocks := schedule.TableSlots(d, now)
	if len(clocks) == 0 {
		return
	}
	for _, e := range s.Store.Waitlist(date) {
		if e.NotifiedAt != nil {
			continue
		}
		free := s.Store.FreeTables(date, clocks, e.PartySize, true, now)
		var times []string
		for _, c := range clocks {
			if free[c] > 0 && nearTime(e.Time, c) {
				times = append(times, c)
			}
		}
		if len(times) == 0 {
			continue
		}
		if changed, err := s.Store.MarkWaitNotified(e.Ref, now); err != nil || !changed {
			continue
		}
		subject, body := waitFreeEmail(s.Cfg, e, times)
		s.Mail.SendAsync(e.Email, subject, body)
	}
}
