package handlers

import (
	"fmt"
	"strings"
	"time"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

// Guest emails are written in the language the guest booked in. Manager
// emails are always English. Every sentence lives in emailText, so the
// wording can be changed in one place.

var emailText = map[string]map[string]string{
	langEN: {
		"hi": "Hi %s,", "table_for": "Table for %s", "date": "Date", "time": "Time",
		"takeaway": "Takeaway order", "pickup": "Pickup", "total": "Total", "name": "Name",
		"phone": "Phone", "notes": "Notes", "ref": "Booking", "occasion": "Occasion",

		"policy_table":       "The deposit is taken off your bill. Free cancellation until %s. After that, and if you don't come, the deposit is kept.",
		"policy_table_nodep": "Can't come after all? Please cancel with the link below, so another guest can have the table.",
		"policy_ta":          "You pay the remaining %s when you pick up. The takeaway deposit is non-refundable.",

		"link_subject": "Pay your deposit to confirm your booking · %s",
		"link_intro":   "Thanks for booking with %s. Your booking isn't confirmed yet.\nPlease pay the deposit of %s within 30 minutes using this link:",
		"link_outro":   "If the deposit isn't paid in time, the booking is released automatically.",

		"booked_table": "Your table is booked", "booked_ta": "Your takeaway order is confirmed",
		"confirm_subject": "%s · %s",
		"got_deposit":     "We've received your deposit of %s.",
		"manage_table":    "See, change or cancel your booking:",
		"manage_ta":       "See your order:",
		"receipt":         "Stripe sends your payment receipt in a separate email.",
		"table_code":      "Your table code: %s\nAt the restaurant, scan the QR code on your table and type this code. Then you can order and pay from your phone.",

		"cancel_subject": "Your booking has been cancelled · %s",
		"cancel_intro":   "Your booking has been cancelled.",
		"refunded":       "Your deposit of %s has been refunded. It can take 5–10 days to show on your account.",
		"kept":           "As per our policy, the deposit of %s is not refunded.",
		"questions":      "Questions? Just reply or call us.",

		"changed_subject": "Your booking has been changed · %s",
		"changed_intro":   "Your booking has been changed. These are the new details:",

		"remind_subject":  "See you %s at %s · %s",
		"remind_intro":    "Just a reminder of your table booking:",
		"remind_cancel":   "Can't come after all? Please cancel here, so another guest can have the table:",
		"remind_kept":     "The %s deposit is taken off your bill when you come. It is kept for late cancellations and no-shows.",
		"remind_today":    "today",
		"remind_tomorrow": "tomorrow",

		"wl_subject": "You're on the waiting list · %s",
		"wl_intro":   "You're on the waiting list for %s, %s. We'll email you as soon as a table becomes free.",
		"wl_time":    "Preferred time: around %s",
		"wl_any":     "Preferred time: any time",
		"wl_remove":  "Don't need a table any more? Take yourself off the list here:",

		"wf_subject": "A table is free %s · %s",
		"wf_intro":   "Good news: a table just became free on %s.",
		"wf_times":   "Free times: %s",
		"wf_book":    "Book it here before someone else does:",
		"wf_note":    "The table goes to whoever books first.",
	},
	langSV: {
		"hi": "Hej %s,", "table_for": "Bord för %s", "date": "Datum", "time": "Tid",
		"takeaway": "Avhämtning", "pickup": "Hämtning", "total": "Totalt", "name": "Namn",
		"phone": "Telefon", "notes": "Önskemål", "ref": "Bokning", "occasion": "Tillfälle",

		"policy_table":       "Handpenningen dras av från notan. Gratis avbokning till %s. Efter det, och om ni inte kommer, behålls handpenningen.",
		"policy_table_nodep": "Kan ni inte komma ändå? Avboka gärna via länken nedan, så att en annan gäst kan få bordet.",
		"policy_ta":          "Resten, %s, betalar du när du hämtar. Handpenningen för avhämtning återbetalas inte.",

		"link_subject": "Betala handpenningen för att bekräfta din bokning · %s",
		"link_intro":   "Tack för din bokning hos %s. Bokningen är inte bekräftad ännu.\nBetala handpenningen på %s inom 30 minuter via den här länken:",
		"link_outro":   "Om handpenningen inte betalas i tid släpps bokningen automatiskt.",

		"booked_table": "Ditt bord är bokat", "booked_ta": "Din beställning är bekräftad",
		"confirm_subject": "%s · %s",
		"got_deposit":     "Vi har tagit emot din handpenning på %s.",
		"manage_table":    "Se, ändra eller avboka din bokning:",
		"manage_ta":       "Se din beställning:",
		"receipt":         "Kvittot på betalningen kommer i ett separat e-postmeddelande från Stripe.",
		"table_code":      "Din bordskod: %s\nPå restaurangen skannar du QR-koden på ert bord och skriver in koden. Sedan kan ni beställa och betala i mobilen.",

		"cancel_subject": "Din bokning har avbokats · %s",
		"cancel_intro":   "Din bokning har avbokats.",
		"refunded":       "Din handpenning på %s har återbetalats. Det kan ta 5–10 dagar innan den syns på ditt konto.",
		"kept":           "Enligt våra villkor återbetalas inte handpenningen på %s.",
		"questions":      "Frågor? Svara på det här meddelandet eller ring oss.",

		"changed_subject": "Din bokning har ändrats · %s",
		"changed_intro":   "Din bokning har ändrats. Här är de nya uppgifterna:",

		"remind_subject":  "Välkommen %s kl. %s · %s",
		"remind_intro":    "En påminnelse om din bordsbokning:",
		"remind_cancel":   "Kan ni inte komma ändå? Avboka här, så att en annan gäst kan få bordet:",
		"remind_kept":     "Handpenningen på %s dras av från notan när ni kommer. Vid sen avbokning eller om ni inte kommer behålls den.",
		"remind_today":    "i dag",
		"remind_tomorrow": "i morgon",

		"wl_subject": "Du står på väntelistan · %s",
		"wl_intro":   "Du står på väntelistan för %s, %s. Vi mejlar dig så fort ett bord blir ledigt.",
		"wl_time":    "Önskad tid: runt %s",
		"wl_any":     "Önskad tid: när som helst",
		"wl_remove":  "Behöver du inte bordet längre? Ta bort dig från listan här:",

		"wf_subject": "Ett bord är ledigt %s · %s",
		"wf_intro":   "Goda nyheter: ett bord blev just ledigt %s.",
		"wf_times":   "Lediga tider: %s",
		"wf_book":    "Boka här innan någon annan hinner före:",
		"wf_note":    "Bordet går till den som bokar först.",
	},
}

// bookingLang is the language of a booking's emails (old bookings: Swedish).
func bookingLang(b db.Booking) string {
	if validLang(b.Lang) {
		return b.Lang
	}
	return defaultLang
}

// mailBody joins paragraphs with blank lines, skipping empty ones.
func mailBody(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, "\n\n") + "\n"
}

