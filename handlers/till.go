package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ozmo-booking/db"
	"ozmo-booking/schedule"
)

// The shared till, like the old one's "Servitör" keypad: a waiter types
// their number and 4-digit PIN to log in, and "Lock" logs them out again
// for the next person. The same keypad clocks them in and out ("Klockkort").
//
// A PIN is short, so the keypad only works on devices the manager has set
// up as a till (a signed cookie on that device, from the Staff page): from
// anywhere else nobody can try PINs, and too many wrong ones lock the
// keypad for a while.

const (
	tillCookie     = "kb_till"
	tillCookieLife = 400 * 24 * time.Hour // as long as browsers keep a cookie
	tillSession    = 12 * time.Hour       // a waiter's PIN login lasts at most a shift
)

var (
	limitPINDevice = limit{n: 10, window: 10 * time.Minute} // wrong PINs on one till
	limitPINWaiter = limit{n: 5, window: 15 * time.Minute}  // wrong PINs for one waiter number
)

func (s *Server) makeTillCookie() string {
	payload := "till|" + randomID()
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + s.sign(payload)
}

// isTill reports whether this device was set up as a till.
func (s *Server) isTill(r *http.Request) bool {
	c, err := r.Cookie(tillCookie)
	if err != nil {
		return false
	}
	enc, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || !strings.HasPrefix(string(raw), "till|") {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.sign(string(raw))))
}

func (s *Server) setTillCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: tillCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(s.Cfg.BaseURL, "https://"),
	})
}

// adminTillDevice: the manager makes this device a till (or stops it).
func (s *Server) adminTillDevice(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("on") == "1" {
		s.setTillCookie(w, s.makeTillCookie(), int(tillCookieLife/time.Second))
		redirectMsg(w, r, "/admin/staff", nil, "tillon")
		return
	}
	s.setTillCookie(w, "", -1)
	redirectMsg(w, r, "/admin/staff", nil, "tilloff")
}

func (s *Server) tillPage(w http.ResponseWriter, r *http.Request) {
	s.renderTill(w, r, http.StatusOK, "", "")
}

func (s *Server) renderTill(w http.ResponseWriter, r *http.Request, status int, msg, errMsg string) {
	lang := langOf(r)
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, "till", status, M{
		"Admin": true, "Bare": true, "Title": st(lang, "Till"), "IsTill": s.isTill(r),
		"Msg": msg, "Err": errMsg, "Now": s.Now().In(schedule.Location).Format("15:04"),
	})
}

// tillSubmit: number + PIN, then log in, clock in or clock out.
func (s *Server) tillSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	lang := langOf(r)
	if !s.isTill(r) {
		s.renderTill(w, r, http.StatusForbidden, "", "")
		return
	}
	now := s.Now()
	no := atoiDefault(strings.TrimSpace(r.FormValue("no")), 0)
	devKey, waiterKey := "pin|"+s.clientIP(r), "pinno|"+r.FormValue("no")
	if s.rl.over(devKey, limitPINDevice, now) || s.rl.over(waiterKey, limitPINWaiter, now) {
		s.renderTill(w, r, http.StatusTooManyRequests, "", st(lang, "Too many wrong PINs. Wait 15 minutes, or log in with the username and password."))
		return
	}
	m, ok := s.Store.CheckStaffPIN(no, r.FormValue("pin"))
	if !ok {
		s.rl.hit(devKey, now)
		s.rl.hit(waiterKey, now)
		time.Sleep(wrongPasswordDelay)
		s.renderTill(w, r, http.StatusUnauthorized, "", st(lang, "That number or PIN isn't right."))
		return
	}
	clock := now.In(schedule.Location).Format("15:04")
	switch r.FormValue("action") {
	case "in":
		if _, err := s.Store.ClockIn(m, now); errors.Is(err, db.ErrClockedIn) {
			s.renderTill(w, r, http.StatusOK, stf(lang, "%s is already clocked in.", m.Name), "")
			return
		} else if err != nil {
			s.renderTill(w, r, http.StatusInternalServerError, "", st(lang, "Something went wrong. Try again."))
			return
		}
		s.renderTill(w, r, http.StatusOK, stf(lang, "%s clocked in at %s.", m.Name, clock), "")
	case "out":
		sh, err := s.Store.ClockOut(m, now)
		if errors.Is(err, db.ErrClockedOut) {
			s.renderTill(w, r, http.StatusOK, stf(lang, "%s isn't clocked in.", m.Name), "")
			return
		} else if err != nil {
			s.renderTill(w, r, http.StatusInternalServerError, "", st(lang, "Something went wrong. Try again."))
			return
		}
		s.renderTill(w, r, http.StatusOK, stf(lang, "%s clocked out at %s after %s.", m.Name, clock, hoursText(sh.Length(now))), "")
	default:
		s.setSession(w, s.makeStaffSessionFor(m, now, tillSession), int(tillSession/time.Second))
		http.Redirect(w, r, "/admin/orders", http.StatusSeeOther)
	}
}

// tillLock logs the waiter out and shows the keypad for the next one.
func (s *Server) tillLock(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	s.setSession(w, "", -1)
	http.Redirect(w, r, "/till", http.StatusSeeOther)
}

// hoursText: 7h05m -> "7 h 05 min".
func hoursText(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	return fmt.Sprintf("%d h %02d min", m/60, m%60)
}

func randomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
