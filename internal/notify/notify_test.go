package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type recorder struct {
	got []Message
	err error
}

func (r *recorder) Notify(_ context.Context, m Message) error {
	r.got = append(r.got, m)
	return r.err
}

func TestMessageText(t *testing.T) {
	m := Message{Title: "New", Body: "  ", URL: "https://x"}
	if got := m.Text(); got != "New\nhttps://x" {
		t.Fatalf("text = %q", got)
	}
	if (Message{}).Text() != "" {
		t.Fatal("empty message")
	}
}

func TestMultiJoinsErrorsAndDeliversToAll(t *testing.T) {
	a, b := &recorder{err: errors.New("a down")}, &recorder{}
	err := Multi{a, b, Nop{}}.Notify(context.Background(), Message{Title: "t"})
	if err == nil || !strings.Contains(err.Error(), "a down") {
		t.Fatalf("err = %v", err)
	}
	if len(a.got) != 1 || len(b.got) != 1 {
		t.Fatal("every notifier must receive the message")
	}
}

func TestTelegramSends(t *testing.T) {
	var path string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	tg := &Telegram{Token: "123:SECRET", ChatID: "42", BaseURL: srv.URL + "/"}
	if err := tg.Notify(context.Background(), Message{Title: "Found", URL: "https://x/1"}); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:SECRET/sendMessage" {
		t.Fatalf("path = %s", path)
	}
	if payload["chat_id"] != "42" || payload["text"] != "Found\nhttps://x/1" {
		t.Fatalf("payload = %v", payload)
	}
	if err := tg.Notify(context.Background(), Message{}); err != nil {
		t.Fatalf("empty message must be a no-op: %v", err)
	}
}

func TestTelegramTruncatesLongText(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
	}))
	defer srv.Close()
	tg := &Telegram{Token: "t", ChatID: "c", BaseURL: srv.URL}
	if err := tg.Notify(context.Background(), Message{Body: strings.Repeat("あ", 5000)}); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(payload["text"].(string))); n != maxTelegramText {
		t.Fatalf("len = %d", n)
	}
}

func TestTelegramErrorsNeverLeakTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	tg := &Telegram{Token: "123:SECRET", ChatID: "42", BaseURL: srv.URL}
	err := tg.Notify(context.Background(), Message{Title: "x"})
	if err == nil || !strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("status error = %v", err)
	}
	srv.Close() // connection refused: the transport error contains the URL
	err = tg.Notify(context.Background(), Message{Title: "x"})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("transport error leaked the token: %v", err)
	}
	bad := &Telegram{Token: "t", ChatID: "c", BaseURL: "://bad"}
	if err := bad.Notify(context.Background(), Message{Title: "x"}); err == nil {
		t.Fatal("bad URL must fail")
	}
	noToken := &Telegram{BaseURL: "://bad"}
	if err := noToken.Notify(context.Background(), Message{Title: "x"}); err == nil {
		t.Fatal("bad URL must fail without a token too")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv(TelegramTokenEnv, "")
	t.Setenv(TelegramChatEnv, "")
	if _, ok := FromEnv(nil).(Nop); !ok {
		t.Fatal("unset env must give Nop")
	}
	t.Setenv(TelegramTokenEnv, "tok")
	if _, ok := FromEnv(nil).(Nop); !ok {
		t.Fatal("token without chat must give Nop")
	}
	t.Setenv(TelegramChatEnv, "7")
	tg, ok := FromEnv(nil).(*Telegram)
	if !ok || tg.Token != "tok" || tg.ChatID != "7" {
		t.Fatalf("got %#v", tg)
	}
}
