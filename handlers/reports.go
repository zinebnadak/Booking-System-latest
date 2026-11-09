package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// The head manager's pages: Today (what happened on one day) and Charts
// (day by day and month by month).
//
// Everything counts money that went through the website: table deposits and
// takeaway orders. What guests pay at the table goes through the restaurant's
// till, so it isn't included. A booking counts on the day it is for (the
// visit or pickup day), not the day it was made.

// stats are the numbers for a set of bookings (a day, a month...).
type stats struct {
	Revenue       int // online money the restaurant keeps (cents)
	TakeawaySales int // food from takeaway orders that weren't cancelled
	TakeawayKept  int // deposits kept from cancelled takeaway orders
	ToCollect     int // takeaway money still to be paid at pickup
	DepositsNet   int // table deposits paid minus refunds
	DepositsUsed  int // ...taken off the bill of guests who came (or are coming)
	DepositsKept  int // ...kept from no-shows and late cancellations
	DepositsGross int // all table deposits paid
	Refunded      int // table deposits paid back

	TableBookings  int // confirmed table bookings (any outcome), online and by staff
	DepositsPaid   int // ...of which paid a deposit online
	ByPhone        int // ...of which staff took on the phone (no deposit)
	WalkIns        int // ...of which walked in
	PickedUp       int // takeaway orders collected
	Guests         int // guests on paid table bookings that weren't cancelled
	GuestsArrived  int // guests who were checked in
	TakeawayOrders int // paid takeaway orders that weren't cancelled

	Arrived            int // checked in
	Upcoming           int // paid, not checked in yet, not a no-show yet
	NoShows            int // paid, nobody came: deposit kept
	TableCancelledKept int // paid, then cancelled, deposit kept
	CancelledFree      int // phone bookings cancelled (there was no deposit)
	Refunds            int // paid, then cancelled and refunded
	TakeawayCancelled  int // paid takeaway, then cancelled (deposit kept)
	NeverPaid          int // started a booking but never paid

	// Wolt orders picked up (see wolt.go): what the guests paid, Wolt's cut
	// and what the restaurant keeps.
	WoltOrders int
	WoltSales  int
	WoltFee    int
	WoltNet    int
	WoltPct    int
	WoltPctSet bool

	// At the tables: bills paid (card, cash, lunch cards, phones) and gift
	// cards sold (see sales.go).
	Till         int // money in at the tables and for gift cards
	TillBills    int
	TillCash     int // of which cash
	Tips         int
	Prepaid      int // paid with gift cards and lunch coupons sold earlier (not counted again)
	Vouchers     int
	VoucherCents int

	// Online is the website's money alone (deposits and takeaway). Revenue
	// is everything: the website, the tables and Wolt (what we keep), with
	// or without Wolt as the manager picked (woltMode).
	Online int
}

// Wolt in the money pages: "with" (everything together, the start),
// "without" (the restaurant's own sales) or "only" (just Wolt).
const (
	woltWith    = "with"
	woltWithout = "without"
	woltOnly    = "only"
)

// woltMode is the Wolt filter from the page's address.
func woltMode(r *http.Request) string {
	switch m := r.URL.Query().Get("wolt"); m {
	case woltWithout, woltOnly:
		return m
	}
	return woltWith
}

// addMoney puts the table bills, gift cards sold and Wolt orders from one
// date to another (both included) into m, and Revenue as the filter says.
// m has the website's bookings already (computeStats). It returns the
// tables' money by way of paying.
func (s *Server) addMoney(m *stats, from, to time.Time, mode string) map[string]int {
	f, t := schedule.FormatDate(from), schedule.FormatDate(to)
	methods := map[string]int{}
	for _, d := range s.Store.TillByDate(f, t) {
		m.Till += d.Total()
		m.TillBills += d.Bills
		m.TillCash += d.Cash
		m.Tips += d.Tips
		m.Prepaid += d.Prepaid
		m.Vouchers += d.Vouchers
		m.VoucherCents += d.VoucherCents
		for k, c := range d.Methods {
			methods[k] += c
		}
	}
	for _, w := range s.Store.WoltByDate(f, t) {
		m.WoltOrders += w.Orders
		m.WoltSales += w.Cents
	}
	m.WoltPct, m.WoltPctSet = s.Store.WoltPct()
	m.WoltFee = woltFee(m.WoltSales, m.WoltPct)
	m.WoltNet = m.WoltSales - m.WoltFee
	m.Online = m.Revenue
	switch mode {
	case woltWith:
		m.Revenue = m.Online + m.Till + m.WoltNet
	case woltWithout:
		m.Revenue = m.Online + m.Till
	case woltOnly:
		m.Revenue = m.WoltNet
	}
	return methods
}