// details is the booking summary used in every email.
func details(lang string, b db.Booking) string {
	e := emailText[lang]
	var sb strings.Builder
	if b.Kind == db.KindTable {
		fmt.Fprintf(&sb, e["table_for"]+"\n", guestsText(lang, b.PartySize))
		fmt.Fprintf(&sb, "%s: %s\n", e["date"], capitalize(longDate(lang, b.Date)))
		fmt.Fprintf(&sb, "%s: %s–%s\n", e["time"], b.Time, b.EndClock())
		if o := occasionText(lang, b.Occasion); o != "" {
			fmt.Fprintf(&sb, "%s: %s\n", e["occasion"], o)
		}
	} else {
		fmt.Fprintf(&sb, "%s\n", e["takeaway"])
		fmt.Fprintf(&sb, "%s: %s %s %s\n\n", e["pickup"], capitalize(longDate(lang, b.Date)), tr(lang)["at"], b.Time)
		for _, it := range b.Items {
			fmt.Fprintf(&sb, "  %d × %s  %s\n", it.Quantity, it.Name, euro(it.LineTotal()))
		}
		fmt.Fprintf(&sb, "\n%s: %s\n", e["total"], euro(b.TotalCents))
	}
	fmt.Fprintf(&sb, "%s: %s\n", e["name"], b.Name)
	if b.Phone != "" {
		fmt.Fprintf(&sb, "%s: %s\n", e["phone"], b.Phone)
	}
	if b.Notes != "" {
		fmt.Fprintf(&sb, "%s: %s\n", e["notes"], b.Notes)
	}
	fmt.Fprintf(&sb, "%s: #%d\n", e["ref"], b.ID)
	return sb.String()
}

func signature(cfg Config) string {
	s := cfg.RestaurantName
	if cfg.RestaurantPhone != "" {
		s += "\n" + cfg.RestaurantPhone
	}
	if cfg.RestaurantEmail != "" {
		s += "\n" + cfg.RestaurantEmail
	}
	return s
}

// freeCancelUntil: "fredag 23 oktober kl. 18:00" (24 hours before the start).
func freeCancelUntil(lang string, b db.Booking) string {
	until := b.Start().Add(-schedule.RefundWindow).In(schedule.Location)
	return longDateT(lang, until) + " " + tr(lang)["at"] + " " + until.Format("15:04")
}

