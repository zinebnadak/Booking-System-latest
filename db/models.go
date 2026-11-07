package db

import (
	"time"

	"emeal-booking/schedule"
)

// Kind is what a booking is: a table reservation or a takeaway order.
type Kind string

const (
	KindTable    Kind = "table"
	KindTakeaway Kind = "takeaway"
)

// Status is where a booking is in its life.
//
//	pending   -> created, waiting for the deposit to be paid
//	paid      -> deposit paid, booking confirmed
//	expired   -> never paid in time, the table was released
//	cancelled -> cancelled by the restaurant, deposit kept
//	refunded  -> cancelled by the restaurant, deposit refunded
type Status string

const (
	StatusPending   Status = "pending"
	StatusPaid      Status = "paid"
	StatusExpired   Status = "expired"
	StatusCancelled Status = "cancelled"
	StatusRefunded  Status = "refunded"

	// These two are never saved: they're worked out from a paid table
	// booking and the clock (see EffectiveStatus).
	StatusArrived Status = "arrived" // the manager pressed "Arrived"
	StatusNoShow  Status = "no_show" // nobody arrived within 20 minutes: deposit kept, table freed

	StatusPickedUp Status = "picked_up" // takeaway order collected (worked out from PickedUpAt)
)

// Where a booking came from.
const (
	SourceOnline = ""       // the guest booked on the website and paid a deposit
	SourcePhone  = "phone"  // staff took it on the phone: no deposit
	SourceWalkIn = "walkin" // guests came in without booking: seated straight away
)

// OrderItem is one line of a takeaway order. Name and price are copied at
// order time, so later menu changes don't rewrite old orders.
type OrderItem struct {
	MenuItemID     int    `json:"menu_item_id"`
	Name           string `json:"name"`
	UnitPriceCents int    `json:"unit_price_cents"`
	Quantity       int    `json:"quantity"`
}

// LineTotal is price × quantity.
func (o OrderItem) LineTotal() int { return o.UnitPriceCents * o.Quantity }

// Booking is a table reservation or a takeaway order.
type Booking struct {
	ID   int    `json:"id"`
	Ref  string `json:"ref"` // long random code used in links, impossible to guess
	Kind Kind   `json:"kind"`

	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
	Notes string `json:"notes"`
	Lang  string `json:"lang,omitempty"` // "sv" or "en": language of the guest's emails

	Source   string `json:"source,omitempty"`   // "" (online), "phone" or "walkin"
	Occasion string `json:"occasion,omitempty"` // "", "birthday", "anniversary", "business"

	Date      string `json:"date"`               // "2026-10-24"
	Time      string `json:"time"`               // table: start time, takeaway: pickup time
	PartySize int    `json:"party_size"`         // table bookings only
	TableNo   int    `json:"table_no,omitempty"` // 1–6, given automatically, the manager can move it
	Minutes   int    `json:"minutes,omitempty"`  // how long the table is booked; 0 = the normal 2 hours

	Items      []OrderItem `json:"items,omitempty"` // takeaway only
	TotalCents int         `json:"total_cents"`     // takeaway food total

	DepositCents int    `json:"deposit_cents"`
	Status       Status `json:"status"`

	CheckoutSessionID string `json:"checkout_session_id,omitempty"`
	CheckoutURL       string `json:"checkout_url,omitempty"`
	PaymentIntentID   string `json:"payment_intent_id,omitempty"`

	CreatedAt     time.Time  `json:"created_at"`
	HoldExpiresAt time.Time  `json:"hold_expires_at"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
	CancelledAt   *time.Time `json:"cancelled_at,omitempty"`
	ArrivedAt     *time.Time `json:"arrived_at,omitempty"`   // when the manager pressed "Arrived"
	RemindedAt    *time.Time `json:"reminded_at,omitempty"`  // reminder email sent
	PickedUpAt    *time.Time `json:"picked_up_at,omitempty"` // takeaway collected
	CancelledBy   string     `json:"cancelled_by,omitempty"` // "guest" or "staff"
	TableCode     string     `json:"table_code,omitempty"`   // table bookings: 4 digits that unlock the table's QR code
	Anonymized    bool       `json:"anonymized,omitempty"`   // personal details removed (old booking)
}

// Online reports whether the guest booked on the website (and paid a deposit).
func (b Booking) Online() bool { return b.Source == SourceOnline }

// WaitEntry is a guest waiting for a table on a fully booked day.
type WaitEntry struct {
	ID         int        `json:"id"`
	Ref        string     `json:"ref"`
	Date       string     `json:"date"`
	Time       string     `json:"time"` // preferred time, "" = any time
	PartySize  int        `json:"party_size"`
	Name       string     `json:"name"`
	Email      string     `json:"email"`
	Phone      string     `json:"phone"`
	Lang       string     `json:"lang,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	NotifiedAt *time.Time `json:"notified_at,omitempty"` // emailed that a table opened
	Removed    bool       `json:"removed,omitempty"`
}

