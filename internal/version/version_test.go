package version

import "testing"

// These tests mutate the package-level build variables, so they do not run in
// parallel with each other.

func TestHuman(t *testing.T) {
	tests := []struct {
		name    string
		version string
		commit  string
		date    string
		build   string
		want    string
	}{
		{name: "defaults", version: "dev", commit: "none", date: "unknown", build: "0", want: "dev-none (#0, unknown)"},
		{
			name: "full commit truncated to 7 chars", version: "1.0.0",
			commit: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", date: "2026-02-01", build: "42",
			want: "1.0.0-a1b2c3d (#42, 2026-02-01)",
		},
		{name: "short commit unchanged", version: "0.1.0", commit: "abc", date: "2026-01-01", build: "1", want: "0.1.0-abc (#1, 2026-01-01)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version, Commit, Date, Build = tt.version, tt.commit, tt.date, tt.build
			if got := Human(); got != tt.want {
				t.Errorf("Human() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTechnical(t *testing.T) {
	tests := []struct {
		name    string
		version string
		commit  string
		date    string
		build   string
		want    string
	}{
		{name: "defaults", version: "dev", commit: "none", date: "unknown", build: "0", want: "dev+0.unknown.none"},
		{
			name: "full values with date dashes stripped", version: "1.0.0",
			commit: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", date: "2026-02-01", build: "42",
			want: "1.0.0+42.20260201.a1b2c3d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version, Commit, Date, Build = tt.version, tt.commit, tt.date, tt.build
			if got := Technical(); got != tt.want {
				t.Errorf("Technical() = %q, want %q", got, tt.want)
			}
		})
	}
}
