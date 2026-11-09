// E-Meal Kitchen booking system.
//
// Run it with:   go run .
// Then open:     http://localhost:8080
//
// Settings come from a .env file (see .env.example).
package main

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"emeal-booking/db"
	"emeal-booking/handlers"
	"emeal-booking/mailer"
	"emeal-booking/payments"
	"emeal-booking/schedule"
)

// The HTML templates and CSS are built into the program, so deploying
// means copying a single file.
//
//go:embed templates static
var files embed.FS

func main() {
	loadDotEnv(".env")

	port := env("PORT", "8080")
	cfg := handlers.Config{
		BaseURL:         strings.TrimRight(env("BASE_URL", "http://localhost:"+port), "/"),
		RestaurantName:  env("RESTAURANT_NAME", "E-Meal Kitchen"),
		RestaurantPhone: env("RESTAURANT_PHONE", "0400 796 789"),
		RestaurantEmail: env("RESTAURANT_EMAIL", "info@emeal.ax"),
		Address:         env("RESTAURANT_ADDRESS", "Ålandsvägen 40, 22100 Mariehamn"),
		BusinessID:      env("BUSINESS_ID", ""),
		ManagerEmail:    env("MANAGER_EMAIL", ""),
		ReportEmail:     env("REPORT_EMAIL", ""),
		GoogleReviewURL: env("GOOGLE_REVIEW_URL", ""),
		ManagerPhone:    env("MANAGER_PHONE", ""),
		TableButtons:    env("TABLE_BUTTONS", "") == "1", // off for now
		TableQR:         env("TABLE_QR", "") == "1",      // off for now: one QR, for feedback
		TableDeposit:    env("TABLE_DEPOSIT", "") == "1", // off: booking a table online is free
		AdminUser:       env("ADMIN_USER", "admin"),
		AdminPassword:   env("ADMIN_PASSWORD", ""),
		OwnerUser:       env("OWNER_USER", "owner"),
		OwnerPassword:   env("OWNER_PASSWORD", ""),
		CalendarToken:   env("CALENDAR_TOKEN", ""),
		SessionSecret:   env("SESSION_SECRET", ""),
		RetentionDays:   envInt("DATA_RETENTION_DAYS", 365),
		TrustProxy:      envBool("TRUST_PROXY"),
	}
	pay := &payments.Client{
		SecretKey:     env("STRIPE_SECRET_KEY", ""),
		WebhookSecret: env("STRIPE_WEBHOOK_SECRET", ""),
	}
	mail := &mailer.Mailer{
		Host:     env("SMTP_HOST", ""),
		Port:     env("SMTP_PORT", "587"),
		User:     env("SMTP_USER", ""),
		Password: env("SMTP_PASSWORD", ""),
		From:     env("MAIL_FROM", ""),
	}

	// Safety checks before going live.
	if err := checkSettings(&cfg, pay.Enabled(), envBool("DEMO_MODE")); err != nil {
		log.Fatal(err)
	}
	if pay.Enabled() {
		if pay.WebhookSecret == "" {
			log.Println("Warning: STRIPE_WEBHOOK_SECRET is empty: bookings are only confirmed when the guest returns to the site after paying")
		}
		if strings.HasPrefix(pay.SecretKey, "sk_live_") {
			log.Println("Stripe LIVE mode: real money")
		} else {
			log.Println("Stripe test mode")
		}
	} else {
		log.Println("Demo mode: STRIPE_SECRET_KEY is empty, payments are simulated")
	}
	if !mail.Enabled() {
		log.Println("SMTP not configured: emails are printed here instead of sent")
	}

	store, err := db.Open(env("DATA_FILE", "data/emeal.json"))
	if err != nil {
		log.Fatalf("opening data file: %v", err)
	}
	srv, err := handlers.New(cfg, store, pay, mail, files)
	if err != nil {
		log.Fatalf("loading templates: %v", err)
	}
	// Demo only: DEMO_CLOCK=2026-10-24T18:05 pretends it's that moment, so
	// you can show the restaurant a busy evening at any time of day.
	if c := env("DEMO_CLOCK", ""); c != "" && !pay.Enabled() {
		if start, err := time.ParseInLocation("2006-01-02T15:04", c, schedule.Location); err == nil {
			began := time.Now()
			srv.Now = func() time.Time { return start.Add(time.Since(began)) }
			log.Printf("Demo clock: pretending it's %s", c)
		}
	}

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Open Tables screens are told to hang up when the server stops.
	httpServer.RegisterOnShutdown(srv.StopStreams)

	// Stop cleanly on Ctrl+C so no booking is cut off halfway.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Reminders, the waiting list and privacy clean-up run in the background.
	go srv.RunJobs(ctx)
	go func() {
		log.Printf("Running on %s  (staff login: %s/login)", cfg.BaseURL, cfg.BaseURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

func envBool(key string) bool {
	switch strings.ToLower(env(key, "")) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func envInt(key string, def int) int {
	n, err := strconv.Atoi(env(key, ""))
	if err != nil || n < 0 {
		return def
	}
	return n
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// loadDotEnv reads KEY=VALUE lines from a file into the environment.
// Variables that are already set (e.g. on the hosting server) win.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}
