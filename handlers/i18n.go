package handlers

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"ozmo-booking/schedule"
)

// Guest pages and guest emails come in Swedish and English.
// The manager dashboard stays in English.
//
// How the language is picked:
//  1. ?lang=sv or ?lang=en in the address (the switch in the header), saved in a cookie
//  2. the cookie from an earlier visit
//  3. the browser's language: English browsers get English, everyone else Swedish
const (
	langSV      = "sv"
	langEN      = "en"
	langCookie  = "lang"
	defaultLang = langSV
)

func validLang(l string) bool { return l == langSV || l == langEN }

// langOf finds the language for a request (without changing anything).
func langOf(r *http.Request) string {
	if l := r.URL.Query().Get("lang"); validLang(l) {
		return l
	}
	if c, err := r.Cookie(langCookie); err == nil && validLang(c.Value) {
		return c.Value
	}
	al := strings.ToLower(r.Header.Get("Accept-Language"))
	if strings.HasPrefix(al, "en") {
		return langEN
	}
	return defaultLang
}

// rememberLang saves ?lang=... in a cookie for a year.
func rememberLang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l := r.URL.Query().Get("lang"); validLang(l) {
			http.SetCookie(w, &http.Cookie{Name: langCookie, Value: l, Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode, HttpOnly: true})
		}
		next.ServeHTTP(w, r)
	})
}

// switchURL is the current page in another language.
func switchURL(r *http.Request, lang string) string {
	q := r.URL.Query()
	q.Set("lang", lang)
	u := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
	return u.String()
}

// tr returns the words for a language.
func tr(lang string) map[string]string {
	if lang == langEN {
		return dictEN
	}
	return dictSV
}

// ---------- Dates ----------

var (
	weekdaysSV  = []string{"söndag", "måndag", "tisdag", "onsdag", "torsdag", "fredag", "lördag"}
	monthsSV    = []string{"januari", "februari", "mars", "april", "maj", "juni", "juli", "augusti", "september", "oktober", "november", "december"}
	weekdaysSVs = []string{"sön", "mån", "tis", "ons", "tor", "fre", "lör"}
	monthsSVs   = []string{"jan", "feb", "mar", "apr", "maj", "jun", "jul", "aug", "sep", "okt", "nov", "dec"}
)

// longDate: "2026-10-26" -> "måndag 26 oktober" / "Monday 26 October".
func longDate(lang, date string) string {
	d, err := schedule.ParseDate(date)
	if err != nil {
		return date
	}
	return longDateT(lang, d)
}

func longDateT(lang string, d time.Time) string {
	if lang == langEN {
		return d.Format("Monday 2 January")
	}
	return weekdaysSV[d.Weekday()] + " " + d.Format("2") + " " + monthsSV[d.Month()-1]
}

// shortDateL: "mån 26 okt" / "Mon 26 Oct".
func shortDateL(lang, date string) string {
	d, err := schedule.ParseDate(date)
	if err != nil {
		return date
	}
	if lang == langEN {
		return d.Format("Mon 2 Jan")
	}
	return weekdaysSVs[d.Weekday()] + " " + d.Format("2") + " " + monthsSVs[d.Month()-1]
}

func weekdayShort(lang string, d time.Time) string {
	if lang == langEN {
		return d.Format("Mon")
	}
	return weekdaysSVs[d.Weekday()]
}

func monthShort(lang string, d time.Time) string {
	if lang == langEN {
		return d.Format("Jan")
	}
	return monthsSVs[d.Month()-1]
}

