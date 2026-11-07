package handlers

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// The end-of-day email: 30 minutes after closing, the owner gets the day in
// one email: what came in (card, cash, online), the tables and takeaway
// orders, the best sellers, and anything that needs a look (bills still
// open, voided bills, no-shows). Emails to staff are in English.

const reportAfterClose = 30 * time.Minute

// sendDailyReports sends the end-of-day email for yesterday and today, if
// it's due and hasn't gone out (so a restart doesn't lose or repeat one).
func (s *Server) sendDailyReports(now time.Time) {
	to := s.Cfg.ReportEmail
	if to == "" {
		to = s.Cfg.ManagerEmail
	}
	if to == "" {
		return
	}
	today := schedule.Today(now)
	days := []time.Time{today}
	// The server was off last night: send yesterday's in the morning, before opening.
	if h, open := schedule.HoursFor(today); !open || now.Before(today.Add(time.Duration(h.Open)*time.Minute)) {
		days = []time.Time{today.AddDate(0, 0, -1), today}
	}
	for _, day := range days {
		date := schedule.FormatDate(day)
		hours, open := schedule.HoursFor(day)
		if !open || !s.Store.DailyReportDue(date) {
			continue
		}
		if now.Before(day.Add(time.Duration(hours.Close)*time.Minute + reportAfterClose)) {
			continue
		}
		if s.quietDay(date, now) {
			_ = s.Store.MarkDailyReportSent(date) // nothing happened: no email
			continue
		}
		subject, body := s.dailyReport(day, now)
		s.Mail.SendAsync(to, subject, body)
		if err := s.Store.MarkDailyReportSent(date); err != nil {
			log.Printf("end-of-day email %s: %v", date, err)
		}
	}
}

// quietDay: no bills and no bookings at all (e.g. the system wasn't used yet).
func (s *Server) quietDay(date string, now time.Time) bool {
	return len(s.Store.BillsOpenedOn(date)) == 0 && len(s.Store.PaidBillsOn(date)) == 0 &&
		len(s.Store.BookingsBetween(date, date)) == 0
}

type dishCount struct {
	Name string
	Qty  int
}

