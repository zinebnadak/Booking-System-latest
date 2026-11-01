package handlers

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rate limits stop one visitor (or one script) from hammering the forms:
// guessing staff passwords, or creating dozens of unpaid bookings that hold
// every table for 35 minutes and send payment emails to strangers.
//
// Each limit is "at most N tries per window" per visitor address. It lives in
// memory, so a restart clears it, which is fine for this purpose.

type limit struct {
	n      int
	window time.Duration
}

var (
	limitLogin    = limit{n: 10, window: 15 * time.Minute} // wrong passwords
	limitBooking  = limit{n: 6, window: 10 * time.Minute}  // new table bookings and takeaway orders
	limitWaitlist = limit{n: 5, window: 10 * time.Minute}  // joining the waiting list
	limitGuestAct = limit{n: 30, window: 10 * time.Minute} // cancel, change, leave the waiting list
)

type limiter struct {
	mu    sync.Mutex
	hits  map[string][]time.Time
	swept time.Time // when old entries were last forgotten
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

// recent returns key's tries inside the window (and drops older ones).
// The caller holds l.mu.
func (l *limiter) recent(key string, lim limit, now time.Time) []time.Time {
	if now.Sub(l.swept) > time.Hour {
		for k, ts := range l.hits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > time.Hour {
				delete(l.hits, k)
			}
		}
		l.swept = now
	}
	cutoff := now.Add(-lim.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.hits, key)
		return nil
	}
	l.hits[key] = kept
	return kept
}

// over reports whether key has used up its tries, without counting a new one.
func (l *limiter) over(key string, lim limit, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key, lim, now)) >= lim.n
}

// hit counts one try for key.
func (l *limiter) hit(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits[key] = append(l.hits[key], now)
}

// allow counts one try for key and reports whether it is within the limit.
func (l *limiter) allow(key string, lim limit, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.recent(key, lim, now)) >= lim.n {
		return false
	}
	l.hits[key] = append(l.hits[key], now)
	return true
}

// clientIP is the visitor's address, used only for the limits above.
//
// Without TRUST_PROXY it is the address that connected to us. Behind a proxy
// or host that forwards requests (TRUST_PROXY=1), it is the last address in
// X-Forwarded-For: the one our own proxy added. Earlier addresses in that
// header come from the visitor and can be made up.
//
// IPv6 visitors usually get a whole block of addresses (a /64), so they are
// counted per block, otherwise one person could rotate through billions.
func (s *Server) clientIP(r *http.Request) string {
	raw := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		raw = host
	}
	if s.Cfg.TrustProxy {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			parts := strings.Split(xff[len(xff)-1], ",")
			raw = strings.TrimSpace(parts[len(parts)-1])
		}
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		return raw
	}
	if ip.To4() == nil {
		return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	return ip.String()
}

// tooMany shows the "please wait a moment" page with status 429.
func (s *Server) tooMany(w http.ResponseWriter, r *http.Request, back string) {
	w.Header().Set("Retry-After", "600")
	s.message(w, r, http.StatusTooManyRequests, "m_slow_t", "m_slow_x", back, "m_back")
}

// limited lets a visitor through only while they are within the limit.
func (s *Server) limited(name string, lim limit, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.rl.allow(name+"|"+s.clientIP(r), lim, s.Now()) {
			s.tooMany(w, r, "/")
			return
		}
		next(w, r)
	}
}
