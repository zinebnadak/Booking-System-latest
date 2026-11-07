package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"

	"emeal-booking/handlers"
)

// Minimum lengths for the secrets in .env when the site is on the internet.
const (
	minPasswordLen      = 12
	minCalendarTokenLen = 24
)

// isLocal reports whether the site only runs on this computer (localhost),
// where the easy demo logins are harmless.
func isLocal(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	switch h := u.Hostname(); h {
	case "localhost":
		return true
	default:
		ip := net.ParseIP(h)
		return ip != nil && ip.IsLoopback()
	}
}

// checkSettings refuses settings that would leave the site open, and fills in
// the demo logins on a local computer. It runs once when the program starts.
//
//   - On localhost without Stripe (a demo on your own computer), an empty
//     manager password becomes admin/admin.
//   - On a public address, the manager password must be set and long (and
//     the optional second manager login, OWNER_*, too if it's set). Demo mode (fake payments anyone can click through) is only
//     allowed there if DEMO_MODE=1 says it's on purpose.
//   - A calendar link token that is too short to be secret is switched off.
func checkSettings(cfg *handlers.Config, stripe, demoMode bool) error {
	local := isLocal(cfg.BaseURL)
	if !local && !stripe && !demoMode {
		return errors.New("STRIPE_SECRET_KEY is empty, so payments would be fake on a public site. " +
			"Set the Stripe keys, or set DEMO_MODE=1 if this public site is only a demo")
	}
	if local && !stripe {
		if cfg.AdminPassword == "" {
			cfg.AdminPassword = "admin"
			log.Println("Manager login for demo mode: admin / admin")
		}
	}
	if cfg.AdminPassword == "" {
		return errors.New("ADMIN_PASSWORD must be set (see .env.example)")
	}
	if !local {
		for _, p := range []struct{ name, val string }{
			{"ADMIN_PASSWORD", cfg.AdminPassword}, {"OWNER_PASSWORD", cfg.OwnerPassword},
		} {
			if p.val != "" && len([]rune(p.val)) < minPasswordLen {
				return fmt.Errorf("%s is too short: use at least %d characters (make one with: openssl rand -base64 18)", p.name, minPasswordLen)
			}
		}
		if cfg.OwnerPassword != "" && strings.EqualFold(cfg.AdminUser, cfg.OwnerUser) {
			return errors.New("ADMIN_USER and OWNER_USER must be different")
		}
		if strings.HasPrefix(cfg.BaseURL, "http://") {
			log.Println("Warning: BASE_URL starts with http://, so passwords and guests' details travel unencrypted. Use https://")
		}
	}
	if cfg.CalendarToken != "" && len(cfg.CalendarToken) < minCalendarTokenLen {
		log.Printf("Warning: CALENDAR_TOKEN is shorter than %d characters, so the calendar link is switched off (make one with: openssl rand -hex 16)", minCalendarTokenLen)
		cfg.CalendarToken = ""
	}
	return nil
}
