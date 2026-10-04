// Package config reads and writes the CLI's settings and credentials.
//
// Settings and credentials live in separate files so that config.json can
// be kept in a dotfiles repository while credentials.json, which holds
// tokens, stays private (mode 0600).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultHost is the production Streaming Chasers service.
const DefaultHost = "https://app.streamingchasers.com"

const (
	configFile      = "config.json"
	credentialsFile = "credentials.json"
)

// Config holds the non-secret settings.
type Config struct {
	DefaultHost string                 `json:"default_host,omitempty"`
	Hosts       map[string]*HostConfig `json:"hosts,omitempty"`
}

// HostConfig holds the settings for one server.
type HostConfig struct {
	DefaultCompany int64 `json:"default_company,omitempty"`
}

// Host returns the settings for host, creating them if needed.
func (c *Config) Host(host string) *HostConfig {
	if c.Hosts == nil {
		c.Hosts = map[string]*HostConfig{}
	}
	if c.Hosts[host] == nil {
		c.Hosts[host] = &HostConfig{}
	}
	return c.Hosts[host]
}

// Credentials holds the secrets for every server the user has signed in to.
type Credentials struct {
	Hosts map[string]*HostCredentials `json:"hosts,omitempty"`
}

// HostCredentials holds the secrets for one server.  At most one of
// APIToken and OAuth is set.  Client outlives a logout so that signing in
// again does not register a second application with the server.
type HostCredentials struct {
	APIToken string       `json:"api_token,omitempty"`
	OAuth    *OAuthToken  `json:"oauth,omitempty"`
	Client   *OAuthClient `json:"client,omitempty"`
}

// OAuthToken is an access token with what is needed to refresh and revoke it.
type OAuthToken struct {
	AccessToken        string    `json:"access_token"`
	RefreshToken       string    `json:"refresh_token,omitempty"`
	Scope              string    `json:"scope,omitempty"`
	Resource           string    `json:"resource,omitempty"`
	ExpiresAt          time.Time `json:"expires_at,omitempty"`
	TokenEndpoint      string    `json:"token_endpoint"`
	RevocationEndpoint string    `json:"revocation_endpoint,omitempty"`
}

// Expired reports whether the token is within leeway of its expiry.  A
// token without an expiry never expires.
func (t *OAuthToken) Expired(now time.Time, leeway time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return !now.Add(leeway).Before(t.ExpiresAt)
}

// OAuthClient is the application the CLI registered with a server.
type OAuthClient struct {
	ClientID     string    `json:"client_id"`
	RedirectURI  string    `json:"redirect_uri"`
	Scope        string    `json:"scope,omitempty"`
	RegisteredAt time.Time `json:"registered_at,omitempty"`
}

// Host returns the credentials for host, creating them if needed.
func (c *Credentials) Host(host string) *HostCredentials {
	if c.Hosts == nil {
		c.Hosts = map[string]*HostCredentials{}
	}
	if c.Hosts[host] == nil {
		c.Hosts[host] = &HostCredentials{}
	}
	return c.Hosts[host]
}

// Lookup returns the credentials for host, or nil if there are none.
func (c *Credentials) Lookup(host string) *HostCredentials {
	if c == nil || c.Hosts == nil {
		return nil
	}
	return c.Hosts[host]
}

// Store is a directory holding the config and credentials files.
type Store struct {
	Dir string
}

// DefaultDir is where the files live: $STREAMINGCHASERS_CONFIG_DIR, else
// $XDG_CONFIG_HOME/streamingchasers, else ~/.config/streamingchasers.
func DefaultDir(getenv func(string) string) (string, error) {
	if dir := getenv("STREAMINGCHASERS_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	if dir := getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "streamingchasers"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find a home directory for the configuration: %w", err)
	}
	return filepath.Join(home, ".config", "streamingchasers"), nil
}

// ConfigPath is the path of the settings file.
func (s *Store) ConfigPath() string { return filepath.Join(s.Dir, configFile) }

// CredentialsPath is the path of the credentials file.
func (s *Store) CredentialsPath() string { return filepath.Join(s.Dir, credentialsFile) }

// LoadConfig reads the settings; a missing file is an empty Config.
func (s *Store) LoadConfig() (*Config, error) {
	c := &Config{}
	if err := readJSON(s.ConfigPath(), c); err != nil {
		return nil, err
	}
	return c, nil
}

// SaveConfig writes the settings.
func (s *Store) SaveConfig(c *Config) error {
	return s.writeJSON(s.ConfigPath(), c, 0o644)
}

// LoadCredentials reads the credentials; a missing file is empty Credentials.
func (s *Store) LoadCredentials() (*Credentials, error) {
	c := &Credentials{}
	if err := readJSON(s.CredentialsPath(), c); err != nil {
		return nil, err
	}
	return c, nil
}

// SaveCredentials writes the credentials, readable only by the user.
func (s *Store) SaveCredentials(c *Credentials) error {
	return s.writeJSON(s.CredentialsPath(), c, 0o600)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return nil
}

// writeJSON replaces the file atomically so that a crash or a second
// process never leaves half a file behind.
func (s *Store) writeJSON(path string, v any, mode fs.FileMode) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// NormalizeHost turns what a user types ("app.streamingchasers.com",
// "localhost:3000", "https://example.com/") into a base URL with a scheme
// and no path.  A host without a scheme gets https, except for loopback
// addresses, which get http.
func NormalizeHost(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("host is empty")
	}
	if !strings.Contains(s, "://") {
		if isLoopback(hostOnly(s)) {
			s = "http://" + s
		} else {
			s = "https://" + s
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid host %q: %w", s, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid host %q: scheme must be http or https", s)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("invalid host %q: no host name", s)
	}
	if u.User != nil {
		return "", fmt.Errorf("invalid host %q: must not contain credentials", s)
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

func hostOnly(s string) string {
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return strings.Trim(s, "[]")
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// IsLoopbackURL reports whether base points at this machine.
func IsLoopbackURL(base string) bool {
	u, err := url.Parse(base)
	return err == nil && isLoopback(u.Hostname())
}