// woltFee is Wolt's cut of some orders, rounded to the cent.
func woltFee(cents, pct int) int { return (cents*pct + 50) / 100 }

// AvgBill is what a table bill came to, on average.
func (m stats) AvgBill() int {
	if m.TillBills == 0 {
		return 0
	}
	return (m.Till - m.VoucherCents + m.Prepaid + m.TillBills/2) / m.TillBills
}

// Lost is everything that was paid for but didn't happen.
func (s stats) Lost() int {
	return s.NoShows + s.TableCancelledKept + s.CancelledFree + s.Refunds + s.TakeawayCancelled
}

// KeptTotal is all deposits the restaurant kept without serving the guest.
func (s stats) KeptTotal() int { return s.DepositsKept + s.TakeawayKept }

// computeStats adds up a list of bookings.
func computeStats(bs []db.Booking, now time.Time) stats {
	var m stats
	for _, b := range bs {
		st := b.EffectiveStatus(now)
		if b.PaidAt == nil {
			if st != db.StatusPending { // still on the payment page doesn't count yet
				m.NeverPaid++
			}
			continue
		}
		if b.Kind == db.KindTable {
			m.TableBookings++
			m.DepositsGross += b.DepositCents
			switch {
			case b.Source == db.SourcePhone:
				m.ByPhone++
			case b.Source == db.SourceWalkIn:
				m.WalkIns++
			}
			if b.DepositCents > 0 {
				m.DepositsPaid++
			}
			switch st {
			case db.StatusArrived:
				m.Arrived++
				m.Guests += b.PartySize
				m.GuestsArrived += b.PartySize
				m.DepositsUsed += b.DepositCents
			case db.StatusPaid:
				m.Upcoming++
				m.Guests += b.PartySize
				m.DepositsUsed += b.DepositCents // will be taken off their bill
			case db.StatusNoShow:
				m.NoShows++
				m.DepositsKept += b.DepositCents
			case db.StatusCancelled:
				if b.DepositCents == 0 {
					m.CancelledFree++
				} else {
					m.TableCancelledKept++
					m.DepositsKept += b.DepositCents
				}
			case db.StatusRefunded:
				m.Refunds++
				m.Refunded += b.DepositCents
			}
			continue
		}
		switch st {
		case db.StatusCancelled:
			m.TakeawayCancelled++
			m.TakeawayKept += b.DepositCents
		case db.StatusRefunded: // not possible any more, kept for old data
			m.Refunds++
		case db.StatusPickedUp:
			m.TakeawayOrders++
			m.TakeawaySales += b.TotalCents
			m.PickedUp++
		default:
			m.TakeawayOrders++
			m.TakeawaySales += b.TotalCents
			m.ToCollect += b.RemainingCents()
		}
	}
	m.DepositsNet = m.DepositsGross - m.Refunded
	m.Revenue = m.TakeawaySales + m.TakeawayKept + m.DepositsNet
	return m
}

// statsBetween adds up every booking and Wolt order from one day to
// another (inclusive).
func (s *Server) statsBetween(from, to time.Time, now time.Time, mode string) stats {
	m := computeStats(s.Store.BookingsBetween(schedule.FormatDate(from), schedule.FormatDate(to)), now)
	s.addMoney(&m, from, to, mode)
	return m
}

// ---------- Comparisons ----------

type change struct {
	Text string // "+12 %", "−5 %", "new", "±0 %", ""
	Up   bool
	Down bool
}

func pctChange(now, before int) change {
	switch {
	case before == 0 && now == 0:
		return change{}
	case before == 0:
		return change{Text: "new", Up: true}
	}
	p := float64(now-before) / float64(before) * 100
	switch {
	case p >= 0.5:
		return change{Text: fmt.Sprintf("+%.0f %%", p), Up: true}
	case p <= -0.5:
		return change{Text: fmt.Sprintf("−%.0f %%", -p), Down: true}
	}
	return change{Text: "±0 %"}
}

// ---------- Today ----------

type todayRow struct {
	db.Booking
	Status  db.Status
	Amount  int    // deposit (table) or food total (takeaway)
	Summary string // "4 guests · table 2" or "3 dishes"
}

