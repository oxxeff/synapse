// Package server is the inbound HTTP layer of the service.
//
// It exposes a health endpoint and the webhook endpoint. The webhook handler
// runs the trusted-intake steps - HMAC verification, retry deduplication and
// payload parsing - then hands the parsed event to the router and acknowledges
// immediately; routing and result reporting continue in the background under the
// server-lifetime context. The lifecycle (graceful shutdown on context
// cancellation) is final.
package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"go.oxef.dev/ci/synapse/internal/config"
	"go.oxef.dev/ci/synapse/internal/dedup"
	"go.oxef.dev/ci/synapse/internal/gitea"
	"go.oxef.dev/ci/synapse/internal/jenkins"
	"go.oxef.dev/ci/synapse/internal/router"
	"go.oxef.dev/ci/synapse/internal/webhook"
)

// Timeouts that bound request handling and shutdown. They guard against slow or
// stuck peers holding resources; values are conservative for a webhook server.
const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 10 * time.Second
)

// webhookMaxBytes caps the delivery body the server will read. Gitea payloads
// are small; the cap bounds memory against an oversized or hostile request.
const webhookMaxBytes = 5 << 20

// Gitea delivery headers: hex HMAC signature of the body, event name, and the
// unique delivery id used for deduplication.
const (
	headerSignature = "X-Gitea-Signature"
	headerEvent     = "X-Gitea-Event"
	headerDelivery  = "X-Gitea-Delivery"
)

// Server owns the HTTP listener and its routes.
type Server struct {
	http       *http.Server
	log        *slog.Logger
	hmacSecret string
	dedup      *dedup.Store
	router     *router.Router
	botResolve bool
	// dispatch hands a parsed event to the router under the server-lifetime
	// context. It is a no-op until Run captures that context, so the request
	// context (cancelled when the response is written) is never used for routing.
	dispatch func(webhook.Event)
}

// New builds a server bound to the configured address with the health and
// webhook routes registered. It wires the Gitea client, the Jenkins executor and
// the router from configuration. It does not start listening - call Run.
func New(cfg *config.Config, log *slog.Logger) *Server {
	giteaClient := gitea.New(cfg.Gitea.BaseURL, cfg.Gitea.Token)
	exec := jenkins.New(cfg.Jenkins.BaseURL, cfg.Jenkins.User, cfg.Jenkins.Token)
	rtr := router.New(giteaClient, exec, cfg.Build.PollInterval.Std(), cfg.Build.WaitTimeout.Std(), log)

	s := &Server{
		log:        log,
		hmacSecret: cfg.Webhook.HMACSecret,
		dedup:      dedup.New(cfg.Dedup.Window.Std()),
		router:     rtr,
		botResolve: cfg.Gitea.BaseURL != "" && cfg.Gitea.Token != "",
		dispatch:   func(webhook.Event) {},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /webhook", s.handleWebhook)

	s.http = &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	return s
}

// Run starts the listener and blocks until ctx is cancelled or the server fails
// to start. On cancellation it shuts the server down gracefully within
// shutdownTimeout. A clean shutdown returns nil.
func (s *Server) Run(ctx context.Context) error {
	// Bind the dedup eviction goroutine to this call: cancelling on return stops
	// it whether Run exits via shutdown or a listen error.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Routing and report waiting outlive each HTTP request, so they run under the
	// server-lifetime context, captured by the dispatch closure rather than stored
	// on the struct.
	s.dispatch = func(evt webhook.Event) { go s.router.Handle(ctx, evt) }

	if s.botResolve {
		if err := s.router.ResolveBotLogin(ctx); err != nil {
			s.log.Warn("resolve bot login; loop protection degraded", "error", err)
		}
	}

	go s.dedup.Run(ctx)
	go s.router.Run(ctx)

	serveErr := make(chan error, 1)

	go func() {
		s.log.Info("http server listening", "addr", s.http.Addr)
		err := s.http.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case <-ctx.Done():
		s.log.Info("shutting down http server")
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancelShutdown()
		return s.http.Shutdown(shutdownCtx)
	case err := <-serveErr:
		return err
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok\n")); err != nil {
		s.log.Warn("write health response", "error", err)
	}
}

// handleWebhook runs the trusted-intake steps for a Gitea delivery: verify the
// HMAC signature, drop retries, parse the payload. A parsed but not-yet-routable
// event is acknowledged with 202 and logged. An untrusted request never produces
// a reaction in the pull request.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if s.hmacSecret == "" {
		// The secret is consumed by this phase; absence is a service misconfiguration.
		s.log.Error("webhook secret not configured")
		http.Error(w, "webhook secret not configured", http.StatusInternalServerError)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webhookMaxBytes))
	if err != nil {
		s.log.Warn("read webhook body", "error", err)
		http.Error(w, "cannot read request body", http.StatusBadRequest)
		return
	}

	if !webhook.Verify(s.hmacSecret, body, r.Header.Get(headerSignature)) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	delivery := r.Header.Get(headerDelivery)
	if s.dedup.Seen(delivery) {
		s.log.Debug("duplicate webhook delivery", "delivery", delivery)
		w.WriteHeader(http.StatusOK)
		return
	}

	eventType := r.Header.Get(headerEvent)

	evt, err := webhook.Parse(eventType, body)
	if err != nil {
		s.log.Warn("parse webhook payload", "event", eventType, "error", err)
		http.Error(w, "cannot parse webhook payload", http.StatusBadRequest)
		return
	}

	if evt.Kind == webhook.KindUnsupported {
		s.log.Debug("ignoring unsupported webhook event", "event", eventType)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Hand the event to the router and acknowledge immediately; routing and build
	// waiting run in the background so the Gitea delivery connection is not held.
	s.log.Debug("webhook event accepted",
		"kind", evt.Kind,
		"repo", evt.Repo.FullName,
		"pr", evt.PR.Number,
		"sender", evt.Sender,
	)
	s.dispatch(evt)
	w.WriteHeader(http.StatusAccepted)
}