func policy(lang string, b db.Booking) string {
	e := emailText[lang]
	switch {
	case b.Kind == db.KindTakeaway:
		return fmt.Sprintf(e["policy_ta"], euro(b.RemainingCents()))
	case b.DepositCents == 0:
		return e["policy_table_nodep"]
	}
	return fmt.Sprintf(e["policy_table"], freeCancelUntil(lang, b))
}

func bookingLink(cfg Config, b db.Booking, lang string) string {
	return cfg.BaseURL + "/b/" + b.Ref + "?lang=" + lang
}

// paymentLinkEmail is sent right after the guest fills in the form.
func paymentLinkEmail(cfg Config, b db.Booking, payURL string) (string, string) {
	lang := bookingLang(b)
	e := emailText[lang]
	subject := fmt.Sprintf(e["link_subject"], cfg.RestaurantName)
	body := mailBody(
		fmt.Sprintf(e["hi"], b.Name),
		fmt.Sprintf(e["link_intro"], cfg.RestaurantName, euro(b.DepositCents)),
		payURL,
		details(lang, b),
		policy(lang, b)+"\n"+e["link_outro"],
		signature(cfg),
	)
	return subject, body
}

// confirmationEmail is sent once the deposit is paid (or when staff book by phone).
func confirmationEmail(cfg Config, b db.Booking) (string, string) {
	lang := bookingLang(b)
	e := emailText[lang]
	what, manage := e["booked_table"], e["manage_table"]
	if b.Kind == db.KindTakeaway {
		what, manage = e["booked_ta"], e["manage_ta"]
	}
	paid, receipt := "", ""
	if b.DepositCents > 0 {
		paid = " " + fmt.Sprintf(e["got_deposit"], euro(b.DepositCents))
		receipt = e["receipt"]
	}
	code := ""
	if b.Kind == db.KindTable && b.TableCode != "" && b.Source != db.SourceWalkIn {
		code = fmt.Sprintf(e["table_code"], b.TableCode)
	}
	subject := fmt.Sprintf(e["confirm_subject"], what, cfg.RestaurantName)
	body := mailBody(
		fmt.Sprintf(e["hi"], b.Name),
		what+"."+paid,
		details(lang, b),
		code,
		policy(lang, b),
		manage+"\n"+bookingLink(cfg, b, lang),
		receipt,
		signature(cfg),
	)
	return subject, body
}

// changedEmail confirms a booking the guest moved to another time.
func changedEmail(cfg Config, b db.Booking) (string, string) {
	lang := bookingLang(b)
	e := emailText[lang]
	subject := fmt.Sprintf(e["changed_subject"], cfg.RestaurantName)
	body := mailBody(
		fmt.Sprintf(e["hi"], b.Name),
		e["changed_intro"],
		details(lang, b),
		policy(lang, b),
		e["manage_table"]+"\n"+bookingLink(cfg, b, lang),
		signature(cfg),
	)
	return subject, body
}

// reminderEmail is sent the day before (or the same day) a table booking.
func reminderEmail(cfg Config, b db.Booking, now time.Time) (string, string) {
	lang := bookingLang(b)
	e := emailText[lang]
	when := shortDateL(lang, b.Date)
	switch b.Date {
	case schedule.FormatDate(schedule.Today(now)):
		when = e["remind_today"]
	case schedule.FormatDate(schedule.Today(now).AddDate(0, 0, 1)):
		when = e["remind_tomorrow"]
	}
	kept := ""
	if b.DepositCents > 0 {
		kept = fmt.Sprintf(e["remind_kept"], euro(b.DepositCents))
	}
	subject := fmt.Sprintf(e["remind_subject"], when, b.Time, cfg.RestaurantName)
	body := mailBody(
		fmt.Sprintf(e["hi"], b.Name),
		e["remind_intro"],
		details(lang, b),
		kept,
		e["remind_cancel"]+"\n"+bookingLink(cfg, b, lang),
		signature(cfg),
	)
	return subject, body
}

// cancellationEmail tells the guest their booking was cancelled.
func cancellationEmail(cfg Config, b db.Booking, refunded bool) (string, string) {
	lang := bookingLang(b)
	e := emailText[lang]
	money := ""
	switch {
	case b.DepositCents == 0:
	case refunded:
		money = fmt.Sprintf(e["refunded"], euro(b.DepositCents))
	default:
		money = fmt.Sprintf(e["kept"], euro(b.DepositCents))
	}
	subject := fmt.Sprintf(e["cancel_subject"], cfg.RestaurantName)
	body := mailBody(
		fmt.Sprintf(e["hi"], b.Name),
		e["cancel_intro"],
		details(lang, b),
		money,
		e["questions"],
		signature(cfg),
	)
	return subject, body
}

