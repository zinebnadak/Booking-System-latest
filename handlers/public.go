package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"emeal-booking/db"
	"emeal-booking/payments"
	"emeal-booking/schedule"
)

const maxFormBytes = 64 << 10

// How many days show as chips on the booking page.
const dayChips = 14

// ---------- Home ----------

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "home", 200, M{"Hours": hoursRows(langOf(r))})
}

// ---------- Day chips ----------

// dayChip is one tappable day: "mån / 26 / okt".
type dayChip struct {
	Date     string
	Weekday  string
	Num      int
	Month    string
	Label    string // "Idag" / "I morgon" / ""
	State    string // open, closed, full, none (no times left today)
	Selected bool
}

func newChip(lang string, d time.Time, i int) dayChip {
	t := tr(lang)
	c := dayChip{Date: schedule.FormatDate(d), Weekday: weekdayShort(lang, d), Num: d.Day(), Month: monthShort(lang, d), State: "open"}
	switch i {
	case 0:
		c.Label = t["today"]
	case 1:
		c.Label = t["tomorrow"]
	}
	return c
}

// ---------- Table booking ----------

// tableDays builds the 14 day chips, each marked open / closed / full.
// skip is a booking being moved: its own table counts as free.
func (s *Server) tableDays(lang string, size int, now time.Time, skip string) []dayChip {
	today := schedule.Today(now)
	var out []dayChip
	for i := 0; i < dayChips; i++ {
		d := today.AddDate(0, 0, i)
		c := newChip(lang, d, i)
		_, closedDate := s.Store.ClosedReason(c.Date)
		_, open := schedule.HoursFor(d)
		switch clocks := schedule.TableSlots(d, now); {
		case closedDate || !open:
			c.State = "closed"
		case len(clocks) == 0:
			c.State = "none"
		default:
			c.State = "full"
			for _, f := range s.Store.FreeTablesSkip(c.Date, clocks, size, true, now, skip) {
				if f > 0 {
					c.State = "open"
					break
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// bookPageData builds everything the booking page shows for a date and size.
func (s *Server) bookPageData(r *http.Request, date string, size int, form, errs map[string]string) M {
	return s.bookData(r, date, size, form, errs, "")
}

// bookData is bookPageData for a new booking (skip "") or for moving the
// booking skip, whose own table then counts as free.
func (s *Server) bookData(r *http.Request, date string, size int, form, errs map[string]string, skip string) M {
	lang := langOf(r)
	t := tr(lang)
	now := s.Now()
	today := schedule.Today(now)
	days := s.tableDays(lang, size, now, skip)

	// No date chosen yet: start on the first day with free tables.
	if date == "" {
		for _, c := range days {
			if c.State == "open" {
				date = c.Date
				break
			}
		}
	}
	inChips := false
	for i := range days {
		if days[i].Date == date {
			days[i].Selected = true
			inChips = true
		}
	}
	sizes := make([]int, schedule.MaxPartySize)
	for i := range sizes {
		sizes[i] = i + 1
	}
	data := M{
		"Title":     t["book_title"],
		"MinDate":   schedule.FormatDate(today),
		"MaxDate":   schedule.FormatDate(today.AddDate(0, 0, schedule.TableBookingDaysAhead)),
		"Sizes":     sizes,
		"Size":      size,
		"Date":      date,
		"Days":      days,
		"Deposit":   schedule.TableDeposit(size),
		"Form":      form,
		"Errors":    errs,
		"Occasions": occasions,
	}
	if date != "" && !inChips {
		data["OtherDate"] = capitalize(longDate(lang, date))
	}
	if date == "" {
		return data
	}
	d, err := schedule.ParseDate(date)
	when := longDate(lang, date)
	switch {
	case err != nil:
		data["DayMessage"] = t["msg_invalid_date"]
		return data
	case d.Before(today):
		data["DayMessage"] = t["msg_past"]
		return data
	case d.After(today.AddDate(0, 0, schedule.TableBookingDaysAhead)):
		data["DayMessage"] = fmt.Sprintf(t["msg_too_far"], schedule.TableBookingDaysAhead)
		return data
	}
	if reason, closed := s.Store.ClosedReason(date); closed {
		msg := fmt.Sprintf(t["msg_closed_on"], when)
		if reason != "" {
			msg = reason + ". " + msg
		}
		data["DayMessage"] = msg
		return data
	}
	if _, open := schedule.HoursFor(d); !open {
		data["DayMessage"] = fmt.Sprintf(t["msg_closed_on"], when)
		return data
	}
	clocks := schedule.TableSlots(d, now)
	if len(clocks) == 0 {
		data["DayMessage"] = fmt.Sprintf(t["msg_no_more_times"], when)
		return data
	}
	free := s.Store.FreeTablesSkip(date, clocks, size, true, now, skip)
	slots := make([]slotView, 0, len(clocks))
	anyFree, anyFull := false, false
	for _, c := range clocks {
		slots = append(slots, slotView{Time: c, Free: free[c], Full: free[c] <= 0})
		anyFree = anyFree || free[c] > 0
		anyFull = anyFull || free[c] <= 0
	}
	if anyFull && skip == "" {
		// Some or all times are full: offer the waiting list.
		data["WaitURL"] = fmt.Sprintf("/waitlist?date=%s&size=%d", date, size)
	}
	if !anyFree {
		data["DayMessage"] = fmt.Sprintf(t["msg_fully_booked"], when)
		if skip == "" {
			data["DayMessage"] = fmt.Sprintf(t["msg_fully_booked_wl"], when)
		}
		return data
	}
	data["Periods"] = groupSlots(lang, d, slots)
	data["When"] = capitalize(when)
	data["WhenShort"] = capitalize(shortDateL(lang, date))
	return data
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func clampSize(n int) int {
	if n < 1 {
		return 1
	}
	if n > schedule.MaxPartySize {
		return schedule.MaxPartySize
	}
	return n
}

func (s *Server) bookPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	size := clampSize(atoiDefault(q.Get("size"), 2))
	s.render(w, r, "book", 200, s.bookPageData(r, q.Get("date"), size, map[string]string{}, map[string]string{}))
}

// bookTimes returns just the "pick a time" part of the booking page, so the
// page can switch day without reloading.
func (s *Server) bookTimes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	size := clampSize(atoiDefault(q.Get("size"), 2))
	w.Header().Set("Cache-Control", "no-store")
	s.renderBlock(w, r, "book", "times", 200, s.bookPageData(r, q.Get("date"), size, map[string]string{}, map[string]string{}))
}

// guestICS lets a guest add their booking to their phone's calendar.
func (s *Server) guestICS(w http.ResponseWriter, r *http.Request) {
	b, err := s.Store.Get(r.PathValue("ref"))
	if err != nil || b.PaidAt == nil {
		s.notFound(w, r)
		return
	}
	lang := langOf(r)
	t := tr(lang)
	start := b.Start()
	end := start.Add(b.Duration())
	title := fmt.Sprintf(t["ics_table"], s.Cfg.RestaurantName)
	if b.Kind == db.KindTakeaway {
		end = start.Add(15 * time.Minute)
		title = fmt.Sprintf(t["ics_takeaway"], s.Cfg.RestaurantName)
	}
	desc := s.Cfg.BaseURL + "/b/" + b.Ref
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//emeal-booking//EN\r\nBEGIN:VEVENT\r\n" +
		"UID:" + b.Ref + "@emeal-booking\r\n" +
		"DTSTAMP:" + s.Now().UTC().Format("20060102T150405Z") + "\r\n" +
		"DTSTART:" + start.UTC().Format("20060102T150405Z") + "\r\n" +
		"DTEND:" + end.UTC().Format("20060102T150405Z") + "\r\n" +
		foldICS("SUMMARY:"+escICS(title)) + "\r\n" +
		foldICS("DESCRIPTION:"+escICS(desc)) + "\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="emeal-booking.ics"`)
	w.Write([]byte(ics))
}

func (s *Server) bookSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	lang := langOf(r)
	t := tr(lang)
	form := formValues(r, "date", "size", "time", "name", "email", "phone", "notes", "occasion")
	errs := map[string]string{}
	c := checkContact(form, errs, t)
	if !validOccasion(form["occasion"]) {
		form["occasion"] = ""
	}

	size := atoiDefault(form["size"], 0)
	if size < 1 || size > schedule.MaxPartySize {
		errs["size"] = t["err_size"]
		size = clampSize(size)
	}
	now := s.Now()
	data := s.bookPageData(r, form["date"], size, form, errs)
	if data["DayMessage"] != nil {
		s.render(w, r, "book", http.StatusUnprocessableEntity, data)
		return
	}
	d, _ := schedule.ParseDate(form["date"])
	validTime := false
	for _, slot := range schedule.TableSlots(d, now) {
		if slot == form["time"] {
			validTime = true
		}
	}
	if !validTime {
		errs["time"] = t["err_time"]
	}
	if len(errs) > 0 {
		s.render(w, r, "book", http.StatusUnprocessableEntity, data)
		return
	}

	b, err := s.Store.CreateTableBooking(db.Booking{
		Name: c.Name, Email: c.Email, Phone: c.Phone, Notes: c.Notes, Lang: lang,
		Date: form["date"], Time: form["time"], PartySize: size, Occasion: form["occasion"],
		DepositCents: schedule.TableDeposit(size),
	}, now)
	var dup db.ErrDuplicate
	if errors.As(err, &dup) {
		errs["time"] = fmt.Sprintf(t["err_duplicate"], dup.Time)
		s.render(w, r, "book", http.StatusConflict, s.bookPageData(r, form["date"], size, form, errs))
		return
	}
	if errors.Is(err, db.ErrFull) {
		errs["time"] = t["err_taken"]
		s.render(w, r, "book", http.StatusConflict, s.bookPageData(r, form["date"], size, form, errs))
		return
	}
	if errors.Is(err, db.ErrTooManyPending) {
		s.message(w, r, http.StatusTooManyRequests, "m_pending_t", "m_pending_x", "/book", "m_try_again")
		return
	}
	if err != nil {
		log.Printf("create booking: %v", err)
		s.message(w, r, 500, "m_wrong_t", "m_wrong_book", "/book", "m_try_again")
		return
	}
	desc := fmt.Sprintf(t["sd_table"], b.PartySize, shortDateL(lang, b.Date), b.Time)
	s.startPayment(w, r, b, desc)
}

// ---------- Takeaway ----------

type category struct {
	Name  string
	Items []db.MenuItem
}

// takeawayDay is a pickup day chip plus that day's pickup times.
type takeawayDay struct {
	dayChip
	Periods []period
	Times   []string
}

// takeawayDays lists the coming days on which pickup is possible.
func (s *Server) takeawayDays(lang string, now time.Time) []takeawayDay {
	today := schedule.Today(now)
	var out []takeawayDay
	for i := 0; i <= schedule.TakeawayDaysAhead; i++ {
		d := today.AddDate(0, 0, i)
		ds := schedule.FormatDate(d)
		if _, closed := s.Store.ClosedReason(ds); closed {
			continue
		}
		times := schedule.PickupSlots(d, now)
		if len(times) == 0 {
			continue
		}
		slots := make([]slotView, len(times))
		for j, c := range times {
			slots[j] = slotView{Time: c}
		}
		out = append(out, takeawayDay{dayChip: newChip(lang, d, i), Periods: groupSlots(lang, d, slots), Times: times})
	}
	return out
}

func groupMenu(items []db.MenuItem) []category {
	var cats []category
	index := map[string]int{}
	for _, it := range items {
		i, ok := index[it.Category]
		if !ok {
			i = len(cats)
			index[it.Category] = i
			cats = append(cats, category{Name: it.Category})
		}
		cats[i].Items = append(cats[i].Items, it)
	}
	return cats
}

func (s *Server) takeawayPageData(r *http.Request, date string, form, errs map[string]string, qty map[int]int) M {
	lang := langOf(r)
	days := s.takeawayDays(lang, s.Now())
	sel := -1
	for i := range days {
		if days[i].Date == date {
			sel = i
		}
	}
	if sel < 0 && len(days) > 0 {
		sel = 0
	}
	if sel >= 0 {
		days[sel].Selected = true
		date = days[sel].Date
	}
	return M{
		"Title":      tr(lang)["title_takeaway"],
		"Days":       days,
		"Date":       date,
		"Categories": groupMenu(s.Store.TakeawayMenu()),
		"Qty":        qty,
		"Form":       form,
		"Errors":     errs,
	}
}

func (s *Server) takeawayPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "takeaway", 200, s.takeawayPageData(r, r.URL.Query().Get("date"), map[string]string{}, map[string]string{}, map[int]int{}))
}

func (s *Server) takeawaySubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	lang := langOf(r)
	t := tr(lang)
	form := formValues(r, "date", "time", "name", "email", "phone", "notes")
	errs := map[string]string{}
	c := checkContact(form, errs, t)
	now := s.Now()

	// Prices always come from our menu, never from the browser.
	qty := map[int]int{}
	var items []db.OrderItem
	total := 0
	for _, m := range s.Store.TakeawayMenu() {
		n := atoiDefault(r.FormValue("qty_"+strconv.Itoa(m.ID)), 0)
		if n <= 0 {
			continue
		}
		if n > 20 {
			errs["items"] = t["err_items_max"]
			n = 20
		}
		qty[m.ID] = n
		items = append(items, db.OrderItem{MenuItemID: m.ID, Name: m.Name, UnitPriceCents: m.PriceCents, Quantity: n})
		total += m.PriceCents * n
	}
	if len(items) == 0 {
		errs["items"] = t["err_items"]
	} else if errs["items"] == "" {
		// Ran out since the page was opened (a waiter or a table took the last one).
		var lines []db.NewLine
		for _, it := range items {
			lines = append(lines, db.NewLine{MenuItemID: it.MenuItemID, Qty: it.Quantity})
		}
		var so *db.SoldOutError
		if err := s.Store.CheckStock(lines); errors.As(err, &so) {
			if so.Left > 0 && so.Short == "" {
				errs["items"] = fmt.Sprintf(t["err_soldout_left"], so.Left, so.Name)
			} else {
				errs["items"] = fmt.Sprintf(t["err_soldout"], so.Name)
			}
		}
	}

	// The pickup time must be one of the chosen day's times.
	data := s.takeawayPageData(r, form["date"], form, errs, qty)
	validTime := false
	for _, d := range data["Days"].([]takeawayDay) {
		if d.Date != form["date"] {
			continue
		}
		for _, p := range d.Times {
			if p == form["time"] {
				validTime = true
			}
		}
	}
	if !validTime {
		errs["time"] = t["err_pickup"]
	}
	if len(errs) > 0 {
		s.render(w, r, "takeaway", http.StatusUnprocessableEntity, data)
		return
	}

	b, err := s.Store.CreateTakeaway(db.Booking{
		Name: c.Name, Email: c.Email, Phone: c.Phone, Notes: c.Notes, Lang: lang,
		Date: form["date"], Time: form["time"],
		Items: items, TotalCents: total, DepositCents: schedule.TakeawayDeposit(total),
	}, now)
	if errors.Is(err, db.ErrTooManyPending) {
		s.message(w, r, http.StatusTooManyRequests, "m_pending_t", "m_pending_x", "/takeaway", "m_try_again")
		return
	}
	if err != nil {
		log.Printf("create takeaway: %v", err)
		s.message(w, r, 500, "m_wrong_t", "m_wrong_order", "/takeaway", "m_try_again")
		return
	}
	desc := fmt.Sprintf(t["sd_ta"], shortDateL(lang, b.Date), b.Time)
	s.startPayment(w, r, b, desc)
}

