package db

import (
	"crypto/subtle"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"ozmo-booking/schedule"
)

// Guests' feedback: 1–5 stars and a comment. It's private: the restaurant
// gets it by email and sees it on the owner's Feedback page, and it's never
// published anywhere. (Every guest is then offered the restaurant's own
// Google review link, whatever they rated.)

var ErrReviewDone = errors.New("this feedback is already sent")

// ReviewEditFor: after the stars, the comment can be added this long.
const ReviewEditFor = 30 * time.Minute

// Review is one guest's feedback.
type Review struct {
	ID    int       `json:"id"`
	Ref   string    `json:"ref"` // lets the same phone add the comment
	At    time.Time `json:"at"`
	Stars int       `json:"stars"`
	Text  string    `json:"text,omitempty"`
	Email string    `json:"email,omitempty"` // only if they'd like a reply
	Table int       `json:"table,omitempty"` // from the table's page, if so
	Lang  string    `json:"lang,omitempty"`
	Done  bool      `json:"done,omitempty"` // comment step finished (sent to the restaurant)
}

// AddReview saves the stars (step 1).
func (s *Store) AddReview(stars, table int, lang string, now time.Time) (Review, error) {
	if stars < 1 || stars > 5 {
		return Review{}, errors.New("stars must be 1 to 5")
	}
	if table != 0 && !schedule.ValidTable(table) {
		table = 0
	}
	var out Review
	err := s.changeOrders(func() (bool, error) {
		s.d.NextReviewID++
		r := &Review{ID: s.d.NextReviewID, Ref: newRef(), At: now, Stars: stars, Table: table, Lang: lang}
		s.d.Reviews = append(s.d.Reviews, r)
		out = *r
		return true, nil
	})
	return out, err
}

func (s *Store) review(ref string) *Review {
	if len(ref) != 32 {
		return nil
	}
	for _, r := range s.d.Reviews {
		if subtle.ConstantTimeCompare([]byte(r.Ref), []byte(ref)) == 1 {
			return r
		}
	}
	return nil
}

// ReviewByRef returns a review (for its comment step).
func (s *Store) ReviewByRef(ref string) (Review, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.review(ref); r != nil {
		return *r, true
	}
	return Review{}, false
}

// FinishReview adds the comment (and an email for a reply), or nothing
// ("just the stars"). It can be done once, within ReviewEditFor.
func (s *Store) FinishReview(ref, text, email string, now time.Time) (Review, error) {
	var out Review
	err := s.changeOrders(func() (bool, error) {
		r := s.review(ref)
		if r == nil {
			return false, ErrNotFound
		}
		if r.Done || now.Sub(r.At) > ReviewEditFor {
			return false, ErrReviewDone
		}
		text = strings.TrimSpace(text)
		if utf8.RuneCountInString(text) > 2000 {
			text = string([]rune(text)[:2000])
		}
		r.Text, r.Email, r.Done = text, email, true
		out = *r
		return true, nil
	})
	return out, err
}

// Reviews lists feedback, newest first (at most n; 0 = all).
func (s *Store) Reviews(n int) []Review {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Review, 0, len(s.d.Reviews))
	for _, r := range s.d.Reviews {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// ReviewsOn lists a day's feedback.
func (s *Store) ReviewsOn(date string) []Review {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Review
	for _, r := range s.d.Reviews {
		if schedule.FormatDate(r.At) == date {
			out = append(out, *r)
		}
	}
	return out
}