// waitJoinedEmail confirms a place on the waiting list.
func waitJoinedEmail(cfg Config, w db.WaitEntry) (string, string) {
	lang := validLangOr(w.Lang)
	e := emailText[lang]
	pref := e["wl_any"]
	if w.Time != "" {
		pref = fmt.Sprintf(e["wl_time"], w.Time)
	}
	subject := fmt.Sprintf(e["wl_subject"], cfg.RestaurantName)
	body := mailBody(
		fmt.Sprintf(e["hi"], w.Name),
		fmt.Sprintf(e["wl_intro"], longDate(lang, w.Date), guestsText(lang, w.PartySize))+"\n"+pref,
		e["wl_remove"]+"\n"+cfg.BaseURL+"/w/"+w.Ref+"?lang="+lang,
		signature(cfg),
	)
	return subject, body
}

// waitFreeEmail tells a waiting guest that a table became free.
func waitFreeEmail(cfg Config, w db.WaitEntry, times []string) (string, string) {
	lang := validLangOr(w.Lang)
	e := emailText[lang]
	if len(times) > 8 {
		times = times[:8]
	}
	link := fmt.Sprintf("%s/book?date=%s&size=%d&lang=%s", cfg.BaseURL, w.Date, w.PartySize, lang)
	subject := fmt.Sprintf(e["wf_subject"], shortDateL(lang, w.Date), cfg.RestaurantName)
	body := mailBody(
		fmt.Sprintf(e["hi"], w.Name),
		fmt.Sprintf(e["wf_intro"], longDate(lang, w.Date))+"\n"+fmt.Sprintf(e["wf_times"], strings.Join(times, ", ")),
		e["wf_book"]+"\n"+link,
		e["wf_note"],
		e["wl_remove"]+"\n"+cfg.BaseURL+"/w/"+w.Ref+"?lang="+lang,
		signature(cfg),
	)
	return subject, body
}

func validLangOr(l string) string {
	if validLang(l) {
		return l
	}
	return defaultLang
}

// ---------- To the manager (English) ----------

func managerKind(b db.Booking) string {
	kind := fmt.Sprintf("Table for %d", b.PartySize)
	if b.Kind == db.KindTakeaway {
		kind = "Takeaway order"
	}
	if b.Kind == db.KindTable && b.TableNo > 0 {
		kind += fmt.Sprintf(" · table %d", b.TableNo)
	}
	return kind
}

// managerEmail tells the manager about a new confirmed booking.
func managerEmail(cfg Config, b db.Booking) (string, string) {
	subject := fmt.Sprintf("New booking: %s · %s %s", managerKind(b), shortDate(b.Date), b.Time)
	body := mailBody(
		fmt.Sprintf("New confirmed booking (deposit %s paid):", euro(b.DepositCents)),
		details(langEN, b)+"Email: "+b.Email+"\nGuest's language: "+bookingLang(b),
		fmt.Sprintf("Dashboard: %s/admin/day?date=%s", cfg.BaseURL, b.Date),
	)
	return subject, body
}

// managerCancelEmail tells the manager a guest cancelled online.
func managerCancelEmail(cfg Config, b db.Booking, refunded bool) (string, string) {
	money := "No deposit was paid."
	switch {
	case b.DepositCents == 0:
	case refunded:
		money = fmt.Sprintf("The %s deposit was refunded automatically (cancelled more than 24 hours before).", euro(b.DepositCents))
	case b.Kind == db.KindTakeaway:
		money = fmt.Sprintf("The %s deposit is kept (takeaway deposits are non-refundable).", euro(b.DepositCents))
	default:
		money = fmt.Sprintf("The %s deposit is kept (cancelled less than 24 hours before).", euro(b.DepositCents))
	}
	subject := fmt.Sprintf("Cancelled by guest: %s · %s %s", managerKind(b), shortDate(b.Date), b.Time)
	body := mailBody(
		"A guest cancelled their booking online. The table is free again.",
		details(langEN, b),
		money,
		fmt.Sprintf("Dashboard: %s/admin/day?date=%s", cfg.BaseURL, b.Date),
	)
	return subject, body
}

// managerChangeEmail tells the manager a guest moved their booking.
func managerChangeEmail(cfg Config, before, after db.Booking) (string, string) {
	subject := fmt.Sprintf("Booking changed by guest: %s · %s %s → %s %s", after.Name,
		shortDate(before.Date), before.Time, shortDate(after.Date), after.Time)
	body := mailBody(
		fmt.Sprintf("Was: %s, %s at %s.", plural(before.PartySize, "guest", "guests"), prettyDate(before.Date), before.Time),
		"Now:\n"+details(langEN, after),
		fmt.Sprintf("Dashboard: %s/admin/day?date=%s", cfg.BaseURL, after.Date),
	)
	return subject, body
}