func (s *Server) ownerToday(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	today := schedule.Today(now)
	day, err := schedule.ParseDate(r.URL.Query().Get("date"))
	if err != nil {
		day = today
	}
	date := schedule.FormatDate(day)

	mode := woltMode(r)
	bs := s.Store.BookingsBetween(date, date)
	stt := computeStats(bs, now)
	methods := s.addMoney(&stt, day, day, mode)
	weekAgo := s.statsBetween(day.AddDate(0, 0, -7), day.AddDate(0, 0, -7), now, mode)

	// This month so far vs the same days of last month.
	monthStart := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, schedule.Location)
	mtd := s.statsBetween(monthStart, day, now, mode)
	prevStart := monthStart.AddDate(0, -1, 0)
	prevEnd := prevStart.AddDate(0, 0, day.Day()-1)
	if lastOfPrev := monthStart.AddDate(0, 0, -1); prevEnd.After(lastOfPrev) {
		prevEnd = lastOfPrev
	}
	prevMtd := s.statsBetween(prevStart, prevEnd, now, mode)

	var rows []todayRow
	for _, b := range bs {
		row := todayRow{Booking: b, Status: b.EffectiveStatus(now)}
		if b.Kind == db.KindTable {
			row.Amount = b.DepositCents
			row.Summary = guestsCount(lang, b.PartySize)
			switch b.Source {
			case db.SourcePhone:
				row.Summary = st(lang, "by phone") + " · " + row.Summary
			case db.SourceWalkIn:
				row.Summary = st(lang, "walk-in") + " · " + row.Summary
			}
			if b.TableNo > 0 {
				row.Summary += " · " + stf(lang, "table %d", b.TableNo)
			}
		} else {
			row.Amount = b.TotalCents
			n := 0
			for _, it := range b.Items {
				n += it.Quantity
			}
			row.Summary = stf(lang, "%d dishes", n)
			if n == 1 {
				row.Summary = st(lang, "1 dish")
			}
		}
		rows = append(rows, row)
	}

	s.render(w, r, "owner_today", 200, M{
		"Title":     st(lang, "Today"),
		"Admin":     true,
		"Owner":     true,
		"Nav":       "today",
		"Date":      date,
		"IsToday":   day.Equal(today),
		"Prev":      schedule.FormatDate(day.AddDate(0, 0, -1)),
		"Next":      schedule.FormatDate(day.AddDate(0, 0, 1)),
		"S":         stt,
		"WeekAgo":   weekAgo,
		"WeekDay":   weekdayLong(lang, day.Weekday()),
		"VsWeek":    pctChange(stt.Revenue, weekAgo.Revenue),
		"MTD":       mtd,
		"PrevMTD":   prevMtd,
		"VsMonth":   pctChange(mtd.Revenue, prevMtd.Revenue),
		"MonthName": capitalize(monthName(lang, day.Month())),
		"PrevName":  monthName(lang, prevStart.Month()),
		"Rows":      rows,
		"Month":     day.Format("2006-01"),
		"WoltMode":  mode,
		"TillRows":  takingRows(lang, methods),
		"Cash":      s.cashPanel(lang, date),
		"Toast":     flashText(r),
	})
}

// ---------- Charts ----------

// chartSeries and chartData are sent to static/charts.js as JSON.
type chartSeries struct {
	Name   string     `json:"name"`
	Kind   string     `json:"kind"` // "bar" or "line"
	Color  string     `json:"color"`
	Dashed bool       `json:"dashed,omitempty"`
	Values []*float64 `json:"values"` // nil = no data (e.g. 31 Sept)
	Extra  [][]string `json:"extra,omitempty"`
}

type chartData struct {
	Labels    []string      `json:"labels"`
	Long      []string      `json:"long"` // labels for the hover box
	Unit      string        `json:"unit"` // "eur" or "count"
	Series    []chartSeries `json:"series"`
	Highlight int           `json:"highlight"` // index to emphasise (today), -1 for none
	Emphasise bool          `json:"emphasise,omitempty"`
}

// Colours, checked for colour-blind safety and contrast on white.
const (
	colorThis = "#a8711e" // this period (brass)
	colorLast = "#1f6fb2" // last period (blue)
)

type metric struct {
	Key, Label, Unit string
	pick             func(dayVals) float64
}

// dayVals are the numbers for one day or month.
type dayVals struct {
	Money, Takeaway, Deposits    int
	Tables, Guests, Orders, Lost int
	Wolt, WoltOrders             int // Wolt orders picked up, and what we keep of them
	Till                         int // money in at the tables and for gift cards
}

func (d *dayVals) add(b db.Booking, now time.Time) {
	if b.PaidAt == nil {
		return
	}
	st := b.EffectiveStatus(now)
	if b.Kind == db.KindTable {
		d.Tables++
		switch st {
		case db.StatusRefunded:
			d.Lost++
			return
		case db.StatusNoShow, db.StatusCancelled:
			d.Lost++
		default:
			d.Guests += b.PartySize
		}
		d.Deposits += b.DepositCents
		d.Money += b.DepositCents
		return
	}
	if st == db.StatusCancelled {
		d.Lost++
		d.Takeaway += b.DepositCents
		d.Money += b.DepositCents
		return
	}
	d.Orders++
	d.Takeaway += b.TotalCents
	d.Money += b.TotalCents
}