// ---------- Payment ----------

// startPayment creates the Stripe payment page, emails the link to the guest
// and sends them there. Without Stripe keys it uses a demo payment page.
func (s *Server) startPayment(w http.ResponseWriter, r *http.Request, b db.Booking, desc string) {
	base := s.publicBase(r) // not "localhost" when testing from a phone
	payURL := base + "/dev/pay/" + b.Ref
	sessionID := ""
	if s.Pay.Enabled() {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		sess, err := s.Pay.CreateCheckoutSession(ctx, payments.CheckoutParams{
			Reference:     b.Ref,
			Description:   desc + " · " + s.Cfg.RestaurantName,
			AmountCents:   b.DepositCents,
			CustomerEmail: b.Email,
			Locale:        b.Lang,
			SuccessURL:    base + "/b/" + b.Ref + "?session_id={CHECKOUT_SESSION_ID}",
			CancelURL:     base + "/b/" + b.Ref,
			ExpiresAt:     s.Now().Add(31 * time.Minute), // before our 35-min hold ends
		})
		if err != nil {
			log.Printf("stripe checkout for %s: %v", b.Ref, err)
			_ = s.Store.MarkExpired(b.Ref) // give the table back
			s.message(w, r, http.StatusBadGateway, "m_paystart_t", "m_paystart_x", "/", "m_back")
			return
		}
		payURL, sessionID = sess.URL, sess.ID
	}
	if err := s.Store.SetCheckout(b.Ref, sessionID, payURL); err != nil {
		log.Printf("save checkout for %s: %v", b.Ref, err)
	}
	subject, body := paymentLinkEmail(s.Cfg, b, payURL)
	s.Mail.SendAsync(b.Email, subject, body)
	http.Redirect(w, r, payURL, http.StatusSeeOther)
}

