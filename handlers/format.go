package handlers

import (
	"fmt"
	"html/template"
	"math"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

// funcs are helpers that templates can call, e.g. {{euro .DepositCents}}.
var funcs = template.FuncMap{
	"euro":         euro,
	"prettyDate":   prettyDate, // English, for the manager pages
	"shortDate":    shortDate,  // English, for the manager pages
	"date":         longDate,   // {{date $.Lang .Date}}: localized for guests
	"dshort":       shortDateL,
	"cap":          capitalize,
	"guests":       guestsText,
	"endTime":      schedule.EndTime,
	"tableDeposit": schedule.TableDeposit,
	"cell":         chartCell,
	"add":          func(a, b int) int { return a + b },
	"vatRate":      vatRate,
	"shiftDay":     func(t time.Time) string { return schedule.FormatDate(t) },
	"tillKeys":     func() []string { return []string{"7", "8", "9", "4", "5", "6", "1", "2", "3", "C", "0", "⌫"} },
	"lunchCards":   func() []string { return []string{db.PaidEpassi, db.PaidSmartum, db.PaidEdenred} },
	"usesRows":     usesRows,
	// discountSteps are the "Rabatt" choices on a bill.
	"discountSteps": func() []int { return discountSteps },
	"validDiscount": func(p int) bool {
		for _, x := range discountSteps {
			if x == p {
				return true
			}
		}
		return false
	},
	"cents": centsText,
	// seq is a..b, e.g. the guest counts in "Split between".
	"seq": func(a, b int) []int {
		var out []int
		for i := a; i <= b; i++ {
			out = append(out, i)
		}
		return out
	},
	"div": func(a, b int) int {
		if b == 0 {
			return 0
		}
		return a / b
	},
	// divr divides and rounds to the nearest whole number (averages in cents).
	"divr": func(a, b int) int {
		if b == 0 {
			return 0
		}
		return (a + b/2) / b
	},
	"statusText": statusText,
	"occasion":   func(o string) string { return occasionText(langEN, o) },
}

// funcsFor is funcs for one language: dates, statuses and the staff words
// ({{tr "..."}}, {{trf "%d guests" .N}}) come out in that language.
func funcsFor(lang string) template.FuncMap {
	f := template.FuncMap{}
	for k, v := range funcs {
		f[k] = v
	}
	f["tr"] = func(s string) string { return st(lang, s) }
	f["trf"] = func(s string, a ...any) string { return stf(lang, s, a...) }
	f["prettyDate"] = func(d string) string { return longDate(lang, d) }
	f["shortDate"] = func(d string) string { return shortDateL(lang, d) }
	f["statusText"] = func(x db.Status) string { return st(lang, statusText(x)) }
	f["occasion"] = func(o string) string { return occasionText(lang, o) }
	f["guestList"] = func(xs []int) string { return guestList(lang, xs) }
	f["allergens"] = func(codes []string) string { return allergenList(lang, codes) }
	f["allergenChoices"] = func(have []string) []allergenChoice { return allergenChoices(lang, have) }
	f["roomOf"] = roomIDOfTable
	f["method"] = func(m string) string { return methodName(lang, m) }
	// tableLabel: 5 -> "Table 5 · Utrymme 1 · 4 seats", for the table pickers.
	f["tableLabel"] = func(n int) string {
		t, ok := schedule.TableInfo(n)
		if !ok {
			return stf(lang, "Table %d", n)
		}
		return stf(lang, "Table %d", n) + " · " + t.Room + " · " + stf(lang, "%d seats", t.Seats)
	}
	return f
}

// guestList: [4] -> "guest 4", [3 4 5] -> "guests 3, 4 and 5" ("gäst 4", "gäster 3, 4 och 5").
func guestList(lang string, xs []int) string {
	one, many, and := "guest ", "guests ", " and "
	if lang == langSV {
		one, many, and = "gäst ", "gäster ", " och "
	}
	if len(xs) == 1 {
		return one + strconv.Itoa(xs[0])
	}
	out := many
	for i, x := range xs {
		switch {
		case i == 0:
		case i == len(xs)-1:
			out += and
		default:
			out += ", "
		}
		out += strconv.Itoa(x)
	}
	return out
}

// euro formats cents the Finnish way: 1450 -> "14,50 €".
func euro(cents int) string {
	sign := ""
	if cents < 0 {
		sign, cents = "−", -cents
	}
	return fmt.Sprintf("%s%s,%02d €", sign, thousands(cents/100), cents%100)
}

// thousands groups digits with a space, the Nordic way: 5648 -> "5 648".
// The space is a non-breaking one, so an amount never splits over two lines.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "\u00a0" + s[i:]
	}
	return s
}