var metrics = []metric{
	{"money", "Money", "eur", func(d dayVals) float64 { return float64(d.Money) / 100 }},
	{"tables", "Table bookings", "count", func(d dayVals) float64 { return float64(d.Tables) }},
	{"guests", "Guests", "count", func(d dayVals) float64 { return float64(d.Guests) }},
	{"orders", "Takeaway orders", "count", func(d dayVals) float64 { return float64(d.Orders) }},
	{"lost", "No-shows & cancellations", "count", func(d dayVals) float64 { return float64(d.Lost) }},
	{"wolt", "Wolt orders", "count", func(d dayVals) float64 { return float64(d.WoltOrders) }},
}

// byDate groups bookings into per-day numbers.
func byDate(bs []db.Booking, now time.Time) map[string]*dayVals {
	out := map[string]*dayVals{}
	for _, b := range bs {
		v := out[b.Date]
		if v == nil {
			v = &dayVals{}
			out[b.Date] = v
		}
		v.add(b, now)
	}
	return out
}

// byDateWolt is byDate for the bookings from one date to another, with the
// Wolt orders added: Money has them in or not, as the filter says.
func (s *Server) byDateWolt(from, to time.Time, now time.Time, mode string) map[string]*dayVals {
	f, t := schedule.FormatDate(from), schedule.FormatDate(to)
	out := byDate(s.Store.BookingsBetween(f, t), now)
	get := func(d string) *dayVals {
		v := out[d]
		if v == nil {
			v = &dayVals{}
			out[d] = v
		}
		return v
	}
	for d, till := range s.Store.TillByDate(f, t) {
		v := get(d)
		v.Till = till.Total()
		v.Money += v.Till
	}
	if mode == woltOnly {
		for _, v := range out {
			v.Money = 0
		}
	}
	pct, _ := s.Store.WoltPct()
	for d, w := range s.Store.WoltByDate(f, t) {
		v := get(d)
		v.Wolt, v.WoltOrders = w.Cents-woltFee(w.Cents, pct), w.Orders
		if mode != woltWithout {
			v.Money += v.Wolt
		}
	}
	return out
}

func fptr(f float64) *float64 { return &f }

func moneyExtra(lang string, d dayVals, mode string) []string {
	switch mode {
	case woltOnly:
		return []string{stf(lang, "%d Wolt orders", d.WoltOrders)}
	case woltWithout:
		return []string{st(lang, "At the tables") + " " + euro(d.Till), st(lang, "Takeaway") + " " + euro(d.Takeaway), st(lang, "Table deposits") + " " + euro(d.Deposits)}
	}
	return []string{st(lang, "At the tables") + " " + euro(d.Till), st(lang, "Takeaway") + " " + euro(d.Takeaway), st(lang, "Table deposits") + " " + euro(d.Deposits), "Wolt " + euro(d.Wolt)}
}

type countBar struct {
	Label string
	Count int
	Pct   float64
}

type dishRow struct {
	Name    string
	Qty     int
	Revenue int
	Pct     float64
}

type outcomeRow struct {
	Label string
	Note  string
	Count int
	Pct   float64
	Tone  string // ok, wait, bad, muted
}