// confirmPaid marks a booking as paid and, the first time only, sends the
// confirmation to the guest and the notification to the manager.
func (s *Server) confirmPaid(ref, paymentIntentID string) error {
	b, changed, err := s.Store.MarkPaid(ref, paymentIntentID, s.Now())
	if err != nil || !changed {
		return err
	}
	if b.Kind == db.KindTable {
		s.Store.RemoveWaitFor(b.Date, b.Email, b.Phone) // they got a table: no need to wait
	}
	if b.Kind == db.KindTakeaway {
		// The chef sees it now, with the pickup time (and again an hour before).
		if added, err := s.Store.AddTakeawayTicket(b, s.Now()); err != nil {
			log.Printf("takeaway %s to the kitchen: %v", b.Ref, err)
		} else if added {
			s.live.publish()
		}
	}
	subject, body := confirmationEmail(s.Cfg, b)
	s.Mail.SendAsync(b.Email, subject, body)
	subject, body = managerEmail(s.Cfg, b)
	s.Mail.SendAsync(s.Cfg.ManagerEmail, subject, body)
	s.notifyManagerSMS(b)
	return nil
}

// notifyManagerSMS is where the SMS to the manager will be sent (phase 2).
func (s *Server) notifyManagerSMS(b db.Booking) {
	if s.Cfg.ManagerPhone == "" {
		return
	}
	log.Printf("SMS to %s (phase 2, not sent yet): new %s booking #%d for %s %s", s.Cfg.ManagerPhone, b.Kind, b.ID, b.Date, b.Time)
}

