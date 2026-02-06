// Package notify provides notification support for ralphex plan execution results.
package notify

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"strings"
	"time"

	ntfy "github.com/go-pkgz/notify"
)

// Params holds configuration for creating a notification Service.
// Embedded directly in Config struct — no intermediate mapping needed.
type Params struct {
	Channels      []string
	OnError       bool
	OnComplete    bool
	TimeoutMs     int
	TelegramToken string
	TelegramChat  string
	SlackToken    string
	SlackChannel  string
	SMTPHost      string
	SMTPPort      int
	SMTPUsername  string
	SMTPPassword  string
	SMTPStartTLS  bool
	EmailFrom     string
	EmailTo       []string
	WebhookURLs   []string
	CustomScript  string
}

// Service orchestrates sending notifications through configured channels.
type Service struct {
	channels   []channel      // paired notifier + destination
	custom     *customChannel // optional custom script channel
	onError    bool
	onComplete bool
	timeoutMs  int
	hostname   string // resolved once at creation via os.Hostname()
	log        logger
}

// channel pairs a notifier with its destination URI.
type channel struct {
	notifier   ntfy.Notifier
	dest       string
	htmlEscape bool // true for channels that use HTML parse mode (e.g., telegram)
}

// logger interface for dependency injection.
type logger interface {
	Print(format string, args ...any)
}

