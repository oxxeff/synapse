package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTP.Addr != defaultAddr {
		t.Errorf("addr = %q, want %q", cfg.HTTP.Addr, defaultAddr)
	}
	if cfg.Build.WaitTimeout.Std() != defaultWaitTimeout {
		t.Errorf("wait_timeout = %v, want %v", cfg.Build.WaitTimeout, defaultWaitTimeout)
	}
	if cfg.Build.PollInterval.Std() != defaultPollInterval {
		t.Errorf("poll_interval = %v, want %v", cfg.Build.PollInterval, defaultPollInterval)
	}
	if cfg.Dedup.Window.Std() != defaultDedupWindow {
		t.Errorf("dedup window = %v, want %v", cfg.Dedup.Window, defaultDedupWindow)
	}
	if cfg.Log.Level != defaultLogLevel {
		t.Errorf("log level = %q, want %q", cfg.Log.Level, defaultLogLevel)
	}
}

func TestLoadFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantErr string // substring; empty means success
		check   func(t *testing.T, c *Config)
	}{
		{
			name: "full file overrides defaults",
			content: `
http:
  addr: "127.0.0.1:9000"
gitea:
  base_url: "https://git.example.com"
  token: "g-token"
jenkins:
  base_url: "https://ci.example.com"
  user: "bot"
  token: "j-token"
webhook:
  hmac_secret: "s3cr3t"
build:
  wait_timeout: "5m"
  poll_interval: "2s"
dedup:
  window: "3m"
log:
  level: "debug"
`,
			check: func(t *testing.T, c *Config) {
				t.Helper()
				if c.HTTP.Addr != "127.0.0.1:9000" {
					t.Errorf("addr = %q", c.HTTP.Addr)
				}
				if c.Gitea.Token != "g-token" || c.Jenkins.User != "bot" {
					t.Errorf("gitea/jenkins not applied: %+v %+v", c.Gitea, c.Jenkins)
				}
				if c.Webhook.HMACSecret != "s3cr3t" {
					t.Errorf("hmac = %q", c.Webhook.HMACSecret)
				}
				if c.Build.WaitTimeout.Std() != 5*time.Minute {
					t.Errorf("wait_timeout = %v", c.Build.WaitTimeout)
				}
				if c.Dedup.Window.Std() != 3*time.Minute {
					t.Errorf("window = %v", c.Dedup.Window)
				}
			},
		},
		{
			name:    "partial file keeps defaults",
			content: "log:\n  level: \"warn\"\n",
			check: func(t *testing.T, c *Config) {
				t.Helper()
				if c.Log.Level != "warn" {
					t.Errorf("level = %q", c.Log.Level)
				}
				if c.HTTP.Addr != defaultAddr {
					t.Errorf("addr = %q, want default", c.HTTP.Addr)
				}
			},
		},
		{
			name:    "empty file keeps defaults",
			content: "",
			check: func(t *testing.T, c *Config) {
				t.Helper()
				if c.HTTP.Addr != defaultAddr {
					t.Errorf("addr = %q, want default", c.HTTP.Addr)
				}
			},
		},
		{
			name:    "unknown key rejected",
			content: "bogus: 1\n",
			wantErr: "parse config file",
		},
		{
			name:    "invalid duration rejected",
			content: "build:\n  wait_timeout: \"nope\"\n",
			wantErr: "invalid duration",
		},
		{
			name:    "duration as non-scalar rejected",
			content: "build:\n  wait_timeout: [1, 2]\n",
			wantErr: "duration must be a string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := Load(writeConfig(t, tt.content))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			tt.check(t, cfg)
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()

	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	path := writeConfig(t, "http:\n  addr: \"127.0.0.1:1\"\nlog:\n  level: \"info\"\n")

	t.Setenv("SYNAPSE_HTTP_ADDR", "0.0.0.0:7777")
	t.Setenv("SYNAPSE_GITEA_TOKEN", "env-token")
	t.Setenv("SYNAPSE_LOG_LEVEL", "error")
	t.Setenv("SYNAPSE_DEDUP_WINDOW", "30s")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTP.Addr != "0.0.0.0:7777" {
		t.Errorf("addr = %q, want env override", cfg.HTTP.Addr)
	}
	if cfg.Gitea.Token != "env-token" {
		t.Errorf("token = %q", cfg.Gitea.Token)
	}
	if cfg.Log.Level != "error" {
		t.Errorf("level = %q", cfg.Log.Level)
	}
	if cfg.Dedup.Window.Std() != 30*time.Second {
		t.Errorf("window = %v", cfg.Dedup.Window)
	}
}

func TestLoadEnvInvalidDuration(t *testing.T) {
	t.Setenv("SYNAPSE_BUILD_POLL_INTERVAL", "fast")

	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "SYNAPSE_BUILD_POLL_INTERVAL") {
		t.Fatalf("err = %v, want env duration error", err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	base := func() *Config {
		c := defaults()
		return c
	}

	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string
	}{
		{name: "defaults valid", mutate: func(*Config) {}},
		{
			name:    "empty addr",
			mutate:  func(c *Config) { c.HTTP.Addr = "" },
			wantErr: "http.addr",
		},
		{
			name:    "non-positive wait timeout",
			mutate:  func(c *Config) { c.Build.WaitTimeout = 0 },
			wantErr: "wait_timeout must be positive",
		},
		{
			name:    "non-positive poll interval",
			mutate:  func(c *Config) { c.Build.PollInterval = 0 },
			wantErr: "poll_interval must be positive",
		},
		{
			name: "poll not smaller than timeout",
			mutate: func(c *Config) {
				c.Build.PollInterval = c.Build.WaitTimeout
			},
			wantErr: "smaller than build.wait_timeout",
		},
		{
			name:    "non-positive dedup window",
			mutate:  func(c *Config) { c.Dedup.Window = 0 },
			wantErr: "dedup.window must be positive",
		},
		{
			name:    "bad log level",
			mutate:  func(c *Config) { c.Log.Level = "trace" },
			wantErr: "log.level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := base()
			tt.mutate(c)
			err := c.Validate()

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}
