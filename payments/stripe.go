// Package payments talks to Stripe using its plain HTTP API.
//
// We only need four things from Stripe:
//  1. create a Checkout page where the guest pays (a deposit, or their part
//     of the bill at the table)
//  2. look up a Checkout page to see if it was paid
//  3. cancel a Checkout page nobody finished
//  4. refund a payment
//
// plus checking that webhook messages really come from Stripe.
package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a tiny Stripe client. An empty SecretKey means "not configured":
// the site then runs in demo mode with a fake payment page.
type Client struct {
	SecretKey     string
	WebhookSecret string
	APIBase       string // defaults to https://api.stripe.com
	HTTP          *http.Client
}

// Enabled reports whether real Stripe payments are configured.
func (c *Client) Enabled() bool { return c != nil && c.SecretKey != "" }

// Session is the part of a Stripe Checkout Session we care about.
type Session struct {
	ID                string `json:"id"`
	URL               string `json:"url"`
	Status            string `json:"status"`         // open, complete, expired
	PaymentStatus     string `json:"payment_status"` // paid, unpaid
	ClientReferenceID string `json:"client_reference_id"`
	PaymentIntent     string `json:"payment_intent"`
}

// CheckoutParams describes one deposit payment.
type CheckoutParams struct {
	Reference     string // our booking ref, sent back to us in webhooks
	Description   string // shown to the guest on the payment page
	AmountCents   int
	TipCents      int    // a tip, as its own line on the payment page (0: none)
	TipLabel      string // e.g. "Tip for the staff (10 %)"
	CustomerEmail string
	Locale        string // "sv" or "en": language of Stripe's payment page
	SuccessURL    string
	CancelURL     string
	ExpiresAt     time.Time // Stripe allows 30 min – 24 h from now
}

// CreateCheckoutSession creates the Stripe payment page.
func (c *Client) CreateCheckoutSession(ctx context.Context, p CheckoutParams) (*Session, error) {
	f := url.Values{}
	f.Set("mode", "payment")
	f.Set("client_reference_id", p.Reference)
	f.Set("metadata[booking_ref]", p.Reference)
	if p.CustomerEmail != "" { // guests paying at the table don't give one
		f.Set("customer_email", p.CustomerEmail)
		f.Set("payment_intent_data[receipt_email]", p.CustomerEmail) // Stripe emails a receipt
	}
	f.Set("payment_intent_data[description]", p.Description)
	f.Set("payment_intent_data[metadata][booking_ref]", p.Reference)
	f.Set("line_items[0][quantity]", "1")
	f.Set("line_items[0][price_data][currency]", "eur")
	f.Set("line_items[0][price_data][unit_amount]", strconv.Itoa(p.AmountCents))
	f.Set("line_items[0][price_data][product_data][name]", p.Description)
	if p.TipCents > 0 {
		f.Set("line_items[1][quantity]", "1")
		f.Set("line_items[1][price_data][currency]", "eur")
		f.Set("line_items[1][price_data][unit_amount]", strconv.Itoa(p.TipCents))
		f.Set("line_items[1][price_data][product_data][name]", p.TipLabel)
	}
	f.Set("success_url", p.SuccessURL)
	f.Set("cancel_url", p.CancelURL)
	f.Set("expires_at", strconv.FormatInt(p.ExpiresAt.Unix(), 10))
	if p.Locale != "" {
		f.Set("locale", p.Locale)
	}
	var s Session
	err := c.do(ctx, http.MethodPost, "/v1/checkout/sessions", f, "checkout-"+p.Reference, &s)
	return &s, err
}

// GetCheckoutSession looks up a payment page, e.g. to check if it was paid.
func (c *Client) GetCheckoutSession(ctx context.Context, id string) (*Session, error) {
	var s Session
	err := c.do(ctx, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(id), nil, "", &s)
	return &s, err
}

// ExpireCheckoutSession cancels a payment page nobody finished, so it can't
// be paid any more. Stripe refuses if it was paid (or expired) meanwhile:
// look it up then to see which.
func (c *Client) ExpireCheckoutSession(ctx context.Context, id string) (*Session, error) {
	var s Session
	err := c.do(ctx, http.MethodPost, "/v1/checkout/sessions/"+url.PathEscape(id)+"/expire", url.Values{}, "", &s)
	return &s, err
}

// Refund gives the whole deposit back.
func (c *Client) Refund(ctx context.Context, paymentIntentID string) error {
	f := url.Values{}
	f.Set("payment_intent", paymentIntentID)
	return c.do(ctx, http.MethodPost, "/v1/refunds", f, "refund-"+paymentIntentID, nil)
}

func (c *Client) do(ctx context.Context, method, path string, form url.Values, idemKey string, out any) error {
	base := c.APIBase
	if base == "" {
		base = "https://api.stripe.com"
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.SecretKey)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idemKey != "" {
		// If we retry the same request, Stripe won't charge or refund twice.
		req.Header.Set("Idempotency-Key", idemKey)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("stripe: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("stripe: %w", err)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("stripe %s %s: %d %s", method, path, resp.StatusCode, e.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// ---------- Webhooks ----------

// Event is a message Stripe sends to /stripe/webhook.
type Event struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

var ErrBadSignature = errors.New("invalid Stripe signature")

// signatureTolerance is how old a webhook may be (protects against replays).
const signatureTolerance = 5 * time.Minute

// ParseWebhook checks the Stripe-Signature header and decodes the event.
// Without this check anyone could POST "it's paid!" to our server.
func ParseWebhook(payload []byte, sigHeader, secret string, now time.Time) (*Event, error) {
	if secret == "" {
		return nil, errors.New("STRIPE_WEBHOOK_SECRET is not set")
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(sigHeader, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == "" || len(sigs) == 0 {
		return nil, ErrBadSignature
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, ErrBadSignature
	}
	age := now.Sub(time.Unix(unix, 0))
	if age > signatureTolerance || age < -signatureTolerance {
		return nil, fmt.Errorf("%w: timestamp too old", ErrBadSignature)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(payload)
	expected := mac.Sum(nil)
	valid := false
	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err == nil && hmac.Equal(got, expected) {
			valid = true
			break
		}
	}
	if !valid {
		return nil, ErrBadSignature
	}
	var ev Event
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// SignForTest builds a valid Stripe-Signature header (used by tests).
func SignForTest(payload []byte, secret string, t time.Time) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(payload)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
