package handlers

import (
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// Feedback from guests: /review (the review QR code, the table page,
// receipts). Stars first, then a comment. Every piece of feedback goes to
// the restaurant by email and onto the owner's Feedback page, and is never
// published. Then every guest, whatever they rated, is offered the
// restaurant's Google review link (Google doesn't allow asking only happy
// guests, and only the guest can post on Google).

var limitReview = limit{n: 6, window: time.Hour}

func stars(n int) string {
	if n < 0 || n > 5 {
		n = 0
	}
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}

// googleReviewURL is the configured Google link, if it's a proper https one.
func (s *Server) googleReviewURL() string {
	u, err := url.Parse(s.Cfg.GoogleReviewURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	return u.String()
}

func (s *Server) reviewPage(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	s.render(w, r, "review", http.StatusOK, M{
		"Title": st(lang, "How was it?"), "Step": "stars", "Table": atoiDefault(r.URL.Query().Get("table"), 0),
		"Stars": []int{1, 2, 3, 4, 5},
	})
}

// reviewStars: step 1, the stars.
func (s *Server) reviewStars(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if r.FormValue("website") != "" { // a field people can't see: only robots fill it in
		http.Redirect(w, r, "/review", http.StatusSeeOther)
		return
	}
	n := atoiDefault(r.FormValue("stars"), 0)
	if n < 1 || n > 5 {
		http.Redirect(w, r, "/review", http.StatusSeeOther)
		return
	}
	rv, err := s.Store.AddReview(n, atoiDefault(r.FormValue("table"), 0), langOf(r), s.Now())
	if err != nil {
		log.Printf("review: %v", err)
		http.Redirect(w, r, "/review", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/review/"+rv.Ref, http.StatusSeeOther)
}

// reviewStep2: step 2, the comment (or the thank-you page once it's sent).
func (s *Server) reviewStep2(w http.ResponseWriter, r *http.Request) {
	rv, ok := s.Store.ReviewByRef(r.PathValue("ref"))
	if !ok {
		s.notFound(w, r)
		return
	}
	lang := langOf(r)
	step := "comment"
	if rv.Done || s.Now().Sub(rv.At) > db.ReviewEditFor {
		step = "thanks"
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	s.render(w, r, "review", http.StatusOK, M{
		"Title": st(lang, "How was it?"), "Step": step, "R": rv, "StarText": stars(rv.Stars),
		"Google": s.googleReviewURL(),
	})
}

// reviewFinish: the comment is sent (or skipped): the restaurant gets an email.
func (s *Server) reviewFinish(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	ref := r.PathValue("ref")
	text, email := "", ""
	if r.FormValue("skip") == "" {
		text = r.FormValue("text")
		email = strings.TrimSpace(r.FormValue("email"))
		if a, err := mail.ParseAddress(email); email != "" && (err != nil || a.Address != email || len(email) > 254) {
			email = "" // not an email address: no reply then
		}
	}
	rv, err := s.Store.FinishReview(ref, text, email, s.Now())
	if err != nil {
		http.Redirect(w, r, "/review/"+ref, http.StatusSeeOther)
		return
	}
	if to := s.Cfg.ManagerEmail; to != "" {
		subject, body := s.reviewEmail(rv)
		s.Mail.SendAsync(to, subject, body)
	}
	http.Redirect(w, r, "/review/"+ref, http.StatusSeeOther)
}

// reviewEmail tells the restaurant about new feedback (staff emails are in English).
func (s *Server) reviewEmail(rv db.Review) (string, string) {
	when := longDate(langEN, schedule.FormatDate(rv.At)) + ", " + clockOf(rv.At)
	var b strings.Builder
	fmt.Fprintf(&b, "%s  (%d of 5)\n\n", stars(rv.Stars), rv.Stars)
	if rv.Text != "" {
		fmt.Fprintf(&b, "\"%s\"\n\n", rv.Text)
	} else {
		b.WriteString("(No comment, just the stars.)\n\n")
	}
	fmt.Fprintf(&b, "When: %s\n", when)
	if rv.Table > 0 {
		fmt.Fprintf(&b, "Table: %d\n", rv.Table)
	}
	if rv.Email != "" {
		fmt.Fprintf(&b, "They'd like a reply: %s\n", rv.Email)
	}
	fmt.Fprintf(&b, "\nThis feedback is private: it's not published anywhere.\nAll feedback: %s/owner/feedback\n", s.Cfg.BaseURL)
	return fmt.Sprintf("Feedback: %s (%d of 5)", stars(rv.Stars), rv.Stars), b.String()
}

type feedbackView struct {
	db.Review
	StarText string
	When     string
}

// ownerFeedback lists all feedback, with the average of the last 30 days.
func (s *Server) ownerFeedback(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	now := s.Now()
	var list []feedbackView
	sum, n, low := 0, 0, 0
	for _, rv := range s.Store.Reviews(200) {
		list = append(list, feedbackView{Review: rv, StarText: stars(rv.Stars),
			When: shortDateL(lang, schedule.FormatDate(rv.At)) + " " + clockOf(rv.At)})
		if now.Sub(rv.At) <= 30*24*time.Hour {
			sum, n = sum+rv.Stars, n+1
			if rv.Stars < 4 {
				low++
			}
		}
	}
	avg := ""
	if n > 0 {
		avg = strings.Replace(strconv.FormatFloat(float64(sum)/float64(n), 'f', 1, 64), ".", ",", 1)
	}
	s.render(w, r, "owner_feedback", http.StatusOK, M{
		"Title": st(lang, "Feedback"), "Admin": true, "Owner": true, "Nav": "feedback",
		"List": list, "Avg": avg, "Count": n, "Low": low, "Google": s.googleReviewURL(),
	})
}
