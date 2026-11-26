// Package sender delivers notifications by email or SMS. Both are optional:
// a sender is nil when its configuration is absent, and callers skip that channel.
package sender

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"bms/internal/config"
)

type Message struct {
	To      string
	Subject string
	Body    string
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// ---------- email ----------

type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	Timeout  time.Duration
}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("header injection rejected")
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	d := net.Dialer{Timeout: timeout}
	var conn net.Conn
	var err error
	if s.Port == 465 {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if s.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		} else if s.User != "" {
			return errors.New("SMTP server does not offer STARTTLS; refusing to send credentials in clear text")
		}
	}
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + s.From + "\r\nTo: " + m.To + "\r\nSubject: " + mime.QEncoding.Encode("utf-8", m.Subject) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		strings.ReplaceAll(m.Body, "\n", "\r\n") + "\r\n"
	if _, err := io.WriteString(w, msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// ---------- SMS ----------

// Webhook posts {to, from, message} as JSON to an SMS gateway with a bearer key.
// Most gateways (or a small adapter in front of one) accept this shape.
type Webhook struct {
	URL    string
	APIKey string
	From   string
	Client *http.Client
}

func (w *Webhook) Send(ctx context.Context, m Message) error {
	text := m.Subject
	if m.Body != "" {
		if text != "" {
			text += ": " + m.Body
		} else {
			text = m.Body
		}
	}
	if len(text) > 480 {
		text = text[:480]
	}
	raw, err := json.Marshal(map[string]string{"to": m.To, "from": w.From, "message": text})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if w.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+w.APIKey)
	}
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("sms gateway returned %d", resp.StatusCode)
	}
	return nil
}

// LogSender logs outbound messages to slog for local development when OUTBOUND_LOG=1.
type LogSender struct {
	Channel string
}

func (l *LogSender) Send(_ context.Context, m Message) error {
	slog.Info("outbound message logged", "channel", l.Channel, "to", m.To, "subject", m.Subject, "body", m.Body)
	return nil
}

// Channels holds the configured customer outbound senders per channel.
type Channels struct {
	Email    Sender
	SMS      Sender
	WhatsApp Sender
}

// For returns the configured sender and provider label for a channel.
func (ch Channels) For(channel string) (Sender, string) {
	var s Sender
	switch channel {
	case "email":
		s = ch.Email
	case "sms":
		s = ch.SMS
	case "whatsapp":
		s = ch.WhatsApp
	}
	if s == nil {
		return nil, ""
	}
	switch s.(type) {
	case *SMTP:
		return s, "smtp"
	case *Webhook:
		return s, "webhook"
	case *LogSender:
		return s, "log"
	default:
		return s, "custom"
	}
}

// Available reports whether a sender is configured for channel.
func (ch Channels) Available(channel string) bool {
	s, _ := ch.For(channel)
	return s != nil
}

// FromConfig builds the configured email and SMS senders. A channel without configuration is nil.
func FromConfig(c config.Config) (email, sms Sender) {
	if c.SMTPHost != "" {
		email = &SMTP{Host: c.SMTPHost, Port: c.SMTPPort, User: c.SMTPUser, Password: c.SMTPPassword, From: c.SMTPFrom, Timeout: 15 * time.Second}
	}
	if c.SMSWebhookURL != "" {
		sms = &Webhook{URL: c.SMSWebhookURL, APIKey: c.SMSAPIKey, From: c.SMSSender}
	}
	return email, sms
}

// OutboundFromConfig builds customer outbound senders from config.
// When OUTBOUND_LOG=1, any unconfigured channel uses LogSender for local dev.
func OutboundFromConfig(c config.Config) Channels {
	email, sms := FromConfig(c)
	var wa Sender
	if c.WhatsAppWebhookURL != "" {
		wa = &Webhook{URL: c.WhatsAppWebhookURL, APIKey: c.WhatsAppAPIKey, From: c.WhatsAppSender}
	}
	if c.OutboundLog {
		if email == nil {
			email = &LogSender{Channel: "email"}
		}
		if sms == nil {
			sms = &LogSender{Channel: "sms"}
		}
		if wa == nil {
			wa = &LogSender{Channel: "whatsapp"}
		}
	}
	return Channels{Email: email, SMS: sms, WhatsApp: wa}
}
