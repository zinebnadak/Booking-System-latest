package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"emeal-booking/db"
)

// Logging in for staff.
//
// The manager has their username and password in .env: every staff page,
// including the menu, closed days, staff and the money pages. (There used
// to be a separate head manager; its login, if set, now is a second
// manager login.)
//
// Waiters get their own login from the manager (Staff page): the tables,
// orders, kitchen and bookings, but not the menu, closed days or staff. What
// they do with money is recorded with their name (Activity page).
//
// After logging in, the browser keeps a signed cookie for 14 days, so staff
// don't have to type the password every time. Nobody can fake the cookie
// without the server's secret.

type role string

const (
	roleNone    role = ""
	roleWaiter  role = "waiter"
	roleManager role = "manager"
	roleOwner   role = "owner"

	sessionCookie = "kb_session"
	sessionLength = 14 * 24 * time.Hour
)

// wrongPasswordDelay is the pause after a wrong password (tests set it to 0).
var wrongPasswordDelay = 400 * time.Millisecond

type ctxKey struct{}

// login is who is logged in for a request.
type login struct {
	Role    role
	StaffID int    // waiters
	Name    string // waiters: their name
}

// rank orders the roles: a higher one may do everything a lower one can.
func rank(r role) int {
	switch r {
	case roleWaiter:
		return 1
	case roleManager, roleOwner: // one and the same now
		return 2
	}
	return 0
}

// roleOf returns who is logged in for this request (set by requireRole).
func roleOf(r *http.Request) role { return loginOf(r).Role }

func loginOf(r *http.Request) login {
	if v, ok := r.Context().Value(ctxKey{}).(login); ok {
		return v
	}
	return login{}
}

// actor is who did something, for the activity list.
func actor(r *http.Request) string {
	l := loginOf(r)
	switch l.Role {
	case roleWaiter:
		return l.Name
	}
	return "Manager"
}

// newSecret makes a random key for signing cookies when SESSION_SECRET isn't set.
func newSecret() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func (s *Server) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// makeSession builds the cookie value: "<role>|<expires>.<signature>".
func (s *Server) makeSession(who role, now time.Time) string {
	payload := string(who) + "|" + strconv.FormatInt(now.Add(sessionLength).Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + s.sign(payload)
}

// makeStaffSession is a waiter's cookie: "waiter|<expires>|<id>|<version>".
// The version changes with a new password or when they're removed, so old
// cookies stop working at once.
func (s *Server) makeStaffSession(m db.StaffMember, now time.Time) string {
	return s.makeStaffSessionFor(m, now, sessionLength)
}

// makeStaffSessionFor is a waiter's cookie lasting length (shorter for a
// PIN login at the shared till).
func (s *Server) makeStaffSessionFor(m db.StaffMember, now time.Time, length time.Duration) string {
	payload := string(roleWaiter) + "|" + strconv.FormatInt(now.Add(length).Unix(), 10) +
		"|" + strconv.Itoa(m.ID) + "|" + strconv.Itoa(m.Ver)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + s.sign(payload)
}

// readSession checks the cookie and returns who is logged in.
func (s *Server) readSession(r *http.Request) role { return s.readLogin(r).Role }

func (s *Server) readLogin(r *http.Request) login {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return login{}
	}
	enc, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return login{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return login{}
	}
	payload := string(raw)
	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return login{}
	}
	parts := strings.Split(payload, "|")
	if len(parts) < 2 {
		return login{}
	}
	unix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || s.Now().After(time.Unix(unix, 0)) {
		return login{}
	}
	switch role(parts[0]) {
	case roleManager, roleOwner: // old head-manager cookies are the manager's
		return login{Role: roleManager}
	case roleWaiter:
		if len(parts) != 4 {
			return login{}
		}
		id, err1 := strconv.Atoi(parts[2])
		ver, err2 := strconv.Atoi(parts[3])
		if err1 != nil || err2 != nil {
			return login{}
		}
		m, ok := s.Store.StaffSession(id, ver)
		if !ok {
			return login{} // removed, or a new password since
		}
		return login{Role: roleWaiter, StaffID: m.ID, Name: m.Name}
	}
	return login{}
}

func (s *Server) setSession(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(s.Cfg.BaseURL, "https://"),
	})
}