// prettyDate: "2026-10-24" -> "Saturday 24 October".
func prettyDate(date string) string { return longDate(langEN, date) }

// shortDate: "2026-10-24" -> "Sat 24 Oct".
func shortDate(date string) string { return shortDateL(langEN, date) }

// guestsText: "1 guest" / "4 personer".
func guestsText(lang string, n int) string {
	t := tr(lang)
	if n == 1 {
		return t["guests_one"]
	}
	return fmt.Sprintf(t["guests_many"], n)
}

func statusText(s db.Status) string {
	switch s {
	case db.StatusPaid:
		return "Confirmed"
	case db.StatusPending:
		return "Awaiting payment"
	case db.StatusExpired:
		return "Expired"
	case db.StatusCancelled:
		return "Cancelled · deposit kept"
	case db.StatusRefunded:
		return "Cancelled · refunded"
	case db.StatusArrived:
		return "Arrived"
	case db.StatusNoShow:
		return "No-show · deposit kept"
	case db.StatusPickedUp:
		return "Picked up"
	}
	return string(s)
}

// parseEuro turns "14,50", "14.50" or "14,50 €" into 1450 cents.
func parseEuro(s string) (int, error) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "€"))
	s = strings.ReplaceAll(s, ",", ".")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 10000 {
		return 0, fmt.Errorf("invalid price")
	}
	return int(math.Round(f * 100)), nil
}

// hoursRow is one line of the opening-hours table.
type hoursRow struct{ Days, Hours, Kitchen string }

// hoursRows groups days with the same hours: "Måndag–torsdag 10:30–21:00".
func hoursRows(lang string) []hoursRow {
	t := tr(lang)
	week := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
	var rows []hoursRow
	for i := 0; i < len(week); {
		h, open := schedule.Hours[week[i]]
		j := i
		for j+1 < len(week) {
			h2, open2 := schedule.Hours[week[j+1]]
			if open2 != open || h2 != h {
				break
			}
			j++
		}
		days := weekdayLong(lang, week[i])
		if j > i {
			days += "–" + weekdayLong(lang, week[j])
		}
		days = capitalize(days)
		if open {
			rows = append(rows, hoursRow{days, schedule.Clock(h.Open) + "–" + schedule.Clock(h.Close), fmt.Sprintf(t["kitchen_until"], schedule.Clock(h.KitchenClose))})
		} else {
			rows = append(rows, hoursRow{Days: days, Hours: t["closed"]})
		}
		i = j + 1
	}
	return rows
}

// ---------- Time groups ----------

// period is a labelled group of times: Lunch, Afternoon or Evening.
type period struct {
	Label string
	Slots []slotView
}

type slotView struct {
	Time string
	Free int
	Full bool
}

// periodOf decides which group a start time belongs to. Lunch only exists on
// weekdays (10:30–14:30); evening starts at 17:00.
func periodOf(date time.Time, clock string) string {
	m, _ := schedule.ParseClock(clock)
	weekday := date.Weekday() >= time.Monday && date.Weekday() <= time.Friday
	switch {
	case weekday && m < 14*60+30:
		return "period_lunch"
	case m < 17*60:
		return "period_afternoon"
	}
	return "period_evening"
}

// groupSlots splits times into Lunch / Afternoon / Evening, keeping order.
func groupSlots(lang string, date time.Time, slots []slotView) []period {
	t := tr(lang)
	var out []period
	for _, s := range slots {
		label := t[periodOf(date, s.Time)]
		if len(out) == 0 || out[len(out)-1].Label != label {
			out = append(out, period{Label: label})
		}
		out[len(out)-1].Slots = append(out[len(out)-1].Slots, s)
	}
	return out
}

