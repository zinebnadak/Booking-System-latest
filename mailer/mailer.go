// Package mailer sends plain-text emails over SMTP.
//
// Works with any email provider that gives SMTP details (Gmail with an app
// password, Outlook, Brevo, Resend, Mailgun, Zoho...). If SMTP isn't
// configured, emails are printed in the terminal instead, which is handy
// while developing.
package mailer

import (
	"crypto/tls"
	"fmt"
	"log"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type Mailer struct {
	Host     string // e.g. smtp.gmail.com
	Port     string // 587 (STARTTLS) or 465 (TLS)
	User     string
	Password string
	From     string // e.g. "Ozmo <bokning@example.com>"

	// Capture, if set, receives every email instead of sending it. The
	// tests use it to check what guests and the manager are told.
	Capture func(to, subject, body string)
}

func (m *Mailer) Enabled() bool { return m != nil && m.Host != "" }

// Send sends one email. Without SMTP settings it just logs the email.
func (m *Mailer) Send(to, subject, body string) error {
	if strings.TrimSpace(to) == "" {
		return nil
	}
	if m.Capture != nil {
		m.Capture(to, subject, body)
		return nil
	}
	if !m.Enabled() {
		log.Printf("(email not configured, printing instead)\nTo: %s\nSubject: %s\n\n%s\n", to, subject, body)
		return nil
	}
	from, err := mail.ParseAddress(m.From)
	if err != nil {
		return fmt.Errorf("MAIL_FROM: %w", err)
	}
	msg := buildMessage(m.From, to, subject, body)

	addr := net.JoinHostPort(m.Host, m.Port)
	var auth smtp.Auth
	if m.User != "" {
		auth = smtp.PlainAuth("", m.User, m.Password, m.Host)
	}
	if m.Port == "465" {
		return sendImplicitTLS(addr, m.Host, auth, from.Address, to, msg)
	}
	// Port 587/25: SendMail upgrades to TLS with STARTTLS automatically.
	return smtp.SendMail(addr, auth, from.Address, []string{to}, msg)
}

// SendAsync sends in the background so the guest never waits for email.
func (m *Mailer) SendAsync(to, subject, body string) {
	if m.Capture != nil {
		m.Send(to, subject, body) // tests: right away, in order
		return
	}
	go func() {
		if err := m.Send(to, subject, body); err != nil {
			log.Printf("email to %s failed: %v", to, err)
		}
	}()
}

func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return []byte(b.String())
}

func sendImplicitTLS(addr, host string, auth smtp.Auth, from, to string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