// Result holds completion data for notifications.
type Result struct {
	Status    string `json:"status"` // "success" or "failure"
	Mode      string `json:"mode"`
	PlanFile  string `json:"plan_file"`
	Branch    string `json:"branch"`
	Duration  string `json:"duration"`
	Files     int    `json:"files"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Error     string `json:"error,omitempty"`
}

// New creates a notification Service from the given Params.
// returns nil, nil if no channels are configured, enabling callers to skip nil checks via nil-safe Send.
// validates required fields per channel and returns an error for misconfigured channels.
func New(p Params, log logger) (*Service, error) {
	if len(p.Channels) == 0 {
		return nil, nil //nolint:nilnil // nil,nil signals "no channels configured" — callers use nil-safe Send
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	svc := &Service{
		onError:    p.OnError,
		onComplete: p.OnComplete,
		timeoutMs:  p.TimeoutMs,
		hostname:   hostname,
		log:        log,
	}
	if svc.timeoutMs <= 0 {
		svc.timeoutMs = 10000
	}

	for _, ch := range p.Channels {
		switch strings.TrimSpace(strings.ToLower(ch)) {
		case "telegram":
			if p.TelegramToken == "" {
				return nil, errors.New("telegram channel: notify_telegram_token is required")
			}
			if p.TelegramChat == "" {
				return nil, errors.New("telegram channel: notify_telegram_chat is required")
			}
			c, cErr := telegramChannelMaker(p)
			if cErr != nil {
				// telegram init makes a live API call to verify the bot token;
				// if the network/API is unavailable, skip the channel instead of blocking
				// startup — notifications are best-effort.
				// redact the token from the error to avoid leaking it in logs
				errMsg := strings.ReplaceAll(cErr.Error(), p.TelegramToken, "[REDACTED]")
				log.Print("[WARN] telegram channel disabled: %s", errMsg)
				continue
			}
			svc.channels = append(svc.channels, c)
		case "email":
			c, cErr := makeEmailChannel(p)
			if cErr != nil {
				return nil, fmt.Errorf("email channel: %w", cErr)
			}
			svc.channels = append(svc.channels, c)
		case "slack":
			c, cErr := makeSlackChannel(p)
			if cErr != nil {
				return nil, fmt.Errorf("slack channel: %w", cErr)
			}
			svc.channels = append(svc.channels, c)
		case "webhook":
			chs, cErr := makeWebhookChannels(p)
			if cErr != nil {
				return nil, fmt.Errorf("webhook channel: %w", cErr)
			}
			svc.channels = append(svc.channels, chs...)
		case "custom":
			if p.CustomScript == "" {
				return nil, errors.New("custom channel: notify_custom_script is required")
			}
			svc.custom = newCustomChannel(p.CustomScript)
		default:
			return nil, fmt.Errorf("unknown notification channel: %q", ch)
		}
	}

	if len(svc.channels) == 0 && svc.custom == nil {
		log.Print("[WARN] all notification channels were disabled due to initialization errors")
	}

	return svc, nil
}

// Send sends a notification for the given result. nil-safe on receiver — callers don't need nil checks.
// checks onError/onComplete flags and sends to all configured channels.
// errors are logged but never returned (best-effort).
func (s *Service) Send(ctx context.Context, r Result) {
	if s == nil {
		return
	}

	// filter based on result status
	if r.Status == "success" && !s.onComplete {
		return
	}
	if r.Status == "failure" && !s.onError {
		return
	}

	msg := s.formatMessage(r)

	timeout := time.Duration(s.timeoutMs) * time.Millisecond
	sendCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// send to go-pkgz/notify channels
	for _, ch := range s.channels {
		text := msg
		if ch.htmlEscape {
			text = html.EscapeString(msg)
		}
		if err := ch.notifier.Send(sendCtx, ch.dest, text); err != nil {
			s.log.Print("[WARN] notification failed for %s: %v", ch.notifier, err)
		}
	}

	// send to custom script channel
	if s.custom != nil {
		if err := s.custom.send(sendCtx, r); err != nil {
			s.log.Print("[WARN] custom notification failed: %v", err)
		}
	}
}

// formatMessage creates a plain text notification message from the result.
func (s *Service) formatMessage(r Result) string {
	var b strings.Builder

	if r.Status == "success" {
		fmt.Fprintf(&b, "ralphex completed on %s\n", s.hostname)
	} else {
		fmt.Fprintf(&b, "ralphex failed on %s\n", s.hostname)
	}

	b.WriteString("\n")

	if r.PlanFile != "" {
		fmt.Fprintf(&b, "plan:     %s\n", r.PlanFile)
	}
	if r.Branch != "" {
		fmt.Fprintf(&b, "branch:   %s\n", r.Branch)
	}
	if r.Mode != "" {
		fmt.Fprintf(&b, "mode:     %s\n", r.Mode)
	}
	if r.Duration != "" {
		fmt.Fprintf(&b, "duration: %s\n", r.Duration)
	}

	if r.Status == "success" {
		fmt.Fprintf(&b, "changes:  %d files (+%d/-%d lines)\n", r.Files, r.Additions, r.Deletions)
	}

	if r.Error != "" {
		fmt.Fprintf(&b, "error:    %s\n", r.Error)
	}

	return b.String()
}

// telegramChannelMaker creates a telegram notifier and destination.
// overridden in tests to avoid live API calls.
var telegramChannelMaker = makeTelegramChannel

// makeTelegramChannel creates a telegram notifier and destination.
// uses ntfy.Telegram with the token, sending to telegram:<chat>?parseMode=HTML.
// caller must validate that TelegramToken and TelegramChat are non-empty before calling.
func makeTelegramChannel(p Params) (channel, error) {
	tg, err := ntfy.NewTelegram(ntfy.TelegramParams{Token: p.TelegramToken})
	if err != nil {
		return channel{}, fmt.Errorf("create telegram notifier: %w", err)
	}

	dest := fmt.Sprintf("telegram:%s?parseMode=HTML", p.TelegramChat)
	return channel{notifier: tg, dest: dest, htmlEscape: true}, nil
}

// makeEmailChannel creates an email notifier and destination.
func makeEmailChannel(p Params) (channel, error) {
	if p.SMTPHost == "" {
		return channel{}, errors.New("notify_smtp_host is required")
	}
	if p.EmailFrom == "" {
		return channel{}, errors.New("notify_email_from is required")
	}
	if len(p.EmailTo) == 0 {
		return channel{}, errors.New("notify_email_to is required")
	}

	em := ntfy.NewEmail(ntfy.SMTPParams{
		Host:     p.SMTPHost,
		Port:     p.SMTPPort,
		Username: p.SMTPUsername,
		Password: p.SMTPPassword,
		StartTLS: p.SMTPStartTLS,
	})

	// build mailto: destination with all recipients, from, and subject
	to := strings.Join(p.EmailTo, ",")
	dest := fmt.Sprintf("mailto:%s?from=%s&subject=%s",
		to,
		url.QueryEscape(p.EmailFrom),
		url.QueryEscape("ralphex notification"),
	)

	return channel{notifier: em, dest: dest}, nil
}

// makeSlackChannel creates a slack notifier and destination.
func makeSlackChannel(p Params) (channel, error) {
	if p.SlackToken == "" {
		return channel{}, errors.New("notify_slack_token is required")
	}
	if p.SlackChannel == "" {
		return channel{}, errors.New("notify_slack_channel is required")
	}

	sl := ntfy.NewSlack(p.SlackToken)
	dest := "slack:" + p.SlackChannel
	return channel{notifier: sl, dest: dest}, nil
}

// makeWebhookChannels creates webhook notifiers for each configured URL.
func makeWebhookChannels(p Params) ([]channel, error) {
	if len(p.WebhookURLs) == 0 {
		return nil, errors.New("notify_webhook_urls is required")
	}

	wh := ntfy.NewWebhook(ntfy.WebhookParams{})
	var channels []channel
	for _, u := range p.WebhookURLs {
		channels = append(channels, channel{notifier: wh, dest: u})
	}
	return channels, nil
}
