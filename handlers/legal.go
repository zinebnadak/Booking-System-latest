package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"emeal-booking/schedule"
)

// legalPage shows the booking terms or the privacy notice, in the guest's
// language. The text itself is in templates/legal.html.
func (s *Server) legalPage(page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lang := langOf(r)
		t := tr(lang)
		s.render(w, r, "legal", 200, M{
			"Title":           t["title_"+page],
			"Page":            page,
			"RetentionDays":   s.Cfg.RetentionDays,
			"RetentionText":   daysText(lang, s.Cfg.RetentionDays),
			"NoShowAfterText": fmt.Sprintf(t["n_minutes"], int(schedule.NoShowAfter.Minutes())),
		})
	}
}

// daysText: 365 -> "1 year" / "1 år", 90 -> "3 months", 10 -> "10 days".
func daysText(lang string, days int) string {
	t := tr(lang)
	switch {
	case days >= 365 && days%365 == 0:
		if days == 365 {
			return t["n_year"]
		}
		return fmt.Sprintf(t["n_years"], days/365)
	case days >= 30 && days%30 == 0:
		return fmt.Sprintf(t["n_months"], days/30)
	}
	return fmt.Sprintf(t["n_days"], days)
}

// manifest lets people add the site to their phone's home screen like an
// app: one for guests (opens the start page), one for staff (opens today's
// bookings).
func (s *Server) manifest(staff bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name, short, start, bg, theme := s.Cfg.RestaurantName, "E-Meal", "/", "#171d1d", "#171d1d"
		id := "/"
		if staff {
			name, short, start, bg, theme = s.Cfg.RestaurantName+" Staff", "E-Meal Staff", "/admin/orders", "#171d1d", "#171d1d"
			id = "/admin/day" // kept: phones that already added the app keep it
		}
		icon := func(file, size, purpose string) map[string]string {
			return map[string]string{"src": "/static/icons/" + file + "?v=" + s.assetVer, "sizes": size, "type": "image/png", "purpose": purpose}
		}
		prefix := "icon"
		if staff {
			prefix = "staff"
		}
		m := map[string]any{
			"id": id, "name": name, "short_name": short, "start_url": start, "scope": "/",
			"display": "standalone", "background_color": bg, "theme_color": theme,
			"icons": []map[string]string{
				icon(prefix+"-192.png", "192x192", "any"),
				icon(prefix+"-512.png", "512x512", "any"),
				icon(prefix+"-maskable-512.png", "512x512", "maskable"),
			},
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		json.NewEncoder(w).Encode(m)
	}
}
