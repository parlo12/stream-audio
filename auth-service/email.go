package main

import (
	"fmt"
	"log"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
)

// Transactional email via Amazon SES's SMTP interface. Uses only the standard
// library (net/smtp), so no AWS SDK dependency is added. SES on port 587
// upgrades to TLS via STARTTLS, which smtp.SendMail negotiates automatically.
//
// Required env (all must be set, or sending is a no-op that logs a warning so
// the service still boots and runs):
//   SES_SMTP_HOST   e.g. email-smtp.us-east-1.amazonaws.com
//   SES_SMTP_USER   SES SMTP username (IAM-derived, NOT the AWS access key)
//   SES_SMTP_PASS   SES SMTP password
//   SES_SENDER      verified From, e.g. "Narrafied <hello@narrafied.com>"
//   SES_SMTP_PORT   optional, defaults to 587

func emailConfigured() bool {
	return os.Getenv("SES_SMTP_HOST") != "" &&
		os.Getenv("SES_SMTP_USER") != "" &&
		os.Getenv("SES_SMTP_PASS") != "" &&
		os.Getenv("SES_SENDER") != ""
}

// sendEmail delivers a multipart text+HTML message. Returns nil (after logging)
// when SES is unconfigured, so callers in background loops never crash on a
// missing config in a fresh environment.
func sendEmail(to, subject, htmlBody, textBody string) error {
	if !emailConfigured() {
		log.Printf("✉️  email skipped (SES not configured): to=%s subject=%q", to, subject)
		return nil
	}

	host := os.Getenv("SES_SMTP_HOST")
	port := getEnv("SES_SMTP_PORT", "587")
	user := os.Getenv("SES_SMTP_USER")
	pass := os.Getenv("SES_SMTP_PASS")
	sender := os.Getenv("SES_SENDER")

	envelopeFrom := sender
	if parsed, err := mail.ParseAddress(sender); err == nil {
		envelopeFrom = parsed.Address // MAIL FROM wants the bare address
	}

	const boundary = "narrafied-alt-boundary-9f2c"
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", sender)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary)

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(textBody)
	b.WriteString("\r\n\r\n")

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	b.WriteString(htmlBody)
	b.WriteString("\r\n\r\n")

	fmt.Fprintf(&b, "--%s--\r\n", boundary)

	auth := smtp.PlainAuth("", user, pass, host)
	if err := smtp.SendMail(host+":"+port, auth, envelopeFrom, []string{to}, []byte(b.String())); err != nil {
		return fmt.Errorf("ses send to %s: %w", to, err)
	}
	log.Printf("✅ email sent to %s (%q)", to, subject)
	return nil
}