func (s *Server) ownerCharts(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	today := schedule.Today(now)
	q := r.URL.Query()
	mode := woltMode(r)
	view := q.Get("view")
	if view != "months" {
		view = "days"
	}
	met := metrics[0]
	for _, m := range metrics {
		if m.Key == q.Get("metric") {
			met = m
		}
	}
	month, err := time.ParseInLocation("2006-01", q.Get("month"), schedule.Location)
	if err != nil {
		month = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, schedule.Location)
	}
	first := month
	last := first.AddDate(0, 1, -1)

	var chart chartData
	chart.Unit = met.Unit
	chart.Highlight = -1
	var periodFrom, periodTo time.Time
	var thisTotal, lastTotal float64
	var thisName, lastName string

	if view == "days" {
		periodFrom, periodTo = first, last
		prevFirst := first.AddDate(0, -1, 0)
		prevLast := first.AddDate(0, 0, -1)
		cur := s.byDateWolt(first, last, now, mode)
		prev := s.byDateWolt(prevFirst, prevLast, now, mode)
		thisName, lastName = capitalize(monthName(lang, first.Month())), capitalize(monthName(lang, prevFirst.Month()))
		this := chartSeries{Name: thisName, Kind: "bar", Color: colorThis}
		before := chartSeries{Name: lastName, Kind: "line", Color: colorLast, Dashed: true}
		for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
			chart.Labels = append(chart.Labels, strconv.Itoa(d.Day()))
			chart.Long = append(chart.Long, capitalize(shortDateL(lang, schedule.FormatDate(d))))
			if d.Equal(today) {
				chart.Highlight = len(chart.Labels) - 1
			}
			var v dayVals
			if p := cur[schedule.FormatDate(d)]; p != nil {
				v = *p
			}
			if d.After(today) {
				this.Values = append(this.Values, nil)
			} else {
				x := met.pick(v)
				thisTotal += x
				this.Values = append(this.Values, fptr(x))
			}
			if met.Key == "money" {
				this.Extra = append(this.Extra, moneyExtra(lang, v, mode))
			}
			pd := prevFirst.AddDate(0, 0, d.Day()-1)
			if pd.After(prevLast) {
				before.Values = append(before.Values, nil)
				continue
			}
			var pv dayVals
			if p := prev[schedule.FormatDate(pd)]; p != nil {
				pv = *p
			}
			x := met.pick(pv)
			lastTotal += x
			before.Values = append(before.Values, fptr(x))
		}
		chart.Series = []chartSeries{this, before}
	} else {
		// Twelve months ending with the chosen month, against the year before.
		periodFrom, periodTo = first.AddDate(0, -11, 0), last
		cur := s.byDateWolt(first.AddDate(0, -11, 0), last, now, mode)
		prev := s.byDateWolt(first.AddDate(0, -23, 0), first.AddDate(0, -11, -1), now, mode)
		thisName, lastName = st(lang, "Last 12 months"), st(lang, "Year before")
		this := chartSeries{Name: st(lang, "This year"), Kind: "bar", Color: colorThis}
		before := chartSeries{Name: st(lang, "Year before"), Kind: "line", Color: colorLast, Dashed: true}
		anyBefore := false
		sum := func(src map[string]*dayVals, m time.Time) dayVals {
			var t dayVals
			for d := m; d.Month() == m.Month(); d = d.AddDate(0, 0, 1) {
				if p := src[schedule.FormatDate(d)]; p != nil {
					t.Money += p.Money
					t.Takeaway += p.Takeaway
					t.Deposits += p.Deposits
					t.Tables += p.Tables
					t.Guests += p.Guests
					t.Orders += p.Orders
					t.Lost += p.Lost
					t.Wolt += p.Wolt
					t.WoltOrders += p.WoltOrders
					t.Till += p.Till
				}
			}
			return t
		}
		for i := 11; i >= 0; i-- {
			m := first.AddDate(0, -i, 0)
			chart.Labels = append(chart.Labels, capitalize(monthShortL(lang, m.Month())))
			chart.Long = append(chart.Long, monthYear(lang, m))
			if m.Year() == today.Year() && m.Month() == today.Month() {
				chart.Highlight = len(chart.Labels) - 1
			}
			v := sum(cur, m)
			x := met.pick(v)
			thisTotal += x
			if m.After(today) {
				this.Values = append(this.Values, nil)
			} else {
				this.Values = append(this.Values, fptr(x))
			}
			if met.Key == "money" {
				this.Extra = append(this.Extra, moneyExtra(lang, v, mode))
			}
			pv := sum(prev, m.AddDate(-1, 0, 0))
			px := met.pick(pv)
			lastTotal += px
			if px > 0 {
				anyBefore = true
			}
			before.Values = append(before.Values, fptr(px))
		}
		chart.Series = []chartSeries{this}
		if anyBefore {
			chart.Series = append(chart.Series, before)
		}
	}

	// The rest of the page covers the same period as the chart.
	periodBookings := s.Store.BookingsBetween(schedule.FormatDate(periodFrom), schedule.FormatDate(periodTo))
	stt := computeStats(periodBookings, now)
	s.addMoney(&stt, periodFrom, periodTo, mode)
	weekdays, hours := rushCharts(lang, periodBookings, now)
	dishes, drinks := bestSellers(s.Store.PaidBillsBetween(schedule.FormatDate(periodFrom), schedule.FormatDate(periodTo)),
		periodBookings, s.Store.Menu(false), now)

	outcomes := []outcomeRow{
		{"Arrived", "deposit taken off the bill", stt.Arrived, 0, "ok"},
		{"Confirmed, not here yet", "", stt.Upcoming, 0, "wait"},
		{"Takeaway orders", "", stt.TakeawayOrders, 0, "ok"},
		{"No-shows", "deposit kept", stt.NoShows, 0, "bad"},
		{"Cancelled after paying", "deposit kept", stt.TableCancelledKept + stt.TakeawayCancelled, 0, "bad"},
		{"Refunded", "deposit paid back", stt.Refunds, 0, "bad"},
		{"Never paid", "left before paying", stt.NeverPaid, 0, "muted"},
	}
	maxOut := 0
	for _, o := range outcomes {
		if o.Count > maxOut {
			maxOut = o.Count
		}
	}
	for i := range outcomes {
		if maxOut > 0 {
			outcomes[i].Pct = float64(outcomes[i].Count) / float64(maxOut) * 100
		}
	}

	fmtTotal := func(x float64) string {
		if met.Unit == "eur" {
			return euro(int(x*100 + 0.5))
		}
		return strconv.Itoa(int(x + 0.5))
	}
	periodLabel := monthYear(lang, first)
	if view == "months" {
		periodLabel = capitalize(monthShortL(lang, periodFrom.Month())) + " " + periodFrom.Format("2006") + " – " +
			capitalize(monthShortL(lang, periodTo.Month())) + " " + periodTo.Format("2006")
	}

	s.render(w, r, "owner_charts", 200, M{
		"Charts":      true,
		"Title":       st(lang, "Charts"),
		"Admin":       true,
		"Owner":       true,
		"Nav":         "charts",
		"View":        view,
		"Metric":      met,
		"Metrics":     metrics,
		"MonthKey":    month.Format("2006-01"),
		"PrevMonth":   first.AddDate(0, -1, 0).Format("2006-01"),
		"NextMonth":   first.AddDate(0, 1, 0).Format("2006-01"),
		"PrevYear":    first.AddDate(-1, 0, 0).Format("2006-01"),
		"NextYear":    first.AddDate(1, 0, 0).Format("2006-01"),
		"PeriodLabel": periodLabel,
		"Chart":       chart,
		"ThisName":    thisName,
		"LastName":    lastName,
		"ThisTotal":   fmtTotal(thisTotal),
		"LastTotal":   fmtTotal(lastTotal),
		"VsLast":      pctChange(int(thisTotal*100), int(lastTotal*100)),
		"HasLast":     lastTotal > 0,
		"S":           stt,
		"Weekdays":    weekdays,
		"Hours":       hours,
		"Dishes":      dishes,
		"Drinks":      drinks,
		"Removed":     s.removedReport(lang, schedule.FormatDate(periodFrom), schedule.FormatDate(periodTo)),
		"Outcomes":    outcomes,
		"WoltMode":    mode,
		"WoltQuery":   "view=" + view + "&month=" + month.Format("2006-01") + "&metric=" + met.Key,
	})
}

