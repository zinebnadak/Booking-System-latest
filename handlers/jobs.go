package handlers

import (
	"context"
	"log"
	"time"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

// Work that happens by itself every few minutes, while the server runs:
//   - reminder emails the day before a table booking
//   - telling the waiting list when a table frees up (e.g. an unpaid
//     booking ran out)
//   - removing guests' personal details from old bookings (privacy)
//   - ending "3 meals" deals and button calls left open overnight

const (
	jobEvery = 5 * time.Minute

	// A reminder goes out once the booking is less than 24 hours away,
	// but not in the last 2 hours (too late to be useful), and only for
	// bookings made at least 12 hours earlier (they just got a confirmation).
	remindBefore   = 24 * time.Hour
	remindNotAfter = 2 * time.Hour
	remindMinAge   = 12 * time.Hour
)

// RunJobs runs the background work until ctx is cancelled.
func (s *Server) RunJobs(ctx context.Context) {
	s.Tick()
	t := time.NewTicker(jobEvery)
	defer t.Stop()
	// Payments on guests' phones that nobody finished are let go quickly,
	// so the rest of the table can pay.
	pay := time.NewTicker(paySweepEvery)
	defer pay.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick()
		case <-pay.C:
			s.SweepPayments(ctx)
		}
	}
}

// Tick does one round of background work. Tests call it directly.
func (s *Server) Tick() {
	now := s.Now()
	s.sendReminders(now)
	// Deals and button presses left open from an earlier day are cleared.
	if err := s.Store.TidyKitchen(now); err != nil {
		log.Printf("tidying kitchen: %v", err)
	}
	if err := s.Store.TidyEmptyBills(now); err != nil { // tables opened for QR orders where nobody ordered
		log.Printf("tidying empty bills: %v", err)
	}
	if err := s.Store.TidyTables(now); err != nil {
		log.Printf("tidying tables: %v", err)
	} else {
		s.live.publish() // screens redraw (their "minutes waiting" too)
	}
	s.sendDailyReports(now) // the owner's end-of-day email, after closing
	for _, date := range s.Store.WaitDates(schedule.FormatDate(schedule.Today(now))) {
		s.notifyWaitlist(date)
	}
	if s.Cfg.RetentionDays > 0 {
		cutoff := schedule.FormatDate(schedule.Today(now).AddDate(0, 0, -s.Cfg.RetentionDays))
		if n, err := s.Store.Anonymize(cutoff); err != nil {
			log.Printf("privacy clean-up: %v", err)
		} else if n > 0 {
			log.Printf("privacy clean-up: removed guest details from %d bookings before %s", n, cutoff)
		}
	}
}

func (s *Server) sendReminders(now time.Time) {
	for _, b := range s.Store.Upcoming(now.Add(remindNotAfter), now.Add(remindBefore)) {
		if b.Kind != db.KindTable || b.Email == "" || b.RemindedAt != nil || b.ArrivedAt != nil ||
			now.Sub(b.CreatedAt) < remindMinAge {
			continue
		}
		updated, changed, err := s.Store.MarkReminded(b.Ref, now)
		if err != nil || !changed {
			continue
		}
		subject, body := reminderEmail(s.Cfg, updated, now)
		s.Mail.SendAsync(updated.Email, subject, body)
	}
}
