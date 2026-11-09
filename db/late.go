package db

import (
	"errors"
	"time"

	"emeal-booking/schedule"
)

// "We're running late": on the day, a guest can tell the restaurant from
// their booking page (the link in the confirmation and reminder emails)
// that they'll be 15 or 30 minutes late. The table is then kept that much
// longer before the booking counts as a no-show, and staff see it on
// Bookings and Orders.

// LateChoices are the delays a guest can pick.
var LateChoices = []int{15, 30}

// LateNoticeFrom: how long before the start the button shows up.
const LateNoticeFrom = 3 * time.Hour

// ErrCantBeLate: the booking isn't one a guest can say they're late for
// (not today, already here, cancelled, or already a no-show).
var ErrCantBeLate = errors.New("this booking can't be marked as running late")

// NoShowTime is when a booking nobody checked in for turns into a no-show:
// 20 minutes after the start, plus however late the guest said they'd be.
func (b Booking) NoShowTime() time.Time {
	return b.Start().Add(schedule.NoShowAfter + time.Duration(b.LateMinutes)*time.Minute)
}

// CanSayLate: the guest may press "We're running late" now.
func (b Booking) CanSayLate(now time.Time) bool {
	if b.Kind != KindTable || b.Status != StatusPaid || b.ArrivedAt != nil || b.Start().IsZero() {
		return false
	}
	return !now.Before(b.Start().Add(-LateNoticeFrom)) && now.Before(b.NoShowTime())
}

// RunningLate records that the guest will be minutes late (15 or 30).
// Picking again changes it; changed is false when nothing changed (the
// same choice tapped twice).
func (s *Store) RunningLate(ref string, minutes int, now time.Time) (b Booking, changed bool, err error) {
	ok := false
	for _, m := range LateChoices {
		ok = ok || m == minutes
	}
	if !ok {
		return Booking{}, false, ErrCantBeLate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.find(ref)
	if p == nil {
		return Booking{}, false, ErrNotFound
	}
	if !p.CanSayLate(now) {
		return p.clone(), false, ErrCantBeLate
	}
	if p.LateMinutes == minutes {
		return p.clone(), false, nil
	}
	before := p.clone()
	at := now
	p.LateMinutes, p.LateAt = minutes, &at
	if err := s.save(); err != nil {
		*p = before
		return Booking{}, false, err
	}
	return p.clone(), true, nil
}
