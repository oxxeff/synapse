package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.oxef.dev/ci/synapse/internal/config"
	"go.oxef.dev/ci/synapse/internal/webhook"
)

const testSecret = "topsecret"

func testConfig() *config.Config {
	return &config.Config{
		HTTP:    config.HTTP{Addr: "127.0.0.1:0"},
		Webhook: config.Webhook{HMACSecret: testSecret},
		Dedup:   config.Dedup{Window: config.Duration(time.Minute)},
	}
}

func testServer(t *testing.T) *Server {
	t.Helper()
	return New(testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))

	return hex.EncodeToString(mac.Sum(nil))
}

// wantWebhookStatus posts body to the webhook endpoint and asserts the status
// code, signing with testSecret unless signature is set explicitly.
func wantWebhookStatus(t *testing.T, srv *httptest.Server, event, delivery, body, signature string, want int) {
	t.Helper()

	if signature == "" {
		signature = sign(testSecret, body)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/webhook", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
		return
	}
	req.Header.Set("X-Gitea-Event", event)
	req.Header.Set("X-Gitea-Delivery", delivery)
	req.Header.Set("X-Gitea-Signature", signature)

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
		return
	}
	if resp == nil {
		t.Fatal("do: nil response")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != want {
		t.Errorf("status = %d, want %d", resp.StatusCode, want)
	}
}

const mergedBody = `{
	"action": "closed",
	"pull_request": {"number": 15, "merged": true, "merge_commit_sha": "abc123"},
	"repository": {"name": "app", "full_name": "acme/app", "owner": {"login": "acme"}},
	"sender": {"login": "erin"}
}`

func TestStaticRoutes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(testServer(t).http.Handler)
	t.Cleanup(srv.Close)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{name: "health ok", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK},
		{name: "webhook wrong method", method: http.MethodGet, path: "/webhook", wantStatus: http.StatusMethodNotAllowed},
		{name: "unknown route", method: http.MethodGet, path: "/nope", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(context.Background(), tt.method, srv.URL+tt.path, nil)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("%s %s = %d, want %d", tt.method, tt.path, resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

func TestWebhook(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		event     string
		delivery  string
		body      string
		signature string // empty means sign with testSecret
		want      int
	}{
		{name: "accepted merge event", event: "pull_request", delivery: "d1", body: mergedBody, want: http.StatusAccepted},
		{name: "unsupported event ignored", event: "push", delivery: "d2", body: `{}`, want: http.StatusOK},
		{name: "invalid signature", event: "pull_request", delivery: "d3", body: mergedBody, signature: "deadbeef", want: http.StatusUnauthorized},
		{name: "malformed payload", event: "pull_request", delivery: "d4", body: `{not json`, want: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(testServer(t).http.Handler)
			t.Cleanup(srv.Close)

			wantWebhookStatus(t, srv, tt.event, tt.delivery, tt.body, tt.signature, tt.want)
		})
	}
}

func TestWebhookDispatches(t *testing.T) {
	t.Parallel()

	s := testServer(t)
	got := make(chan webhook.Event, 1)
	s.dispatch = func(evt webhook.Event) { got <- evt }

	srv := httptest.NewServer(s.http.Handler)
	t.Cleanup(srv.Close)

	wantWebhookStatus(t, srv, "pull_request", "d1", mergedBody, "", http.StatusAccepted)

	select {
	case evt := <-got:
		if evt.Kind != webhook.KindMerge {
			t.Errorf("dispatched kind = %q, want merge", evt.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("router dispatch was not called for a matchable event")
	}
}

func TestWebhookDuplicateDelivery(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(testServer(t).http.Handler)
	t.Cleanup(srv.Close)

	wantWebhookStatus(t, srv, "pull_request", "dup", mergedBody, "", http.StatusAccepted)

	// The same delivery id is acknowledged without reprocessing.
	wantWebhookStatus(t, srv, "pull_request", "dup", mergedBody, "", http.StatusOK)
}

func TestWebhookSecretNotConfigured(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.Webhook.HMACSecret = ""
	srv := httptest.NewServer(New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil))).http.Handler)
	t.Cleanup(srv.Close)

	// With no secret the handler rejects before any other work.
	wantWebhookStatus(t, srv, "pull_request", "d1", mergedBody, "", http.StatusInternalServerError)
}

func TestRunGracefulShutdown(t *testing.T) {
	t.Parallel()

	srv := testServer(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	// Give the listener a moment to come up, then trigger shutdown via context.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil after graceful shutdown", err)
		}
	case <-time.After(shutdownTimeout + time.Second):
		t.Fatal("Run did not return after shutdown")
	}
}

func TestRunListenError(t *testing.T) {
	t.Parallel()

	// An address the OS cannot bind surfaces as a Run error rather than a panic.
	cfg := testConfig()
	cfg.HTTP.Addr = "127.0.0.1:99999"
	srv := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := srv.Run(context.Background()); err == nil {
		t.Fatal("expected listen error for invalid port")
	}
}
