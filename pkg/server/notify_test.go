package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotifyPostSlackSuccess(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("content-type %q", ct)
		}
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &gotBody); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := New(Options{})
	req := callReq(map[string]any{
		"text":        "CI fixed on #42",
		"webhook_url": srv.URL,
	})

	res, err := s.handleNotifyPostSlack(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}
	if gotBody["text"] != "CI fixed on #42" {
		t.Fatalf("body text %q", gotBody["text"])
	}
	text := resultText(t, res)
	if !strings.Contains(text, "OK:") || !strings.Contains(text, "Slack message posted") {
		t.Fatalf("result %q", text)
	}
}

func TestNotifyPostSlackMissingWebhook(t *testing.T) {
	t.Setenv("SLACK_WEBHOOK_URL", "")

	s := New(Options{})
	req := callReq(map[string]any{"text": "hello"})

	res, err := s.handleNotifyPostSlack(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error result")
	}
	text := resultText(t, res)
	if !strings.Contains(text, "ERROR: VALIDATION") {
		t.Fatalf("result %q", text)
	}
}

func TestNotifyPostSlackUsesEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("SLACK_WEBHOOK_URL", srv.URL)

	s := New(Options{})
	req := callReq(map[string]any{"text": "from env"})

	res, err := s.handleNotifyPostSlack(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
}