package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockNotifier implements ntfy.Notifier for testing.
type mockNotifier struct {
	schema string
	mu     sync.Mutex
	calls  []sendCall
	err    error
}

type sendCall struct {
	dest string
	text string
}

func (m *mockNotifier) Send(_ context.Context, dest, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, sendCall{dest: dest, text: text})
	return m.err
}

func (m *mockNotifier) Schema() string { return m.schema }
func (m *mockNotifier) String() string { return "mock-" + m.schema }

func (m *mockNotifier) getCalls() []sendCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]sendCall, len(m.calls))
	copy(res, m.calls)
	return res
}

// mockLogger captures log output for testing.
type mockLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (l *mockLogger) Print(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, fmt.Sprintf(format, args...))
}

func (l *mockLogger) getMsgs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	res := make([]string, len(l.msgs))
	copy(res, l.msgs)
	return res
}

func TestNew(t *testing.T) {
	t.Run("empty channels returns nil", func(t *testing.T) {
		svc, err := New(Params{}, &mockLogger{})
		require.NoError(t, err)
		assert.Nil(t, svc)
	})

	t.Run("unknown channel returns error", func(t *testing.T) {
		_, err := New(Params{Channels: []string{"unknown"}}, &mockLogger{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown notification channel")
	})

	t.Run("webhook channel valid config", func(t *testing.T) {
		svc, err := New(Params{
			Channels:    []string{"webhook"},
			OnComplete:  true,
			WebhookURLs: []string{"https://example.com/hook"},
		}, &mockLogger{})
		require.NoError(t, err)
		require.NotNil(t, svc)
		assert.Len(t, svc.channels, 1)
		assert.True(t, svc.onComplete)
	})

	t.Run("webhook channel missing urls", func(t *testing.T) {
		_, err := New(Params{Channels: []string{"webhook"}}, &mockLogger{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "notify_webhook_urls is required")
	})

	t.Run("email channel missing host", func(t *testing.T) {
		_, err := New(Params{Channels: []string{"email"}}, &mockLogger{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "notify_smtp_host is required")
	})

	t.Run("email channel missing from", func(t *testing.T) {
		_, err := New(Params{
			Channels: []string{"email"},
			SMTPHost: "smtp.example.com",
		}, &mockLogger{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "notify_email_from is required")
	})

	t.Run("email channel missing to", func(t *testing.T) {
		_, err := New(Params{
			Channels:  []string{"email"},
			SMTPHost:  "smtp.example.com",
			EmailFrom: "from@example.com",
		}, &mockLogger{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "notify_email_to is required")
	})

	t.Run("email channel valid config", func(t *testing.T) {
		svc, err := New(Params{
			Channels:  []string{"email"},
			OnError:   true,
			SMTPHost:  "smtp.example.com",
			SMTPPort:  587,
			EmailFrom: "from@example.com",
			EmailTo:   []string{"to@example.com"},
		}, &mockLogger{})
		require.NoError(t, err)
		require.NotNil(t, svc)
		assert.Len(t, svc.channels, 1)
	})

	t.Run("slack channel missing token", func(t *testing.T) {
		_, err := New(Params{Channels: []string{"slack"}}, &mockLogger{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "notify_slack_token is required")
	})

	t.Run("slack channel missing channel", func(t *testing.T) {
		_, err := New(Params{
			Channels:   []string{"slack"},
			SlackToken: "xoxb-token",
		}, &mockLogger{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "notify_slack_channel is required")
	})

	t.Run("slack channel valid config", func(t *testing.T) {
		svc, err := New(Params{
			Channels:     []string{"slack"},
			OnComplete:   true,
			SlackToken:   "xoxb-token",
			SlackChannel: "general",
		}, &mockLogger{})
		require.NoError(t, err)
		require.NotNil(t, svc)
		assert.Len(t, svc.channels, 1)
	})

	t.Run("telegram channel missing token", func(t *testing.T) {
		_, err := New(Params{Channels: []string{"telegram"}}, &mockLogger{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "notify_telegram_token is required")
	})

	t.Run("telegram channel missing chat", func(t *testing.T) {
		_, err := New(Params{
			Channels:      []string{"telegram"},
			TelegramToken: "bot-token",
		}, &mockLogger{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "notify_telegram_chat is required")
	})

	t.Run("telegram channel api failure logs warning and skips", func(t *testing.T) {
		orig := telegramChannelMaker
		telegramChannelMaker = func(p Params) (channel, error) {
			return channel{}, errors.New("can't retrieve bot info from Telegram API: 401 Unauthorized")
		}
		t.Cleanup(func() { telegramChannelMaker = orig })

		log := &mockLogger{}
		svc, err := New(Params{
			Channels:      []string{"telegram"},
			TelegramToken: "bot-token",
			TelegramChat:  "-123",
		}, log)
		require.NoError(t, err, "api failure should not return error")
		require.NotNil(t, svc)
		assert.Empty(t, svc.channels, "telegram channel should be skipped")
		msgs := log.getMsgs()
		require.Len(t, msgs, 2)
		assert.Contains(t, msgs[0], "[WARN] telegram channel disabled")
		assert.Contains(t, msgs[1], "all notification channels were disabled")
	})

	t.Run("telegram channel api failure redacts token from log", func(t *testing.T) {
		orig := telegramChannelMaker
		telegramChannelMaker = func(p Params) (channel, error) {
			return channel{}, fmt.Errorf("request to https://api.telegram.org/bot%s/getMe failed", p.TelegramToken)
		}
		t.Cleanup(func() { telegramChannelMaker = orig })

		log := &mockLogger{}
		_, err := New(Params{
			Channels:      []string{"telegram"},
			TelegramToken: "123456:ABC-secret-token",
			TelegramChat:  "-123",
		}, log)
		require.NoError(t, err)
		msgs := log.getMsgs()
		require.NotEmpty(t, msgs)
		assert.NotContains(t, msgs[0], "123456:ABC-secret-token", "token must be redacted from log output")
		assert.Contains(t, msgs[0], "[REDACTED]")
	})

	t.Run("custom channel missing script", func(t *testing.T) {
		_, err := New(Params{Channels: []string{"custom"}}, &mockLogger{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "notify_custom_script is required")
	})

	t.Run("custom channel valid config", func(t *testing.T) {
		svc, err := New(Params{
			Channels:     []string{"custom"},
			OnComplete:   true,
			CustomScript: "/usr/local/bin/notify.sh",
		}, &mockLogger{})
		require.NoError(t, err)
		require.NotNil(t, svc)
		assert.NotNil(t, svc.custom)
	})

	t.Run("default timeout", func(t *testing.T) {
		svc, err := New(Params{
			Channels:    []string{"webhook"},
			WebhookURLs: []string{"https://example.com"},
		}, &mockLogger{})
		require.NoError(t, err)
		assert.Equal(t, 10000, svc.timeoutMs)
	})

	t.Run("custom timeout", func(t *testing.T) {
		svc, err := New(Params{
			Channels:    []string{"webhook"},
			WebhookURLs: []string{"https://example.com"},
			TimeoutMs:   5000,
		}, &mockLogger{})
		require.NoError(t, err)
		assert.Equal(t, 5000, svc.timeoutMs)
	})

	t.Run("multiple webhook urls", func(t *testing.T) {
		svc, err := New(Params{
			Channels:    []string{"webhook"},
			WebhookURLs: []string{"https://a.com", "https://b.com"},
		}, &mockLogger{})
		require.NoError(t, err)
		assert.Len(t, svc.channels, 2)
	})
}

func TestService_Send(t *testing.T) {
	t.Run("nil receiver is no-op", func(t *testing.T) {
		var svc *Service
		svc.Send(context.Background(), Result{Status: "success"})
		// should not panic
	})

	t.Run("success sends to channels when onComplete is true", func(t *testing.T) {
		mock := &mockNotifier{schema: "http"}
		log := &mockLogger{}
		svc := &Service{
			channels:   []channel{{notifier: mock, dest: "https://example.com/hook"}},
			onComplete: true,
			onError:    true,
			timeoutMs:  5000,
			hostname:   "test-host",
			log:        log,
		}
		svc.Send(context.Background(), Result{Status: "success", PlanFile: "plan.md", Branch: "feat", Mode: "full", Duration: "5m"})
		calls := mock.getCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, "https://example.com/hook", calls[0].dest)
		assert.Contains(t, calls[0].text, "ralphex completed on test-host")
	})

	t.Run("success skipped when onComplete is false", func(t *testing.T) {
		mock := &mockNotifier{schema: "http"}
		log := &mockLogger{}
		svc := &Service{
			channels:   []channel{{notifier: mock, dest: "https://example.com/hook"}},
			onComplete: false,
			onError:    true,
			timeoutMs:  5000,
			hostname:   "test-host",
			log:        log,
		}
		svc.Send(context.Background(), Result{Status: "success"})
		assert.Empty(t, mock.getCalls())
	})

	t.Run("failure sends when onError is true", func(t *testing.T) {
		mock := &mockNotifier{schema: "http"}
		log := &mockLogger{}
		svc := &Service{
			channels:   []channel{{notifier: mock, dest: "https://example.com/hook"}},
			onComplete: false,
			onError:    true,
			timeoutMs:  5000,
			hostname:   "test-host",
			log:        log,
		}
		svc.Send(context.Background(), Result{Status: "failure", Error: "something broke"})
		calls := mock.getCalls()
		require.Len(t, calls, 1)
		assert.Contains(t, calls[0].text, "ralphex failed on test-host")
	})

	t.Run("failure skipped when onError is false", func(t *testing.T) {
		mock := &mockNotifier{schema: "http"}
		log := &mockLogger{}
		svc := &Service{
			channels:   []channel{{notifier: mock, dest: "https://example.com/hook"}},
			onComplete: true,
			onError:    false,
			timeoutMs:  5000,
			hostname:   "test-host",
			log:        log,
		}
		svc.Send(context.Background(), Result{Status: "failure"})
		assert.Empty(t, mock.getCalls())
	})

	t.Run("notifier errors are logged not returned", func(t *testing.T) {
		mock := &mockNotifier{schema: "http", err: errors.New("network error")}
		log := &mockLogger{}
		svc := &Service{
			channels:   []channel{{notifier: mock, dest: "https://example.com/hook"}},
			onComplete: true,
			timeoutMs:  5000,
			hostname:   "test-host",
			log:        log,
		}
		svc.Send(context.Background(), Result{Status: "success"})
		msgs := log.getMsgs()
		require.Len(t, msgs, 1)
		assert.Contains(t, msgs[0], "notification failed")
		assert.Contains(t, msgs[0], "network error")
	})

	t.Run("multiple channels all receive notification", func(t *testing.T) {
		mock1 := &mockNotifier{schema: "http"}
		mock2 := &mockNotifier{schema: "slack"}
		log := &mockLogger{}
		svc := &Service{
			channels: []channel{
				{notifier: mock1, dest: "https://example.com/hook"},
				{notifier: mock2, dest: "slack:general"},
			},
			onComplete: true,
			timeoutMs:  5000,
			hostname:   "test-host",
			log:        log,
		}
		svc.Send(context.Background(), Result{Status: "success"})
		assert.Len(t, mock1.getCalls(), 1)
		assert.Len(t, mock2.getCalls(), 1)
	})

	t.Run("html entities escaped for telegram channel", func(t *testing.T) {
		tgMock := &mockNotifier{schema: "telegram"}
		plainMock := &mockNotifier{schema: "http"}
		log := &mockLogger{}
		svc := &Service{
			channels: []channel{
				{notifier: tgMock, dest: "telegram:-100123?parseMode=HTML", htmlEscape: true},
				{notifier: plainMock, dest: "https://example.com/hook"},
			},
			onError:   true,
			timeoutMs: 5000,
			hostname:  "test-host",
			log:       log,
		}
		svc.Send(context.Background(), Result{
			Status: "failure",
			Branch: "feature/<fix>&test",
			Error:  "error: <nil>",
		})

		// telegram channel should get HTML-escaped content
		tgCalls := tgMock.getCalls()
		require.Len(t, tgCalls, 1)
		assert.Contains(t, tgCalls[0].text, "feature/&lt;fix&gt;&amp;test")
		assert.Contains(t, tgCalls[0].text, "error: &lt;nil&gt;")

		// plain channel should get raw content
		plainCalls := plainMock.getCalls()
		require.Len(t, plainCalls, 1)
		assert.Contains(t, plainCalls[0].text, "feature/<fix>&test")
		assert.Contains(t, plainCalls[0].text, "error: <nil>")
	})
}

func TestService_FormatMessage(t *testing.T) {
	svc := &Service{hostname: "build-server"}

	t.Run("success message", func(t *testing.T) {
		msg := svc.formatMessage(Result{
			Status:    "success",
			PlanFile:  "docs/plans/add-auth.md",
			Branch:    "add-auth",
			Mode:      "full",
			Duration:  "12m 34s",
			Files:     8,
			Additions: 142,
			Deletions: 23,
		})
		assert.Contains(t, msg, "ralphex completed on build-server")
		assert.Contains(t, msg, "plan:     docs/plans/add-auth.md")
		assert.Contains(t, msg, "branch:   add-auth")
		assert.Contains(t, msg, "mode:     full")
		assert.Contains(t, msg, "duration: 12m 34s")
		assert.Contains(t, msg, "changes:  8 files (+142/-23 lines)")
		assert.NotContains(t, msg, "error:")
	})

	t.Run("failure message", func(t *testing.T) {
		msg := svc.formatMessage(Result{
			Status:   "failure",
			PlanFile: "docs/plans/add-auth.md",
			Branch:   "add-auth",
			Mode:     "full",
			Duration: "3m 12s",
			Error:    "runner: task phase: max iterations reached",
		})
		assert.Contains(t, msg, "ralphex failed on build-server")
		assert.Contains(t, msg, "error:    runner: task phase: max iterations reached")
		assert.NotContains(t, msg, "changes:")
	})

	t.Run("missing optional fields", func(t *testing.T) {
		msg := svc.formatMessage(Result{Status: "success"})
		assert.Contains(t, msg, "ralphex completed on build-server")
		assert.NotContains(t, msg, "plan:")
		assert.NotContains(t, msg, "branch:")
		assert.NotContains(t, msg, "mode:")
		assert.NotContains(t, msg, "duration:")
		// changes line still present with zero values
		assert.Contains(t, msg, "changes:  0 files (+0/-0 lines)")
	})

	t.Run("message line count", func(t *testing.T) {
		msg := svc.formatMessage(Result{
			Status:    "success",
			PlanFile:  "plan.md",
			Branch:    "feat",
			Mode:      "full",
			Duration:  "5m",
			Files:     3,
			Additions: 50,
			Deletions: 10,
		})
		lines := strings.Split(strings.TrimRight(msg, "\n"), "\n")
		// header, blank line, plan, branch, mode, duration, changes = 7
		assert.Len(t, lines, 7)
	})
}