// dailyReport writes a day's summary.
func (s *Server) dailyReport(day, now time.Time) (string, string) {
	lang := langEN
	date := schedule.FormatDate(day)
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	// Money at the tables
	var bills, tips, total int
	takings := map[string]int{}
	dishes := map[string]int{}
	vat := map[int]int{}
	for _, bill := range s.Store.PaidBillsOn(date) {
		bills++
		for m, c := range bill.Takings() {
			takings[m] += c
			total += c
		}
		tips += bill.Tips()
		for rate, cents := range bill.VATByRate() {
			vat[rate] += cents
		}
		for _, l := range bill.Lines {
			dishes[l.Name] += l.Qty
		}
	}
	bk := s.Store.BookingsBetween(date, date)
	stt := computeStats(bk, now)
	for _, x := range bk {
		if x.Kind == db.KindTakeaway && x.EffectiveStatus(now) != db.StatusCancelled && x.PaidAt != nil {
			for _, it := range x.Items {
				dishes[it.Name] += it.Quantity
			}
		}
	}

	line("%s · %s", s.Cfg.RestaurantName, capitalize(longDate(lang, date)))
	line("")
	billWord := "bills"
	if bills == 1 {
		billWord = "bill"
	}
	line("PAID AT THE TABLES: %s (%d %s)", euro(total), bills, billWord)
	if len(takings) == 0 {
		takings[db.PaidCard], takings[db.PaidCash] = 0, 0
	}
	for _, m := range append([]string{db.PaidCard, db.PaidCash}, db.Methods(takings)...) {
		c, ok := takings[m]
		if !ok {
			continue
		}
		delete(takings, m)
		note := ""
		if db.PrepaidMethod(m) {
			note = " (paid for when sold)"
		}
		name := methodName(lang, m)
		switch m {
		case db.PaidCard:
			name = "Card terminal"
		case db.PaidCash:
			name = "Cash"
		}
		line("  %-21s %s%s", name+":", euro(c), note)
	}
	if sold := s.Store.VouchersSoldOn(date); len(sold) > 0 {
		byKind, by := map[string]int{}, map[string]int{}
		for _, v := range sold {
			byKind[v.Kind] += v.SoldCents
			by[v.SoldWith] += v.SoldCents
		}
		line("  Gift cards sold:      %s", euro(byKind[db.VoucherGift]))
		line("  Lunch booklets sold:  %s", euro(byKind[db.VoucherLunch]))
		line("    (paid at the terminal %s, in cash %s; not in the total above)", euro(by[db.PaidCard]), euro(by[db.PaidCash]))
	}
	if rows := db.VATTable(vat); len(rows) > 0 {
		line("  VAT on what was sold (bills paid today, incl. deposits used):")
		line("    VAT %%     Without VAT        VAT   With VAT")
		for _, r := range rows {
			line("    %-6s %12s %10s %10s", vatRate(r.Rate), centsText(r.Net), centsText(r.VAT), centsText(r.Gross))
		}
	}
	if tips > 0 {
		line("  Tips on phones:       %s (for the staff, not in the total)", euro(tips))
	}
	line("")
	line("ONLINE DEPOSITS")
	line("  Table deposits paid:  %s", euro(stt.DepositsGross))
	if stt.Refunded > 0 {
		line("  Refunded:             -%s", euro(stt.Refunded))
	}
	if stt.KeptTotal() > 0 {
		line("  Kept (no-shows, late cancellations): %s", euro(stt.KeptTotal()))
	}
	line("")
	line("TABLES AND TAKEAWAY")
	line("  Table bookings: %d (%d came, %d no-shows)", stt.TableBookings, stt.Arrived, stt.NoShows)
	line("  Takeaway orders: %d (%d picked up)", stt.TakeawayOrders, stt.PickedUp)
	if ds := s.Store.DealStatsOn(date, now); ds.Tables > 0 {
		line("  3 meals: %d tables (%d finished all meals)", ds.Tables, ds.Finished)
	}

	// Best sellers
	var top []dishCount
	for name, q := range dishes {
		top = append(top, dishCount{name, q})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Qty != top[j].Qty {
			return top[i].Qty > top[j].Qty
		}
		return top[i].Name < top[j].Name
	})
	if len(top) > 5 {
		top = top[:5]
	}
	if len(top) > 0 {
		line("")
		line("BEST SELLERS")
		for _, d := range top {
			line("  %d × %s", d.Qty, d.Name)
		}
	}

	// Needs a look
	var look []string
	for _, bill := range s.Store.OpenBills() {
		if len(bill.Lines) > 0 && schedule.FormatDate(bill.OpenedAt) <= date {
			look = append(look, fmt.Sprintf("  Table %d: bill still open, %s not paid (opened %s)", bill.Table, euro(bill.ToPay()), clockOf(bill.OpenedAt)))
		}
	}
	for _, bill := range s.Store.BillsOpenedOn(date) {
		if bill.Voided && len(bill.Lines) > 0 {
			look = append(look, fmt.Sprintf("  Table %d: a bill of %s was voided", bill.Table, euro(bill.Total())))
		}
	}
	if fb := s.feedbackLine(date); fb != "" {
		line("")
		line("FEEDBACK")
		line("  %s", fb)
	}
	line("")
	if len(look) > 0 {
		line("NEEDS A LOOK")
		for _, l := range look {
			line("%s", l)
		}
	} else {
		line("Nothing left open. Good night!")
	}
	line("")
	line("More: %s/owner?date=%s", s.Cfg.BaseURL, date)
	line("")
	line("%s", signature(s.Cfg))

	subject := fmt.Sprintf("%s · %s: %s at the tables", s.Cfg.RestaurantName, shortDateL(lang, date), euro(total))
	return subject, b.String()
}

// ownerSummary shows the end-of-day email for a day (to read it now, or
// try it before email is set up).
func (s *Server) ownerSummary(w http.ResponseWriter, r *http.Request) {
	now := s.Now()
	day, err := schedule.ParseDate(r.URL.Query().Get("date"))
	if err != nil {
		day = schedule.Today(now)
	}
	subject, body := s.dailyReport(day, now)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "Subject: %s\n\n%s", subject, body)
}

// feedbackLine sums up a day's feedback for the end-of-day email ("" = none).
func (s *Server) feedbackLine(date string) string {
	list := s.Store.ReviewsOn(date)
	if len(list) == 0 {
		return ""
	}
	sum, low := 0, 0
	for _, r := range list {
		sum += r.Stars
		if r.Stars < 4 {
			low++
		}
	}
	avg := strings.Replace(fmt.Sprintf("%.1f", float64(sum)/float64(len(list))), ".", ",", 1)
	word := "ratings"
	if len(list) == 1 {
		word = "rating"
	}
	return fmt.Sprintf("%d %s, average %s ★ (%d under 4 stars)", len(list), word, avg, low)
}
