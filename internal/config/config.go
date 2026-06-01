// Package config loads and validates the service configuration.
//
// Configuration comes from an optional YAML file overridden by environment
// variables: file values replace built-in defaults, environment values replace
// file values. The contract is the YAML keys and env names, not the Go types -
// the latter may change without breaking deployments.
//
// Secrets (Gitea/Jenkins credentials, HMAC) are not required to start: the
// service boots as long as the listen address and timing values are coherent.
// Presence of a given secret is enforced by the phase that consumes it, so the
// HTTP skeleton runs without real infrastructure wired in.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Default timing values applied when neither file nor environment set them.
const (
	defaultAddr         = ":8080"
	defaultWaitTimeout  = 10 * time.Minute
	defaultPollInterval = 5 * time.Second
	defaultDedupWindow  = 10 * time.Minute
	defaultLogLevel     = "info"
)

// Config is the resolved service configuration after defaults, file and
// environment have been merged and validated.
type Config struct {
	HTTP    HTTP    `yaml:"http"`
	Gitea   Gitea   `yaml:"gitea"`
	Jenkins Jenkins `yaml:"jenkins"`
	Webhook Webhook `yaml:"webhook"`
	Build   Build   `yaml:"build"`
	Dedup   Dedup   `yaml:"dedup"`
	Log     Log     `yaml:"log"`
}

// HTTP holds the inbound webhook server settings.
type HTTP struct {
	Addr string `yaml:"addr"`
}

// Gitea holds access to the Gitea API used for reading the contract, ACL checks
// and posting reactions and comments from the bot account.
type Gitea struct {
	BaseURL string `yaml:"base_url"`
	Token   string `yaml:"token"`
}

// Jenkins holds access to the executor API. Jenkins is the current and only
// executor implementation; the contract itself stays executor-neutral.
type Jenkins struct {
	BaseURL string `yaml:"base_url"`
	User    string `yaml:"user"`
	Token   string `yaml:"token"`
}

// Webhook holds the shared secret used to verify the HMAC signature of inbound
// Gitea deliveries - the only trust boundary for incoming events.
type Webhook struct {
	HMACSecret string `yaml:"hmac_secret"`
}

// Build controls how long the service waits for an executor build and how often
// it polls its status before handing the final report off to the executor.
type Build struct {
	WaitTimeout  Duration `yaml:"wait_timeout"`
	PollInterval Duration `yaml:"poll_interval"`
}

// Dedup controls the in-memory window that suppresses repeated webhook
// deliveries carrying an already-seen delivery identifier.
type Dedup struct {
	Window Duration `yaml:"window"`
}

// Log controls structured logging.
type Log struct {
	Level string `yaml:"level"`
}

// SlogLevel maps the configured level name to an slog level. The name is
// validated during Load, so an unexpected value here falls back to info.
func (l Log) SlogLevel() slog.Level {
	switch l.Level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Load builds the configuration from defaults, then the YAML file at path (if
// non-empty), then environment overrides, and validates the result. An empty
// path skips the file and uses defaults plus environment only.
func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		if err := applyFile(cfg, path); err != nil {
			return nil, err
		}
	}

	if err := cfg.applyEnv(); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func defaults() *Config {
	return &Config{
		HTTP: HTTP{Addr: defaultAddr},
		Build: Build{
			WaitTimeout:  Duration(defaultWaitTimeout),
			PollInterval: Duration(defaultPollInterval),
		},
		Dedup: Dedup{Window: Duration(defaultDedupWindow)},
		Log:   Log{Level: defaultLogLevel},
	}
}

// applyFile decodes the YAML file onto cfg. Only keys present in the file
// override defaults; unknown keys are rejected so typos surface instead of
// being silently ignored.
func applyFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	// An empty file decodes to io.EOF, which means "no overrides", not an error.
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse config file: %w", err)
	}

	return nil
}

func (c *Config) applyEnv() error {
	setString(&c.HTTP.Addr, "SYNAPSE_HTTP_ADDR")
	setString(&c.Gitea.BaseURL, "SYNAPSE_GITEA_URL")
	setString(&c.Gitea.Token, "SYNAPSE_GITEA_TOKEN")
	setString(&c.Jenkins.BaseURL, "SYNAPSE_JENKINS_URL")
	setString(&c.Jenkins.User, "SYNAPSE_JENKINS_USER")
	setString(&c.Jenkins.Token, "SYNAPSE_JENKINS_TOKEN")
	setString(&c.Webhook.HMACSecret, "SYNAPSE_WEBHOOK_SECRET")
	setString(&c.Log.Level, "SYNAPSE_LOG_LEVEL")

	for _, d := range []struct {
		dst *Duration
		env string
	}{
		{&c.Build.WaitTimeout, "SYNAPSE_BUILD_WAIT_TIMEOUT"},
		{&c.Build.PollInterval, "SYNAPSE_BUILD_POLL_INTERVAL"},
		{&c.Dedup.Window, "SYNAPSE_DEDUP_WINDOW"},
	} {
		if err := setDuration(d.dst, d.env); err != nil {
			return err
		}
	}

	return nil
}

func setString(dst *string, env string) {
	if v, ok := os.LookupEnv(env); ok {
		*dst = v
	}
}

func setDuration(dst *Duration, env string) error {
	v, ok := os.LookupEnv(env)
	if !ok {
		return nil
	}

	parsed, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("env %s: invalid duration %q: %w", env, v, err)
	}

	*dst = Duration(parsed)
	return nil
}

// Validate checks coherence of the values needed to run. It deliberately does
// not require secrets - those are enforced where consumed.
func (c *Config) Validate() error {
	if c.HTTP.Addr == "" {
		return errors.New("http.addr must not be empty")
	}

	if c.Build.WaitTimeout <= 0 {
		return errors.New("build.wait_timeout must be positive")
	}

	if c.Build.PollInterval <= 0 {
		return errors.New("build.poll_interval must be positive")
	}

	if c.Build.PollInterval >= c.Build.WaitTimeout {
		return errors.New("build.poll_interval must be smaller than build.wait_timeout")
	}

	if c.Dedup.Window <= 0 {
		return errors.New("dedup.window must be positive")
	}

	if !validLogLevel(c.Log.Level) {
		return fmt.Errorf("log.level %q must be one of debug, info, warn, error", c.Log.Level)
	}

	return nil
}

func validLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