// Start is when a table booking begins (or a takeaway is picked up).
func (b Booking) Start() time.Time {
	d, err := schedule.ParseDate(b.Date)
	if err != nil {
		return time.Time{}
	}
	t, err := schedule.At(d, b.Time)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Duration is how long the table is booked (normally 2 hours; a walk-in
// seated between two bookings can be shorter).
func (b Booking) Duration() time.Duration {
	if b.Minutes > 0 {
		return time.Duration(b.Minutes) * time.Minute
	}
	return schedule.BookingDuration
}

// EndClock is when the booking ends, e.g. "20:00".
func (b Booking) EndClock() string { return schedule.EndTimeFor(b.Time, b.Minutes) }

// isNoShow: a paid table booking nobody checked in for within 20 minutes.
func (b Booking) isNoShow(now time.Time) bool {
	return b.Kind == KindTable && b.Status == StatusPaid && b.ArrivedAt == nil &&
		!now.Before(b.Start().Add(schedule.NoShowAfter))
}

// HoldsTable reports whether this booking currently blocks a table:
// paid bookings do (until they turn into a no-show), unpaid ones only
// until their hold runs out.
func (b Booking) HoldsTable(now time.Time) bool {
	if b.Kind != KindTable {
		return false
	}
	switch b.Status {
	case StatusPaid:
		return !b.isNoShow(now)
	case StatusPending:
		return now.Before(b.HoldExpiresAt)
	}
	return false
}

// EffectiveStatus is the status as the outside world should see it:
// an unpaid booking whose hold ran out is expired, a checked-in booking is
// arrived, and a paid booking nobody checked in for is a no-show.
func (b Booking) EffectiveStatus(now time.Time) Status {
	switch {
	case b.Status == StatusPending && !now.Before(b.HoldExpiresAt):
		return StatusExpired
	case b.Kind == KindTable && b.Status == StatusPaid && b.ArrivedAt != nil:
		return StatusArrived
	case b.Kind == KindTakeaway && b.Status == StatusPaid && b.PickedUpAt != nil:
		return StatusPickedUp
	case b.isNoShow(now):
		return StatusNoShow
	}
	return b.Status
}

// RemainingCents is what a takeaway customer still pays at pickup.
func (b Booking) RemainingCents() int {
	if b.Kind != KindTakeaway {
		return 0
	}
	return b.TotalCents - b.DepositCents
}

// clone returns a copy that can be handed out safely (the slice is copied too).
func (b *Booking) clone() Booking {
	c := *b
	c.Items = append([]OrderItem(nil), b.Items...)
	return c
}

// MenuItem is one dish or drink on the menu.
type MenuItem struct {
	ID          int    `json:"id"`
	Category    string `json:"category"`
	Name        string `json:"name"`
	Description string `json:"description"`
	PriceCents  int    `json:"price_cents"`
	Tags        string `json:"tags"` // e.g. "L, *G" (lactose-free, can be made gluten-free)
	Available   bool   `json:"available"`
	Sort        int    `json:"sort"`

	// Drink: sold at the tables only. Drinks never go to the chef's screen
	// and are never on the takeaway menu (takeaway is food only).
	Drink bool `json:"drink,omitempty"`

	// SoldOutOn: the chef marked it sold out on this day ("2026-10-31"). It's
	// off every menu for the rest of that day and back by itself the next.
	SoldOutOn string `json:"sold_out_on,omitempty"`
	// Photo: the dish's photo file (in the photos folder next to the data file).
	Photo string `json:"photo,omitempty"`
	// Allergens: which of the 14 main allergens the dish contains (codes from
	// Allergens below), shown to guests next to the dish.
	Allergens []string `json:"allergens,omitempty"`

	// Alcohol: an alcoholic drink (over 1,2 %), with the higher VAT.
	Alcohol bool `json:"alcohol,omitempty"`
	// Course: starter, main or dessert (CourseStarter…), for holding the
	// mains until the starters are eaten. 0 for drinks.
	Course int `json:"course,omitempty"`

	// Set menus (e.g. After Work, 3 rätter 30 €). The set menu itself is a
	// Package: it has the price and never goes to the chef. Its dishes are
	// Included: price 0 (it's in the package), they go to the chef. Both are
	// StaffOnly: guests can't order them on their phones.
	Package   bool `json:"package,omitempty"`
	Included  bool `json:"included,omitempty"`
	StaffOnly bool `json:"staff_only,omitempty"`

	// HappyCents: the cocktail-hour price (0 = none), charged during
	// schedule.HappyHours. Happy is set on menus handed out at that time.
	HappyCents int  `json:"happy_cents,omitempty"`
	Happy      bool `json:"-"`

	// Stock (the Stock page): Counted means staff keep count of how many are
	// left (Left). It goes down as it's ordered, and at 0 it's out of stock
	// until someone counts it again or taps "Back in stock". Not counted:
	// it's simply on the menu.
	Counted bool `json:"counted,omitempty"`
	Left    int  `json:"left,omitempty"`

	// Until: a special, on the menu up to and including this day
	// ("2026-11-03"), then off by itself. "" = no end.
	Until string `json:"until,omitempty"`

	// Uses: what one portion takes from the ingredients in stock (100 g
	// spaghetti, 30 g cheese). Ordering it takes them; when one runs out,
	// the dish is out of stock too.
	Uses []IngredientUse `json:"uses,omitempty"`

	// Worked out when menus are handed out (not stored): Limited means its
	// ingredients limit it, CanMake is how many can still be made, ShortOf
	// is an ingredient that ran out.
	Limited bool   `json:"-"`
	CanMake int    `json:"-"`
	ShortOf string `json:"-"`
}

// VAT is the dish's VAT rate in hundredths of a percent (1350 = 13,5 %).
func (m MenuItem) VAT() int {
	if m.Alcohol {
		return schedule.VATAlcohol
	}
	return schedule.VATFood
}

// PriceMissing: a dish copied from the old menu without a price yet. Guests
// don't see it until the manager sets one.
func (m MenuItem) PriceMissing() bool { return m.PriceCents <= 0 && !m.Included }

// Allergens are the 14 allergens EU food law says guests must be told about,
// in the order they're shown.
var Allergens = []string{"gluten", "crustaceans", "egg", "fish", "peanuts", "soy", "milk",
	"nuts", "celery", "mustard", "sesame", "sulphites", "lupin", "molluscs"}

// CleanAllergens keeps only known allergen codes, once each, in the usual order.
func CleanAllergens(codes []string) []string {
	have := map[string]bool{}
	for _, c := range codes {
		have[c] = true
	}
	var out []string
	for _, a := range Allergens {
		if have[a] {
			out = append(out, a)
		}
	}
	return out
}

// SoldOut reports whether the dish is sold out on this day.
func (m MenuItem) SoldOut(today string) bool { return m.SoldOutOn != "" && m.SoldOutOn == today }

// OutOfStock: none left: staff count it and it's at 0, or an ingredient it
// needs ran out.
func (m MenuItem) OutOfStock() bool { return (m.Counted && m.Left <= 0) || m.ShortOf != "" }

// LeftNow is how many can still be ordered, by its own count and by its
// ingredients (whichever is fewer). Only meaningful if Counted or Limited.
func (m MenuItem) LeftNow() int {
	switch {
	case m.Counted && m.Limited:
		return max(min(m.Left, m.CanMake), 0)
	case m.Limited:
		return max(m.CanMake, 0)
	}
	return max(m.Left, 0)
}

// RunningLow: only a few left (LowStock or fewer), by its own count or its
// ingredients.
func (m MenuItem) RunningLow() bool {
	return (m.Counted || m.Limited) && !m.OutOfStock() && m.LeftNow() > 0 && m.LeftNow() <= LowStock
}

// Gone: nobody can order it now (out of stock, or the kitchen marked it
// sold out today).
func (m MenuItem) Gone(today string) bool { return m.OutOfStock() || m.SoldOut(today) }

// Ended: a special whose last day has passed.
func (m MenuItem) Ended(today string) bool { return m.Until != "" && m.Until < today }

// Active: on the menu today (switched on, and not a special that ended).
func (m MenuItem) Active(today string) bool { return m.Available && !m.Ended(today) }

// ClosedDate is a day the restaurant is closed for bookings (holidays, events).
type ClosedDate struct {
	Date   string `json:"date"`
	Reason string `json:"reason"`
}