// ---------- Booking status page ----------

func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	b, err := s.Store.Get(ref)
	if err != nil {
		s.notFound(w, r)
		return
	}
	// Coming back from Stripe: double-check the payment directly, so the
	// guest sees "confirmed" even if Stripe's webhook hasn't arrived yet.
	if sid := r.URL.Query().Get("session_id"); sid != "" && sid == b.CheckoutSessionID && b.Status == db.StatusPending && s.Pay.Enabled() {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		sess, err := s.Pay.GetCheckoutSession(ctx, sid)
		cancel()
		if err == nil && sess.PaymentStatus == "paid" && sess.ClientReferenceID == b.Ref {
			if err := s.confirmPaid(b.Ref, sess.PaymentIntent); err != nil {
				log.Printf("confirm %s: %v", b.Ref, err)
			}
			b, _ = s.Store.Get(ref)
		}
	}
	lang := langOf(r)
	t := tr(lang)
	now := s.Now()
	data := M{
		"Title":     t["st_title"],
		"B":         b,
		"Status":    b.EffectiveStatus(now),
		"End":       b.EndClock(),
		"HoldUntil": b.HoldExpiresAt.In(schedule.Location).Format("15:04"),
		"PayURL":    b.CheckoutURL,
		"CanCancel": canCancel(b, now),
		"CanChange": canChange(b, now),
		"Toast":     guestFlash(lang, r.URL.Query().Get("msg")),
		"Occasion":  occasionText(lang, b.Occasion),
	}
	if b.Kind == db.KindTable && !b.Start().IsZero() {
		until := b.Start().Add(-schedule.RefundWindow)
		data["FreeCancelUntil"] = longDateT(lang, until) + " " + t["at"] + " " + until.In(schedule.Location).Format("15:04")
		data["RefundNow"] = b.DepositCents > 0 && schedule.RefundEligible(b.Date, b.Time, now)
	}
	s.render(w, r, "status", 200, data)
}

