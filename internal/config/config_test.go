package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNormalizeHost(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "app.streamingchasers.com", want: "https://app.streamingchasers.com"},
		{in: " https://App.StreamingChasers.COM/ ", want: "https://app.streamingchasers.com"},
		{in: "https://example.com/api/v1", want: "https://example.com"},
		{in: "localhost:3000", want: "http://localhost:3000"},
		{in: "127.0.0.1:3000/", want: "http://127.0.0.1:3000"},
		{in: "[::1]:3000", want: "http://[::1]:3000"},
		{in: "http://staging.example.com:8080", want: "http://staging.example.com:8080"},
		{in: "https://localhost:3000", want: "https://localhost:3000"},
		{in: "", wantErr: true},
		{in: "ftp://example.com", wantErr: true},
		{in: "https://user:pass@example.com", wantErr: true},
		{in: "https://", wantErr: true},
	}
	for _, tt := range tests {
		got, err := NormalizeHost(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("NormalizeHost(%q) = %q, want an error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeHost(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsLoopbackURL(t *testing.T) {
	for base, want := range map[string]bool{
		"http://localhost:3000":            true,
		"http://127.0.0.1":                 true,
		"http://[::1]:3000":                true,
		"https://app.streamingchasers.com": false,
		"://bad":                           false,
	} {
		if got := IsLoopbackURL(base); got != want {
			t.Errorf("IsLoopbackURL(%q) = %v, want %v", base, got, want)
		}
	}
}

func TestDefaultDir(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}

	got, err := DefaultDir(env(map[string]string{"STREAMINGCHASERS_CONFIG_DIR": "/a", "XDG_CONFIG_HOME": "/b"}))
	if err != nil || got != "/a" {
		t.Errorf("with STREAMINGCHASERS_CONFIG_DIR: got %q, %v", got, err)
	}

	got, err = DefaultDir(env(map[string]string{"XDG_CONFIG_HOME": "/b"}))
	if err != nil || got != filepath.Join("/b", "streamingchasers") {
		t.Errorf("with XDG_CONFIG_HOME: got %q, %v", got, err)
	}

	got, err = DefaultDir(env(nil))
	if err != nil || !strings.HasSuffix(got, filepath.Join(".config", "streamingchasers")) {
		t.Errorf("with nothing set: got %q, %v", got, err)
	}
}

func TestMissingFilesAreEmpty(t *testing.T) {
	store := &Store{Dir: filepath.Join(t.TempDir(), "does-not-exist")}

	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultHost != "" || len(cfg.Hosts) != 0 {
		t.Errorf("expected an empty config, got %+v", cfg)
	}

	creds, err := store.LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if creds.Lookup("https://example.com") != nil {
		t.Error("expected no credentials")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	store := &Store{Dir: filepath.Join(t.TempDir(), "nested", "streamingchasers")}

	cfg := &Config{DefaultHost: "https://example.com"}
	cfg.Host("https://example.com").DefaultCompany = 7
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultHost != "https://example.com" || got.Host("https://example.com").DefaultCompany != 7 {
		t.Errorf("round trip lost data: %+v", got)
	}
}

func TestCredentialsRoundTripAndPermissions(t *testing.T) {
	store := &Store{Dir: filepath.Join(t.TempDir(), "streamingchasers")}
	expires := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	creds := &Credentials{}
	host := creds.Host("https://example.com")
	host.OAuth = &OAuthToken{AccessToken: "a", RefreshToken: "r", ExpiresAt: expires, TokenEndpoint: "https://example.com/oauth/token"}
	host.Client = &OAuthClient{ClientID: "cid", RedirectURI: "http://127.0.0.1:1234/callback"}
	if err := store.SaveCredentials(creds); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	loaded := got.Lookup("https://example.com")
	if loaded == nil || loaded.OAuth == nil || loaded.OAuth.RefreshToken != "r" || !loaded.OAuth.ExpiresAt.Equal(expires) {
		t.Errorf("round trip lost the token: %+v", loaded)
	}
	if loaded.Client == nil || loaded.Client.ClientID != "cid" {
		t.Errorf("round trip lost the client: %+v", loaded)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(store.CredentialsPath())
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("credentials file mode = %o, want 600", perm)
		}
		dir, err := os.Stat(store.Dir)
		if err != nil {
			t.Fatal(err)
		}
		if perm := dir.Mode().Perm(); perm != 0o700 {
			t.Errorf("config directory mode = %o, want 700", perm)
		}
	}
}

func TestSaveLeavesNoTemporaryFiles(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	for i := 0; i < 3; i++ {
		if err := store.SaveCredentials(&Credentials{}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "credentials.json" {
		t.Errorf("unexpected files: %v", entries)
	}
}

func TestInvalidJSONIsReported(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	if err := os.WriteFile(store.ConfigPath(), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := store.LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "config.json") {
		t.Errorf("expected an error naming the file, got %v", err)
	}
}

func TestEmptyFileIsEmptyConfig(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	if err := os.WriteFile(store.ConfigPath(), []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadConfig(); err != nil {
		t.Errorf("an empty file should load: %v", err)
	}
}

func TestOAuthTokenExpired(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	never := &OAuthToken{}
	if never.Expired(now, time.Minute) {
		t.Error("a token without an expiry should not expire")
	}

	token := &OAuthToken{ExpiresAt: now.Add(30 * time.Second)}
	if !token.Expired(now, time.Minute) {
		t.Error("a token inside the leeway should count as expired")
	}
	if token.Expired(now, 10*time.Second) {
		t.Error("a token outside the leeway should not count as expired")
	}
	if !token.Expired(now.Add(time.Hour), 0) {
		t.Error("a token past its expiry should be expired")
	}
}