// ---------- Form checking ----------

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ") // also removes newlines
}

// clip shortens s to at most n letters.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// contact holds the cleaned-up guest details.
type contact struct{ Name, Email, Phone, Notes string }

// checkContact validates name, email, phone and notes, filling errs with
// messages in the guest's language.
func checkContact(form map[string]string, errs map[string]string, t map[string]string) contact {
	c := contact{
		Name:  oneLine(form["name"]),
		Email: strings.TrimSpace(form["email"]),
		Phone: oneLine(form["phone"]),
		Notes: strings.TrimSpace(form["notes"]),
	}
	if c.Name == "" || len([]rune(c.Name)) > 80 {
		errs["name"] = t["err_name"]
	}
	if addr, err := mail.ParseAddress(c.Email); err != nil || addr.Address != c.Email || !strings.Contains(c.Email, ".") {
		errs["email"] = t["err_email"]
	}
	digits := 0
	for _, r := range c.Phone {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case strings.ContainsRune("+ -()", r):
		default:
			digits = -100
		}
	}
	if digits < 6 || digits > 15 {
		errs["phone"] = t["err_phone"]
	}
	if len([]rune(c.Notes)) > 500 {
		errs["notes"] = t["err_notes"]
	}
	return c
}

// formValues copies the posted fields into a simple map for re-showing the form.
func formValues(r interface{ FormValue(string) string }, keys ...string) map[string]string {
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		m[k] = r.FormValue(k)
	}
	return m
}

// chartCell prints one value of a chart for its table view.
func chartCell(c chartData, series, i int) string {
	if series >= len(c.Series) || i >= len(c.Series[series].Values) || c.Series[series].Values[i] == nil {
		return "–"
	}
	v := *c.Series[series].Values[i]
	if c.Unit == "eur" {
		return euro(int(v*100 + 0.5))
	}
	return fmt.Sprintf("%.0f", v)
}

// plural: plural(1, "guest", "guests") = "1 guest", plural(3, ...) = "3 guests".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// vatRate: 2550 -> "25,50" (as on the old till's receipts).
func vatRate(r int) string { return centsText(r) }

// centsText: 6693 -> "66,93" (an amount without the € sign, for receipt tables).
func centsText(c int) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s%d,%02d", sign, c/100, c%100)
}

// discountSteps are the discounts staff can give (percent).
var discountSteps = []int{0, 10, 15, 20, 25, 30, 50, 100}

// useRow is one "uses from stock" row in a form: which ingredient, how much.
type useRow struct {
	Options []useOption
	Amount  int
}

type useOption struct {
	ID       int
	Name     string
	Unit     string
	Selected bool
}

// usesForm is the "uses from stock" rows of one form; Prefix names its
// fields (prefix_ing, prefix_amt).
type usesForm struct {
	Prefix string
	Rows   []useRow
	None   bool // no ingredients in stock yet
}

// usesRows: a dish's ingredients as form rows, plus empty rows to add more
// (at least extra of them, and never fewer than 3 rows in all).
func usesRows(prefix string, uses []db.IngredientUse, ings []db.Ingredient, extra int) usesForm {
	return usesForm{Prefix: prefix, Rows: useRowsFor(uses, ings, extra), None: len(ings) == 0}
}

func useRowsFor(uses []db.IngredientUse, ings []db.Ingredient, extra int) []useRow {
	row := func(u db.IngredientUse) useRow {
		r := useRow{Amount: u.Amount}
		for _, i := range ings {
			r.Options = append(r.Options, useOption{ID: i.ID, Name: i.Name, Unit: i.Unit, Selected: i.ID == u.ID})
		}
		return r
	}
	var out []useRow
	for _, u := range uses {
		out = append(out, row(u))
	}
	for n := 0; n < extra || len(out) < 3; n++ {
		out = append(out, row(db.IngredientUse{}))
	}
	return out
}