// rushCharts builds the busiest-days and busiest-hours charts from
// confirmed visits and pickups.
func rushCharts(lang string, bs []db.Booking, now time.Time) (chartData, chartData) {
	order := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
	wd := map[time.Weekday]int{}
	hr := map[int]int{}
	for _, b := range bs {
		st := b.EffectiveStatus(now)
		if b.PaidAt == nil || st == db.StatusCancelled || st == db.StatusRefunded {
			continue
		}
		start := b.Start()
		wd[start.Weekday()]++
		hr[start.Hour()]++
	}
	weekdays := chartData{Unit: "count", Highlight: -1, Emphasise: true}
	s := chartSeries{Name: st(lang, "Visits and pickups"), Kind: "bar", Color: colorThis}
	for _, d := range order {
		if _, open := schedule.Hours[d]; !open {
			continue
		}
		weekdays.Labels = append(weekdays.Labels, capitalize(weekdayShortWD(lang, d)))
		weekdays.Long = append(weekdays.Long, capitalize(weekdayLong(lang, d)))
		s.Values = append(s.Values, fptr(float64(wd[d])))
	}
	weekdays.Series = []chartSeries{s}

	hours := chartData{Unit: "count", Highlight: -1, Emphasise: true}
	s = chartSeries{Name: st(lang, "Visits and pickups"), Kind: "bar", Color: colorThis}
	for h := 10; h <= 21; h++ {
		hours.Labels = append(hours.Labels, fmt.Sprintf("%02d", h))
		hours.Long = append(hours.Long, fmt.Sprintf("%02d:00–%02d:59", h, h))
		s.Values = append(s.Values, fptr(float64(hr[h])))
	}
	hours.Series = []chartSeries{s}

	return weekdays, hours
}