// ---------- Demo payment (only when Stripe isn't configured) ----------

func (s *Server) devPayPage(w http.ResponseWriter, r *http.Request) {
	if s.Pay.Enabled() {
		s.notFound(w, r)
		return
	}
	b, err := s.Store.Get(r.PathValue("ref"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.render(w, r, "devpay", 200, M{"Title": tr(langOf(r))["dp_title"], "B": b, "Status": b.EffectiveStatus(s.Now())})
}

func (s *Server) devPaySubmit(w http.ResponseWriter, r *http.Request) {
	if s.Pay.Enabled() {
		s.notFound(w, r)
		return
	}
	ref := r.PathValue("ref")
	b, err := s.Store.Get(ref)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if b.EffectiveStatus(s.Now()) == db.StatusPending {
		if err := s.confirmPaid(ref, "demo_"+ref); err != nil {
			log.Printf("demo pay %s: %v", ref, err)
		}
	}
	http.Redirect(w, r, "/b/"+ref, http.StatusSeeOther)
}

// ---------- Stripe webhook ----------

// stripeWebhook receives "payment done" messages from Stripe. This is the
// reliable way to confirm: it works even if the guest closes the browser.
func (s *Server) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.Pay.Enabled() {
		http.NotFound(w, r)
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ev, err := payments.ParseWebhook(payload, r.Header.Get("Stripe-Signature"), s.Pay.WebhookSecret, s.Now())
	if err != nil {
		log.Printf("webhook rejected: %v", err)
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}
	var sess payments.Session
	switch ev.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		if err := json.Unmarshal(ev.Data.Object, &sess); err != nil {
			http.Error(w, "bad object", http.StatusBadRequest)
			return
		}
		if sess.PaymentStatus == "paid" && strings.HasPrefix(sess.ClientReferenceID, "tab-") {
			s.tabPaid(sess.ClientReferenceID, sess.PaymentIntent) // a guest paid at their table
		} else if sess.PaymentStatus == "paid" {
			if err := s.confirmPaid(sess.ClientReferenceID, sess.PaymentIntent); err != nil && !errors.Is(err, db.ErrNotFound) {
				log.Printf("webhook confirm %s: %v", sess.ClientReferenceID, err)
				http.Error(w, "retry later", http.StatusInternalServerError) // Stripe will retry
				return
			}
		}
	case "checkout.session.expired":
		if err := json.Unmarshal(ev.Data.Object, &sess); err == nil {
			if strings.HasPrefix(sess.ClientReferenceID, "tab-") {
				_ = s.Store.ReleaseAttempt(sess.ClientReferenceID)
				s.live.publish()
			} else {
				_ = s.Store.MarkExpired(sess.ClientReferenceID)
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}