func weekdayLong(lang string, wd time.Weekday) string {
	if lang == langEN {
		return wd.String()
	}
	return weekdaysSV[wd]
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// ---------- Words ----------

var dictEN = map[string]string{
	// layout
	"demo_banner": "Demo mode: no real payments are taken.",
	"home_link":   "home",
	"lang_label":  "Language",

	// home
	"home_title":    "Book a table or order takeaway",
	"home_lead":     "Reserve online in a minute. A small deposit secures your booking and is taken off your bill.",
	"cta_book":      "Book a table",
	"cta_takeaway":  "Order takeaway",
	"hours_title":   "Opening hours",
	"kitchen_until": "kitchen until %s",
	"closed":        "Closed",
	"lunch_note":    "Lunch is served 10:30–14:30 on weekdays. After the kitchen closes we serve drinks.",
	"dep_title":     "Table deposit",
	"dep_text":      "10 € for 1–2 guests, 20 € for 3–6 guests, taken off your bill.",
	"dep_note":      "Free cancellation up to 24 hours before. Groups larger than 6, please call us.",
	"ta_title":      "Takeaway",
	"ta_text":       "Pay 25 % when you order and the rest when you pick up.",
	"ta_note":       "Takeaway deposits are non-refundable.",

	// booking
	"book_title":       "Book a table",
	"step_guests":      "How many guests?",
	"step_date":        "Which day?",
	"step_time":        "What time?",
	"step_details":     "Your details",
	"big_group":        "More than 6 guests? Please call us.",
	"big_group_phone":  "More than 6 guests? Call us on",
	"other_date":       "Another date",
	"today":            "Today",
	"tomorrow":         "Tomorrow",
	"day_full":         "Full",
	"day_closed":       "Closed",
	"period_lunch":     "Lunch",
	"period_afternoon": "Afternoon",
	"period_evening":   "Evening",
	"left":             "%d left",
	"full":             "Full",
	"table_2h":         "The table is yours for 2 hours.",
	"f_name":           "Name",
	"f_phone":          "Phone",
	"f_email":          "Email",
	"f_notes_table":    "Anything we should know?",
	"f_notes_ta":       "Notes",
	"optional":         "(optional)",
	"notes_ph_table":   "Allergies, high chair, birthday",
	"deposit":          "Deposit",
	"deposit_note_tbl": "Taken off your bill. Refunded if you cancel at least 24 hours before.",
	"pay_deposit":      "Pay deposit",
	"guests_one":       "1 guest",
	"guests_many":      "%d guests",

	"msg_invalid_date":  "Please pick a valid date.",
	"msg_past":          "That date has already passed. Please pick another day.",
	"msg_too_far":       "Bookings are open for the next %d days. Please pick an earlier day.",
	"msg_closed_on":     "We're closed on %s. Please pick another day.",
	"msg_no_more_times": "There are no more times to book on %s. Please pick another day.",
	"msg_fully_booked":  "We're fully booked on %s. Please pick another day.",
	"err_name":          "Please enter your name.",
	"err_email":         "Please enter a valid email address, like name@example.com.",
	"err_phone":         "Please enter a phone number so we can reach you.",
	"err_notes":         "Please keep notes under 500 characters.",
	"err_size":          "Please choose 1 to 6 guests.",
	"err_time":          "Please pick a time.",
	"err_taken":         "Sorry, that time was just taken. Please pick another.",
	"err_duplicate":     "You already have a booking at %s that day. To change it, please call us.",
	"err_items":         "Please choose at least one dish.",
	"err_items_max":     "For more than 20 of one dish, please call us.",
	"err_pickup":        "Please choose a pickup day and time.",
	"err_summary":       "Please check the highlighted fields.",

	// takeaway
	"ta_page_title": "Order takeaway",
	"ta_lead":       "Pay 25 % now and the rest when you pick up.",
	"ta_step_food":  "Choose your food",
	"ta_step_when":  "When will you pick up?",
	"legend":        "L lactose-free · G gluten-free · V vegetarian · M dairy-free · FI Finnish meat · AX from Åland · * can be made that way, mention it in the notes",
	"notes_ph_ta":   "Allergies, fries or sweet-potato fries, chocolate or kinuski sauce",
	"food_total":    "Food total",
	"deposit_now":   "Deposit now (25 %)",
	"pay_at_pickup": "Pay at pickup",
	"ta_nonrefund":  "The deposit is non-refundable.",
	"cart_one":      "1 dish",
	"cart_many":     "%d dishes",
	"continue":      "Continue",
	"no_days":       "Takeaway can't be ordered for the coming days. Please call us.",
	"less":          "One less",
	"more":          "One more",
	"how_many":      "How many",
	"categories":    "Menu sections",
	"pickup_at":     "Pickup",

	// status page
	"st_title":          "Your booking",
	"st_booked_table":   "Your table is booked",
	"st_code":           "Your table code",
	"st_code_x":         "At the restaurant, scan the QR code on your table and type this code. Then you can order and pay from your phone.",
	"st_booked_ta":      "Your order is confirmed",
	"st_sent_to":        "We've sent a confirmation to %s.",
	"st_pending_h":      "Almost done: pay the deposit",
	"st_hold_table":     "We're holding your table until %s. It's confirmed once the deposit is paid.",
	"st_hold_ta":        "Your order is saved until %s. It's confirmed once the deposit is paid.",
	"st_pay":            "Pay %s",
	"st_just_paid":      "Just paid? It can take a few seconds.",
	"st_refresh":        "Refresh this page",
	"st_expired_h":      "This booking expired",
	"st_expired_table":  "The deposit wasn't paid in time, so the table was released. Nothing was charged.",
	"st_expired_ta":     "The deposit wasn't paid in time, so the order was cancelled. Nothing was charged.",
	"st_start_again":    "Start again",
	"st_cancelled_h":    "Booking cancelled",
	"st_refunded_text":  "Your deposit of %s has been refunded.",
	"st_cancelled_text": "This booking was cancelled. The deposit was not refunded.",
	"lbl_booking":       "Booking",
	"lbl_date":          "Date",
	"lbl_time":          "Time",
	"lbl_guests":        "Guests",
	"lbl_pickup":        "Pickup",
	"lbl_order":         "Order",
	"lbl_total":         "Total",
	"lbl_deposit":       "Deposit",
	"lbl_pay_pickup":    "Pay at pickup",
	"lbl_name":          "Name",
	"lbl_phone":         "Phone",
	"lbl_notes":         "Notes",
	"at":                "at",
	"st_policy_table":   "The deposit is taken off your bill. Free cancellation until %s, after that the deposit is kept.",
	"st_policy_ta":      "The takeaway deposit is non-refundable.",
	"st_contact":        "To cancel or change, contact us:",

	// demo payment
	"dp_title": "Demo payment",
	"dp_lead":  "Stripe isn't set up yet, so this page stands in for the payment page. Paying here confirms the booking and sends the emails, just like a real payment.",
	"dp_pay":   "Pay %s (demo)",
	"dp_back":  "Cancel and go back",
	"dp_done":  "This booking is no longer waiting for payment.",

	// messages
	"m_notfound_t":    "Page not found",
	"m_notfound_x":    "We couldn't find what you were looking for.",
	"m_back":          "Back to start",
	"m_wrong_t":       "Something went wrong",
	"m_wrong_book":    "We couldn't save your booking. Please try again, or call us.",
	"m_wrong_order":   "We couldn't save your order. Please try again, or call us.",
	"m_try_again":     "Try again",
	"m_paystart_t":    "Payment couldn't start",
	"m_paystart_x":    "Our payment provider didn't respond, so nothing was charged. Please try again in a minute, or call us.",
	"m_slow_t":        "Please wait a moment",
	"m_slow_x":        "We got a lot of tries from your connection in a short time. Please wait about 10 minutes and try again, or call us.",
	"m_pending_t":     "You already have unpaid bookings",
	"m_pending_x":     "You have bookings waiting for payment. Finish paying with the link we emailed you, or wait about 35 minutes until they run out and try again.",
	"sd_table":        "Table deposit · %d guests · %s %s",
	"sd_ta":           "Takeaway deposit (25 %%) · pickup %s %s",
	"title_takeaway":  "Order takeaway",
	"ics_table":       "Table at %s",
	"choice_book":     "Pick a day and time, pay a small deposit",
	"choice_takeaway": "Order online, pick up at the restaurant",
	"good_to_know":    "Good to know",
	"big_title":       "Bigger groups",
	"book_lead":       "Takes about a minute. The deposit is taken off your bill.",
	"pick_time":       "Pick a time",
	"pick_day":        "Pick a day",
	"opening_payment": "Opening payment…",
	"your_booking":    "Your booking",
	"your_order":      "Your order",
	"pay":             "Pay",
	"order_empty":     "Nothing added yet. Tap Add on a dish.",
	"add":             "Add",
	"add_calendar":    "Add to calendar",
	"new_booking":     "Make another booking",
	"ta_lead2":        "Order now and pick up at the restaurant. Pay 25 % now and the rest at pickup.",
	"ics_takeaway":    "Pick up takeaway at %s",

	// booking changes, cancelling, waiting list, terms
	"f_occasion":          "Special occasion?",
	"occ_none":            "None",
	"occ_birthday":        "Birthday",
	"occ_anniversary":     "Anniversary",
	"occ_business":        "Business",
	"consent_1":           "By paying you accept our",
	"consent_terms":       "booking terms",
	"consent_2":           "and",
	"consent_privacy":     "privacy policy",
	"consent_terms_cap":   "Booking terms",
	"consent_privacy_cap": "Privacy",
	"wl_join":             "Join the waiting list",
	"wl_time_full":        "Is the time you want full?",
	"wl_join_link":        "Join the waiting list.",
	"msg_fully_booked_wl": "We're fully booked on %s. Join the waiting list and we'll email you if a table frees up, or pick another day.",
	"lbl_occasion":        "Occasion",
	"change_booking":      "Change booking",
	"st_policy_nodep":     "Can't come after all? Please cancel below, so another guest can have the table.",
	"st_change_call":      "To change the booking now, please call us.",
	"st_questions":        "Questions? Contact us:",
	"cancel_booking":      "Cancel booking",
	"cancel_order":        "Cancel order",
	"cancel_ta_kept":      "The %s takeaway deposit is non-refundable, so it won't be paid back.",
	"cancel_free":         "Your booking will be cancelled and the table given to another guest.",
	"cancel_refund":       "You're cancelling more than 24 hours before, so your %s deposit is refunded in full.",
	"cancel_kept":         "It's less than 24 hours before your booking, so the %s deposit is kept.",
	"cancel_yes":          "Yes, cancel my booking",
	"cancel_yes_ta":       "Yes, cancel my order",
	"fl_cancelled":        "Your booking is cancelled.",
	"fl_refunded":         "Cancelled. Your deposit is on its way back.",
	"fl_changed":          "Your booking is changed. We've emailed you the new details.",
	"fl_waitlist_removed": "You're off the waiting list.",
	"ch_title":            "Change your booking",
	"ch_lead":             "Now: %s. Pick a new day, time or number of guests.",
	"ch_size_note":        "Your deposit stays the same. For a different group size, please call us.",
	"ch_confirm":          "Save the change",
	"ch_confirm_note":     "We'll email you the new details. Your deposit stays the same and is still taken off your bill.",
	"saving":              "Saving…",
	"ch_save":             "Save changes",
	"ch_save_short":       "Save",
	"ch_keep":             "Keep my booking as it is",
	"ch_new":              "New time",
	"ch_was":              "Was: %s",
	"m_cancelfail_t":      "We couldn't cancel right now",
	"m_cancelfail_x":      "Our payment provider didn't respond, so nothing was changed. Please try again in a minute, or call us.",
	"m_back_booking":      "Back to your booking",
	"m_nochange_t":        "This booking can't be changed online",
	"m_nochange_x":        "Bookings can be changed online until 24 hours before. Please call us and we'll help.",
	"wl_title":            "Waiting list",
	"wl_lead":             "Some times are fully booked on %s. Leave your details and we'll email you as soon as a table frees up.",
	"wl_pref":             "Preferred time",
	"wl_around":           "Around %s",
	"wl_any":              "Any time",
	"wl_full_now":         "Full right now:",
	"wl_submit":           "Put me on the waiting list",
	"wl_note":             "Free, with no obligation. The table goes to whoever books first after our email.",
	"wl_on_h":             "You're on the waiting list",
	"wl_on_x":             "We'll email %s as soon as a table frees up.",
	"wl_notified_h":       "A table became free",
	"wl_notified_x":       "We emailed you about a free table on %s. Book it before someone else does.",
	"wl_book_now":         "Book now",
	"wl_removed_h":        "You're off the waiting list",
	"wl_removed_x":        "You won't get any more emails about this day.",
	"wl_leave":            "Take me off the list",
	"title_terms":         "Booking terms",
	"title_privacy":       "Privacy",
	"n_minutes":           "%d minutes",
	"n_year":              "1 year",
	"n_years":             "%d years",
	"n_months":            "%d months",
	"n_days":              "%d days",
}

var dictSV = map[string]string{
	"demo_banner": "Demoläge: inga riktiga betalningar görs.",
	"home_link":   "startsida",
	"lang_label":  "Språk",

	"home_title":    "Boka bord eller beställ avhämtning",
	"home_lead":     "Boka online på en minut. En liten handpenning säkrar bokningen och dras av från notan.",
	"cta_book":      "Boka bord",
	"cta_takeaway":  "Beställ avhämtning",
	"hours_title":   "Öppettider",
	"kitchen_until": "köket till %s",
	"closed":        "Stängt",
	"lunch_note":    "Lunch serveras vardagar 10:30–14:30. När köket har stängt serverar vi drycker.",
	"dep_title":     "Handpenning för bord",
	"dep_text":      "10 € för 1–2 personer, 20 € för 3–6 personer. Dras av från notan.",
	"dep_note":      "Gratis avbokning upp till 24 timmar innan. Är ni fler än 6, ring oss.",
	"ta_title":      "Avhämtning",
	"ta_text":       "Betala 25 % när du beställer och resten när du hämtar.",
	"ta_note":       "Handpenningen för avhämtning återbetalas inte.",

	"book_title":       "Boka bord",
	"step_guests":      "Hur många är ni?",
	"step_date":        "Vilken dag?",
	"step_time":        "Vilken tid?",
	"step_details":     "Dina uppgifter",
	"big_group":        "Fler än 6 personer? Ring oss.",
	"big_group_phone":  "Fler än 6 personer? Ring oss på",
	"other_date":       "Annat datum",
	"today":            "Idag",
	"tomorrow":         "I morgon",
	"day_full":         "Fullt",
	"day_closed":       "Stängt",
	"period_lunch":     "Lunch",
	"period_afternoon": "Eftermiddag",
	"period_evening":   "Kväll",
	"left":             "%d kvar",
	"full":             "Fullt",
	"table_2h":         "Bordet är ert i 2 timmar.",
	"f_name":           "Namn",
	"f_phone":          "Telefon",
	"f_email":          "E-post",
	"f_notes_table":    "Något vi borde veta?",
	"f_notes_ta":       "Önskemål",
	"optional":         "(valfritt)",
	"notes_ph_table":   "Allergier, barnstol, födelsedag",
	"deposit":          "Handpenning",
	"deposit_note_tbl": "Dras av från notan. Återbetalas om du avbokar senast 24 timmar innan.",
	"pay_deposit":      "Betala handpenning",
	"guests_one":       "1 person",
	"guests_many":      "%d personer",

	"msg_invalid_date":  "Välj ett giltigt datum.",
	"msg_past":          "Det datumet har redan passerat. Välj en annan dag.",
	"msg_too_far":       "Man kan boka bord för de kommande %d dagarna. Välj en tidigare dag.",
	"msg_closed_on":     "Vi har stängt %s. Välj en annan dag.",
	"msg_no_more_times": "Det finns inga fler tider att boka %s. Välj en annan dag.",
	"msg_fully_booked":  "Vi är fullbokade %s. Välj en annan dag.",
	"err_name":          "Skriv ditt namn.",
	"err_email":         "Skriv en giltig e-postadress, till exempel namn@exempel.fi.",
	"err_phone":         "Skriv ett telefonnummer så att vi kan nå dig.",
	"err_notes":         "Håll önskemålen under 500 tecken.",
	"err_size":          "Välj 1–6 personer.",
	"err_time":          "Välj en tid.",
	"err_taken":         "Tyvärr blev den tiden just bokad. Välj en annan tid.",
	"err_duplicate":     "Du har redan en bokning kl. %s den dagen. Ring oss om du vill ändra den.",
	"err_items":         "Välj minst en rätt.",
	"err_items_max":     "Vill du ha fler än 20 av samma rätt, ring oss.",
	"err_pickup":        "Välj dag och tid för hämtning.",
	"err_summary":       "Kontrollera de markerade fälten.",

	"ta_page_title": "Beställ avhämtning",
	"ta_lead":       "Betala 25 % nu och resten när du hämtar.",
	"ta_step_food":  "Välj mat",
	"ta_step_when":  "När vill du hämta?",
	"legend":        "L laktosfri · G glutenfri · V vegetarisk · M mjölkfri · FI finländskt kött · AX från Åland · * kan fås så, skriv det i önskemålen",
	"notes_ph_ta":   "Allergier, pommes eller sötpotatispommes, choklad- eller kinuskisås",
	"food_total":    "Mat totalt",
	"deposit_now":   "Handpenning nu (25 %)",
	"pay_at_pickup": "Betalas vid hämtning",
	"ta_nonrefund":  "Handpenningen återbetalas inte.",
	"cart_one":      "1 rätt",
	"cart_many":     "%d rätter",
	"continue":      "Fortsätt",
	"no_days":       "Avhämtning kan inte beställas de närmaste dagarna. Ring oss.",
	"less":          "En mindre",
	"more":          "En till",
	"how_many":      "Antal",
	"categories":    "Menyns delar",
	"pickup_at":     "Hämtning",

	"st_title":          "Din bokning",
	"st_booked_table":   "Ditt bord är bokat",
	"st_code":           "Din bordskod",
	"st_code_x":         "På restaurangen skannar du QR-koden på ert bord och skriver in koden. Sedan kan ni beställa och betala i mobilen.",
	"st_booked_ta":      "Din beställning är bekräftad",
	"st_sent_to":        "Vi har skickat en bekräftelse till %s.",
	"st_pending_h":      "Nästan klart: betala handpenningen",
	"st_hold_table":     "Vi håller ditt bord till %s. Bokningen bekräftas när handpenningen är betald.",
	"st_hold_ta":        "Din beställning är sparad till %s. Den bekräftas när handpenningen är betald.",
	"st_pay":            "Betala %s",
	"st_just_paid":      "Har du just betalat? Det kan ta några sekunder.",
	"st_refresh":        "Ladda om sidan",
	"st_expired_h":      "Bokningen har gått ut",
	"st_expired_table":  "Handpenningen betalades inte i tid, så bordet släpptes. Inget har debiterats.",
	"st_expired_ta":     "Handpenningen betalades inte i tid, så beställningen avbröts. Inget har debiterats.",
	"st_start_again":    "Börja om",
	"st_cancelled_h":    "Bokningen är avbokad",
	"st_refunded_text":  "Din handpenning på %s har återbetalats.",
	"st_cancelled_text": "Bokningen avbokades. Handpenningen återbetalades inte.",
	"lbl_booking":       "Bokning",
	"lbl_date":          "Datum",
	"lbl_time":          "Tid",
	"lbl_guests":        "Personer",
	"lbl_pickup":        "Hämtning",
	"lbl_order":         "Beställning",
	"lbl_total":         "Totalt",
	"lbl_deposit":       "Handpenning",
	"lbl_pay_pickup":    "Betalas vid hämtning",
	"lbl_name":          "Namn",
	"lbl_phone":         "Telefon",
	"lbl_notes":         "Önskemål",
	"at":                "kl.",
	"st_policy_table":   "Handpenningen dras av från notan. Gratis avbokning till %s, därefter behålls handpenningen.",
	"st_policy_ta":      "Handpenningen för avhämtning återbetalas inte.",
	"st_contact":        "Vill du avboka eller ändra, kontakta oss:",

	"dp_title": "Demobetalning",
	"dp_lead":  "Stripe är inte uppsatt ännu, så den här sidan ersätter betalsidan. Betalar du här bekräftas bokningen och e-posten skickas, precis som vid en riktig betalning.",
	"dp_pay":   "Betala %s (demo)",
	"dp_back":  "Avbryt och gå tillbaka",
	"dp_done":  "Den här bokningen väntar inte längre på betalning.",

	"m_notfound_t":    "Sidan hittades inte",
	"m_notfound_x":    "Vi hittade inte det du letade efter.",
	"m_back":          "Till startsidan",
	"m_wrong_t":       "Något gick fel",
	"m_wrong_book":    "Vi kunde inte spara din bokning. Försök igen, eller ring oss.",
	"m_wrong_order":   "Vi kunde inte spara din beställning. Försök igen, eller ring oss.",
	"m_try_again":     "Försök igen",
	"m_paystart_t":    "Betalningen kunde inte starta",
	"m_paystart_x":    "Vår betaltjänst svarade inte, så inget har debiterats. Försök igen om en minut, eller ring oss.",
	"m_slow_t":        "Vänta en stund",
	"m_slow_x":        "Vi har fått många försök från din anslutning på kort tid. Vänta cirka 10 minuter och försök igen, eller ring oss.",
	"m_pending_t":     "Du har redan obetalda bokningar",
	"m_pending_x":     "Du har bokningar som väntar på betalning. Betala via länken vi mejlade dig, eller vänta cirka 35 minuter tills de går ut och försök igen.",
	"sd_table":        "Handpenning bord · %d personer · %s %s",
	"sd_ta":           "Handpenning avhämtning (25 %%) · hämtas %s %s",
	"title_takeaway":  "Beställ avhämtning",
	"ics_table":       "Bord på %s",
	"choice_book":     "Välj dag och tid, betala en liten handpenning",
	"choice_takeaway": "Beställ online, hämta på restaurangen",
	"good_to_know":    "Bra att veta",
	"big_title":       "Större sällskap",
	"book_lead":       "Tar ungefär en minut. Handpenningen dras av från notan.",
	"pick_time":       "Välj tid",
	"pick_day":        "Välj dag",
	"opening_payment": "Öppnar betalningen…",
	"your_booking":    "Din bokning",
	"your_order":      "Din beställning",
	"pay":             "Betala",
	"order_empty":     "Inget valt ännu. Tryck Lägg till på en rätt.",
	"add":             "Lägg till",
	"add_calendar":    "Lägg till i kalendern",
	"new_booking":     "Gör en ny bokning",
	"ta_lead2":        "Beställ nu och hämta på restaurangen. Betala 25 % nu och resten vid hämtning.",
	"ics_takeaway":    "Hämta avhämtning på %s",

	// booking changes, cancelling, waiting list, terms
	"f_occasion":          "Något särskilt tillfälle?",
	"occ_none":            "Inget",
	"occ_birthday":        "Födelsedag",
	"occ_anniversary":     "Årsdag",
	"occ_business":        "Affärsmöte",
	"consent_1":           "Genom att betala godkänner du våra",
	"consent_terms":       "bokningsvillkor",
	"consent_2":           "och vår",
	"consent_privacy":     "integritetspolicy",
	"consent_terms_cap":   "Bokningsvillkor",
	"consent_privacy_cap": "Integritet",
	"wl_join":             "Ställ dig på väntelistan",
	"wl_time_full":        "Är tiden du vill ha full?",
	"wl_join_link":        "Ställ dig på väntelistan.",
	"msg_fully_booked_wl": "Vi är fullbokade %s. Ställ dig på väntelistan så mejlar vi om ett bord blir ledigt, eller välj en annan dag.",
	"lbl_occasion":        "Tillfälle",
	"change_booking":      "Ändra bokning",
	"st_policy_nodep":     "Kan ni inte komma ändå? Avboka gärna nedan, så att en annan gäst kan få bordet.",
	"st_change_call":      "Vill du ändra bokningen nu, ring oss.",
	"st_questions":        "Frågor? Kontakta oss:",
	"cancel_booking":      "Avboka",
	"cancel_order":        "Avbeställ",
	"cancel_ta_kept":      "Handpenningen på %s för avhämtning återbetalas inte.",
	"cancel_free":         "Bokningen avbokas och bordet går till en annan gäst.",
	"cancel_refund":       "Du avbokar mer än 24 timmar innan, så du får tillbaka hela handpenningen på %s.",
	"cancel_kept":         "Det är mindre än 24 timmar kvar till bokningen, så handpenningen på %s behålls.",
	"cancel_yes":          "Ja, avboka",
	"cancel_yes_ta":       "Ja, avbeställ",
	"fl_cancelled":        "Din bokning är avbokad.",
	"fl_refunded":         "Avbokat. Handpenningen betalas tillbaka.",
	"fl_changed":          "Bokningen är ändrad. Vi har mejlat de nya uppgifterna.",
	"fl_waitlist_removed": "Du är borttagen från väntelistan.",
	"ch_title":            "Ändra din bokning",
	"ch_lead":             "Nu: %s. Välj ny dag, tid eller antal personer.",
	"ch_size_note":        "Handpenningen är densamma. För en annan gruppstorlek, ring oss.",
	"ch_confirm":          "Spara ändringen",
	"ch_confirm_note":     "Vi mejlar de nya uppgifterna. Handpenningen är densamma och dras fortfarande av från notan.",
	"saving":              "Sparar…",
	"ch_save":             "Spara ändringar",
	"ch_save_short":       "Spara",
	"ch_keep":             "Behåll bokningen som den är",
	"ch_new":              "Ny tid",
	"ch_was":              "Förut: %s",
	"m_cancelfail_t":      "Vi kunde inte avboka just nu",
	"m_cancelfail_x":      "Betalningstjänsten svarade inte, så inget har ändrats. Försök igen om en stund, eller ring oss.",
	"m_back_booking":      "Tillbaka till bokningen",
	"m_nochange_t":        "Bokningen kan inte ändras på nätet",
	"m_nochange_x":        "Bokningar kan ändras på nätet fram till 24 timmar innan. Ring oss så hjälper vi dig.",
	"wl_title":            "Väntelista",
	"wl_lead":             "Vissa tider är fullbokade %s. Lämna dina uppgifter så mejlar vi så fort ett bord blir ledigt.",
	"wl_pref":             "Önskad tid",
	"wl_around":           "Runt %s",
	"wl_any":              "När som helst",
	"wl_full_now":         "Fullt just nu:",
	"wl_submit":           "Sätt upp mig på väntelistan",
	"wl_note":             "Gratis och utan förpliktelser. Bordet går till den som bokar först efter vårt mejl.",
	"wl_on_h":             "Du står på väntelistan",
	"wl_on_x":             "Vi mejlar %s så fort ett bord blir ledigt.",
	"wl_notified_h":       "Ett bord blev ledigt",
	"wl_notified_x":       "Vi har mejlat dig om ett ledigt bord %s. Boka innan någon annan hinner före.",
	"wl_book_now":         "Boka nu",
	"wl_removed_h":        "Du är borttagen från väntelistan",
	"wl_removed_x":        "Du får inga fler mejl om den här dagen.",
	"wl_leave":            "Ta bort mig från listan",
	"title_terms":         "Bokningsvillkor",
	"title_privacy":       "Integritet",
	"n_minutes":           "%d minuter",
	"n_year":              "1 år",
	"n_years":             "%d år",
	"n_months":            "%d månader",
	"n_days":              "%d dagar",
}