// bestSellers ranks what sold, food and drinks apart, from the paid bills
// (waiters, QR and phone orders) and paid takeaway orders. Deal prices
// ("3 rätter") and other packages aren't dishes, so they're left out.
func bestSellers(bills []db.Bill, bs []db.Booking, menu []db.MenuItem, now time.Time) (dishes, drinks []dishRow) {
	byID := map[int]db.MenuItem{}
	for _, m := range menu {
		byID[m.ID] = m
	}
	type tally struct{ qty, rev int }
	food, drink := map[string]*tally{}, map[string]*tally{}
	add := func(isDrink bool, name string, qty, rev int) {
		if qty <= 0 || strings.TrimSpace(name) == "" {
			return
		}
		m := food
		if isDrink {
			m = drink
		}
		t := m[name]
		if t == nil {
			t = &tally{}
			m[name] = t
		}
		t.qty += qty
		t.rev += rev
	}
	for _, b := range bills {
		for _, l := range b.Lines {
			if m, ok := byID[l.MenuItemID]; ok && m.Package {
				continue
			}
			add(l.Drink, l.Name, l.Qty, l.Total())
		}
	}
	for _, b := range bs {
		st := b.EffectiveStatus(now)
		if b.PaidAt == nil || st == db.StatusCancelled || st == db.StatusRefunded {
			continue
		}
		for _, it := range b.Items {
			m, ok := byID[it.MenuItemID]
			if ok && m.Package {
				continue
			}
			add(ok && m.Drink, it.Name, it.Quantity, it.LineTotal())
		}
	}
	rank := func(m map[string]*tally) []dishRow {
		var out []dishRow
		for name, t := range m {
			out = append(out, dishRow{Name: name, Qty: t.qty, Revenue: t.rev})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Qty != out[j].Qty {
				return out[i].Qty > out[j].Qty
			}
			return out[i].Name < out[j].Name
		})
		if len(out) > 10 {
			out = out[:10]
		}
		for i := range out {
			out[i].Pct = float64(out[i].Qty) / float64(out[0].Qty) * 100
		}
		return out
	}
	return rank(food), rank(drink)
}

// ---------- Spreadsheet ----------