// requireRole lets a request through only for the right person. The head
// manager may open every staff page; the manager only the manager pages.
func (s *Server) requireRole(need role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := s.readLogin(r)
		who := l.Role
		if who == roleNone {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
				return
			}
			http.Error(w, "Please log in again", http.StatusUnauthorized)
			return
		}
		if rank(who) < rank(need) {
			lang := langOf(r)
			s.render(w, r, "message", http.StatusForbidden, M{
				"Admin": true, "Role": string(who), "Title": st(lang, "Manager only"),
				"Heading":  st(lang, "Only the manager can see this"),
				"Text":     st(lang, "These pages show the money. Log in as the manager to open them."),
				"LinkHref": "/admin/orders", "LinkText": st(lang, "Back to orders"),
			})
			return
		}
		if r.Method == http.MethodPost && !sameOrigin(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, l)))
	})
}

// managerOnly: pages waiters can't open (the menu, closed days, staff,
// activity, new QR codes).
func (s *Server) managerOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rank(roleOf(r)) < rank(roleManager) {
			lang := langOf(r)
			s.render(w, r, "message", http.StatusForbidden, M{
				"Admin": true, "Title": st(lang, "Manager only"),
				"Heading":  st(lang, "Only the manager can change this"),
				"Text":     st(lang, "Ask the manager, or log in as the manager."),
				"LinkHref": "/admin/orders", "LinkText": st(lang, "Back to orders"),
			})
			return
		}
		next(w, r)
	}
}

// checkPassword compares in constant time, so timing can't leak the password.
func checkPassword(gotUser, gotPass, wantUser, wantPass string) bool {
	if wantPass == "" {
		return false
	}
	a := sha256.Sum256([]byte(gotUser))
	b := sha256.Sum256([]byte(wantUser))
	c := sha256.Sum256([]byte(gotPass))
	d := sha256.Sum256([]byte(wantPass))
	userOK := subtle.ConstantTimeCompare(a[:], b[:]) == 1
	passOK := subtle.ConstantTimeCompare(c[:], d[:]) == 1
	return userOK && passOK
}

// safeNext only allows going back to a page on this site after logging in.
func safeNext(next string, who role) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.HasPrefix(next, "/\\") &&
		(strings.HasPrefix(next, "/admin") || strings.HasPrefix(next, "/owner")) {
		if rank(who) >= rank(roleManager) || !strings.HasPrefix(next, "/owner") {
			return next
		}
	}
	return "/admin/orders" // the staff's home page
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Query().Get("next")
	if who := s.readSession(r); who != roleNone {
		http.Redirect(w, r, safeNext(next, who), http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", 200, M{"Admin": true, "Bare": true, "Title": st(langOf(r), "Log in"), "Next": next})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	user := strings.TrimSpace(r.FormValue("username"))
	pass := r.FormValue("password")
	next := r.FormValue("next")
	// Too many wrong passwords from one address: stop checking for a while,
	// so nobody can try password after password.
	key := "login|" + s.clientIP(r)
	if s.rl.over(key, limitLogin, s.Now()) {
		s.tooMany(w, r, "/login")
		return
	}
	var who role
	var waiter db.StaffMember
	if m, ok := s.Store.CheckStaffLogin(user, pass); ok {
		who, waiter = roleWaiter, m
	}
	switch {
	case who == roleWaiter:
	case checkPassword(user, pass, s.Cfg.AdminUser, s.Cfg.AdminPassword),
		checkPassword(user, pass, s.Cfg.OwnerUser, s.Cfg.OwnerPassword): // the old head manager's login
		who = roleManager
	default:
		s.rl.hit(key, s.Now())
		time.Sleep(wrongPasswordDelay) // slows down password guessing
		s.render(w, r, "login", http.StatusUnauthorized, M{
			"Admin": true, "Bare": true, "Title": st(langOf(r), "Log in"), "Next": next, "User": user,
			"Error": st(langOf(r), "That username or password isn't right. Check them and try again."),
		})
		return
	}
	value := s.makeSession(who, s.Now())
	if who == roleWaiter {
		value = s.makeStaffSession(waiter, s.Now())
	}
	s.setSession(w, value, int(sessionLength/time.Second))
	http.Redirect(w, r, safeNext(next, who), http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) { // another site can't log staff out behind their back
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	s.setSession(w, "", -1)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
