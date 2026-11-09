// Package notify sends "new video" and "download finished" messages to
// people. Notifiers fail open: callers log the error and carry on, because a
// chat outage must never stop a download.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Message is one notification.
type Message struct {
	Title string
	Body  string
	URL   string
}

// Text renders the message as plain text.
func (m Message) Text() string {
	parts := make([]string, 0, 3)
	for _, p := range []string{m.Title, m.Body, m.URL} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n")
}

// Notifier delivers a Message.
type Notifier interface {
	Notify(ctx context.Context, m Message) error
}

// Nop discards messages; it is used when nothing is configured.
type Nop struct{}

// Notify implements Notifier.
func (Nop) Notify(context.Context, Message) error { return nil }

// Multi fans a message out to every notifier and joins their errors.
type Multi []Notifier

// Notify implements Notifier.
func (m Multi) Notify(ctx context.Context, msg Message) error {
	var errs []error
	for _, n := range m {
		if err := n.Notify(ctx, msg); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Environment variables that configure Telegram.
const (
	TelegramTokenEnv = "TELEGRAM_BOT_TOKEN"
	TelegramChatEnv  = "TELEGRAM_CHAT_ID"
)

// DefaultTelegramAPI is the Bot API root.
const DefaultTelegramAPI = "https://api.telegram.org"

// maxTelegramText is Telegram's message length limit.
const maxTelegramText = 4096

// Telegram sends messages through the Bot API.
type Telegram struct {
	Token   string
	ChatID  string
	BaseURL string // Bot API root; tests point it at a local server
	Client  *http.Client
}

// FromEnv returns a Telegram notifier when both variables are set, else Nop.
func FromEnv(client *http.Client) Notifier {
	token, chat := strings.TrimSpace(os.Getenv(TelegramTokenEnv)), strings.TrimSpace(os.Getenv(TelegramChatEnv))
	if token == "" || chat == "" {
		return Nop{}
	}
	return &Telegram{Token: token, ChatID: chat, Client: client}
}

// Notify implements Notifier.
func (t *Telegram) Notify(ctx context.Context, m Message) error {
	text := m.Text()
	if text == "" {
		return nil
	}
	if r := []rune(text); len(r) > maxTelegramText {
		text = string(r[:maxTelegramText-1]) + "…"
	}
	body, err := json.Marshal(map[string]any{
		"chat_id":                  t.ChatID,
		"text":                     text,
		"disable_web_page_preview": true,
	})
	if err != nil {
		return err
	}
	base := strings.TrimRight(t.BaseURL, "/")
	if base == "" {
		base = DefaultTelegramAPI
	}
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+t.Token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return t.redact(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return t.redact(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram: status %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// redact keeps the bot token (part of the request URL) out of error text.
func (t *Telegram) redact(err error) error {
	if t.Token == "" {
		return fmt.Errorf("telegram: %w", err)
	}
	return fmt.Errorf("telegram: %s", strings.ReplaceAll(err.Error(), t.Token, "***"))
}