// ownerReportsCSV downloads every booking of a month as a spreadsheet,
// e.g. for the accountant.
func (s *Server) ownerReportsCSV(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	month, err := time.ParseInLocation("2006-01", r.URL.Query().Get("month"), schedule.Location)
	if err != nil {
		t := schedule.Today(now)
		month = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, schedule.Location)
	}
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, schedule.Location)
	last := first.AddDate(0, 1, -1)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="emeal-%s.csv"`, month.Format("2006-01")))
	w.Write([]byte("\xEF\xBB\xBF")) // so Excel reads å, ä, ö correctly
	cw := csv.NewWriter(w)
	cw.Comma = ';' // Excel in Finland/Sweden expects semicolons
	head := []string{"Booking", "Type", "Booked via", "Date", "Time", "Table", "Guests", "Name", "Phone", "Email", "Status", "Paid", "Deposit €", "Food total €", "Items", "Occasion", "Cancelled by"}
	for i := range head {
		head[i] = st(lang, head[i])
	}
	cw.Write(head)
	money := func(c int) string { return fmt.Sprintf("%d,%02d", c/100, c%100) }
	for _, b := range s.Store.BookingsBetween(schedule.FormatDate(first), schedule.FormatDate(last)) {
		items := ""
		for i, it := range b.Items {
			if i > 0 {
				items += ", "
			}
			items += fmt.Sprintf("%d × %s", it.Quantity, it.Name)
		}
		table, guests, food := "", "", ""
		if b.Kind == db.KindTable {
			table, guests = strconv.Itoa(b.TableNo), strconv.Itoa(b.PartySize)
		} else {
			food = money(b.TotalCents)
		}
		paid := st(lang, "no")
		if b.PaidAt != nil {
			paid = st(lang, "yes")
		}
		kind := st(lang, "table")
		if b.Kind == db.KindTakeaway {
			kind = st(lang, "takeaway")
		}
		via := st(lang, map[string]string{db.SourceOnline: "website", db.SourcePhone: "phone", db.SourceWalkIn: "walk-in"}[b.Source])
		by := ""
		if b.CancelledBy != "" {
			by = st(lang, b.CancelledBy)
		}
		cw.Write([]string{strconv.Itoa(b.ID), kind, via, b.Date, b.Time, table, guests, csvSafe(b.Name), csvPhone(b.Phone), csvSafe(b.Email),
			st(lang, statusText(b.EffectiveStatus(now))), paid, money(b.DepositCents), food, csvSafe(items), occasionText(lang, b.Occasion), by})
	}
	cw.Flush()
}

// csvSafe stops a guest's text from running as a formula when the file is
// opened in Excel. A guest could type a name like =HYPERLINK(...) or
// =cmd|... ; a cell starting with = + - @ (or a tab or line break) gets a '
// in front, so Excel shows it as plain text.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r\n", rune(v[0])) {
		return "'" + v
	}
	return v
}

// csvPhone leaves ordinary phone numbers (digits, spaces, + - and brackets)
// as they are, so "+358 ..." still reads normally; anything else goes
// through csvSafe.
func csvPhone(v string) string {
	for _, r := range v {
		if !(r >= '0' && r <= '9') && !strings.ContainsRune("+ -()", r) {
			return csvSafe(v)
		}
	}
	return v
}

// removedReport: what was taken off bills in the period, and the bills
// skipped and still unpaid now (see db/removals.go).
type removedRow struct {
	When, What, Kind, Why, AskedBy, By, Waiter string
	Table, Cents                               int
}

type removedSummary struct {
	Rows                              []removedRow
	Count, Items, Cents               int
	LineCents, BillCents, UnpaidCents int
	Skipped, SkippedCents             int // bills skipped and still open now
	UndoCents                         int // undone by the waiter right after adding
	ByWaiter                          []waiterRemoved
	Flags                             []waiterFlag // many in the last 7 days
}

// waiterFlag: a waiter with many removals or skipped bills in a week.
type waiterFlag struct {
	Name           string
	Removed, Skips int
}

// flagAfter: this many removals or skipped bills in 7 days marks a waiter.
const flagAfter = 3

type waiterRemoved struct {
	Name         string
	Count, Cents int
	Flag         bool // many in the last 7 days
}

func (s *Server) removedReport(lang, from, to string) removedSummary {
	var out removedSummary
	per := map[string]*waiterRemoved{}
	var order []string
	for _, r := range s.Store.Removals(from, to) {
		row := removedRow{When: shortDateL(lang, schedule.FormatDate(r.At)) + " " + clockOf(r.At), What: fmt.Sprintf("%d× %s", r.Qty, r.Name),
			Table: r.Table, Cents: r.Cents, Why: r.Why, AskedBy: whoL(lang, r.AskedBy), By: whoL(lang, r.By), Waiter: r.Waiter}
		switch r.Kind {
		case db.RemovedBill:
			row.Kind = st(lang, "Bill voided")
			out.BillCents += r.Cents
		case db.RemovedUnpaid:
			row.Kind = st(lang, "Never paid")
			out.UnpaidCents += r.Cents
		case db.RemovedUndo:
			row.Kind = st(lang, "Undone by the waiter")
			out.UndoCents += r.Cents
		default:
			row.Kind = st(lang, "Taken off")
			out.LineCents += r.Cents
		}
		out.Rows = append(out.Rows, row)
		out.Count++
		out.Items += r.Qty
		out.Cents += r.Cents
		who := r.Waiter
		if who == "" {
			who = r.AskedBy
		}
		if who == "" {
			who = st(lang, "No waiter")
		}
		w := per[who]
		if w == nil {
			w = &waiterRemoved{Name: who}
			per[who] = w
			order = append(order, who)
		}
		w.Count++
		w.Cents += r.Cents
	}
	for _, n := range order {
		out.ByWaiter = append(out.ByWaiter, *per[n])
	}
	sort.SliceStable(out.ByWaiter, func(i, j int) bool { return out.ByWaiter[i].Cents > out.ByWaiter[j].Cents })
	for _, b := range s.Store.SkippedBills() {
		out.Skipped++
		out.SkippedCents += b.ToPay()
	}
	out.Flags = s.waiterFlags(lang, s.Now())
	for i := range out.ByWaiter {
		for _, f := range out.Flags {
			if f.Name == out.ByWaiter[i].Name {
				out.ByWaiter[i].Flag = true
			}
		}
	}
	return out
}

// waiterFlags: waiters with flagAfter or more removals (not their own
// one-minute undos) or skipped bills in the last 7 days, most first.
func (s *Server) waiterFlags(lang string, now time.Time) []waiterFlag {
	today := schedule.Today(now)
	from := schedule.FormatDate(today.AddDate(0, 0, -6))
	per := map[string]*waiterFlag{}
	get := func(name string) *waiterFlag {
		if name == "" {
			name = st(lang, "No waiter")
		}
		f := per[name]
		if f == nil {
			f = &waiterFlag{Name: name}
			per[name] = f
		}
		return f
	}
	for _, r := range s.Store.Removals(from, schedule.FormatDate(today)) {
		if r.Kind == db.RemovedUndo {
			continue
		}
		who := r.Waiter
		if who == "" {
			who = r.AskedBy
		}
		get(whoL(lang, who)).Removed++
	}
	for _, b := range s.Store.SkippedBills() {
		if schedule.FormatDate(*b.SkippedAt) >= from {
			who := b.WaiterName
			if who == "" {
				who = b.SkippedBy
			}
			get(whoL(lang, who)).Skips++
		}
	}
	var out []waiterFlag
	for _, f := range per {
		if f.Removed+f.Skips >= flagAfter {
			out = append(out, *f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Removed+out[i].Skips > out[j].Removed+out[j].Skips })
	return out
}
