package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jwmoss/roborocli/internal/roborock"
)

const (
	AppName           = "roborocli"
	EnvPrefix         = "ROBOROCLI"
	DefaultBaseURL    = "https://usiot.roborock.com"
	DefaultAuthHeader = "Authorization"
	ConfigFilename    = "config.yaml"
)

// Config holds login credentials, the stored cloud user data, and device
// preferences.
type Config struct {
	BaseURL string `yaml:"base_url"`

	// Username is the Roborock account email used for login.
	Username string `yaml:"username,omitempty"`
	// ClientID is a stable per-install identifier used in header_clientid.
	ClientID string `yaml:"client_id,omitempty"`

	// UserData stores the login response: rriot credentials, region, token.
	UserData *roborock.UserData `yaml:"user_data,omitempty"`

	// DefaultDUID selects a device when no --device is given.
	DefaultDUID string `yaml:"default_duid,omitempty"`
	// DeviceIPs caches the last known local IP per device duid.
	DeviceIPs map[string]string `yaml:"device_ips,omitempty"`
}

// Default returns the base configuration.
func Default() Config {
	return Config{BaseURL: DefaultBaseURL}
}

// DefaultPath returns the user config file path.
func DefaultPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, AppName, ConfigFilename)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ConfigFilename)
	}
	return filepath.Join(home, ".config", AppName, ConfigFilename)
}

// Load reads the config file, then applies environment overrides.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		path = DefaultPath()
	}
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config file %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}

	applyEnv(&cfg)
	normalize(&cfg)
	return &cfg, nil
}

// Save writes the config with private permissions. It refuses to follow a
// symlink at the destination.
func Save(path string, cfg Config, overwrite bool) error {
	if path == "" {
		path = DefaultPath()
	}
	normalize(&cfg)
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular config path %s", path)
		}
		if !overwrite {
			return fmt.Errorf("config already exists at %s; use --force to overwrite", path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect config file: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return fmt.Errorf("create private config: %w", err)
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write config file: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(file.Name(), 0600); err != nil {
		return fmt.Errorf("set config permissions: %w", err)
	}
	if overwrite {
		// Replace the directory entry rather than follow a raced symlink.
		err = os.Rename(file.Name(), path)
	} else {
		// Publish only if no other process created the destination.
		err = os.Link(file.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("publish config file: %w", err)
	}
	return nil
}

// Validate checks the minimum required fields for cloud commands.
func (c Config) Validate() error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("base URL is required; set --base-url, %s_BASE_URL, or config file base_url", EnvPrefix)
	}
	return nil
}

// ValidateAuth checks that cloud credentials are stored.
func (c Config) ValidateAuth() error {
	if strings.TrimSpace(c.Username) == "" {
		return fmt.Errorf("account email is required; run: roborocli login --email you@example.com")
	}
	if c.UserData == nil || c.UserData.RRiot.U == "" {
		return fmt.Errorf("no stored login; run: roborocli login --email %s", c.Username)
	}
	return nil
}

// Redacted returns a map of config values safe to display.
func (c Config) Redacted() map[string]any {
	out := map[string]any{
		"base_url":     c.BaseURL,
		"username":     c.Username,
		"client_id":    c.ClientID,
		"default_duid": c.DefaultDUID,
		"logged_in":    c.UserData != nil && c.UserData.RRiot.U != "",
		"device_ips":   c.DeviceIPs,
	}
	if c.UserData != nil {
		out["region"] = c.UserData.Region
		out["country"] = c.UserData.Country
		out["api_url"] = c.UserData.RRiot.R.A
		out["mqtt_url"] = c.UserData.RRiot.R.M
	}
	return out
}

func applyEnv(cfg *Config) {
	if value := os.Getenv(EnvPrefix + "_BASE_URL"); value != "" {
		cfg.BaseURL = value
	}
	if value := os.Getenv(EnvPrefix + "_EMAIL"); value != "" {
		cfg.Username = value
	}
	if value := os.Getenv(EnvPrefix + "_USERNAME"); value != "" {
		cfg.Username = value
	}
	if value := os.Getenv(EnvPrefix + "_CLIENT_ID"); value != "" {
		cfg.ClientID = value
	}
	if value := os.Getenv(EnvPrefix + "_DUID"); value != "" {
		cfg.DefaultDUID = value
	}
	if value := os.Getenv(EnvPrefix + "_HOST"); value != "" {
		if cfg.DeviceIPs == nil {
			cfg.DeviceIPs = map[string]string{}
		}
		cfg.DeviceIPs[os.Getenv(EnvPrefix+"_DUID")] = value
	}
}

func normalize(cfg *Config) {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.DefaultDUID = strings.TrimSpace(cfg.DefaultDUID)
	if cfg.DeviceIPs != nil {
		cleaned := make(map[string]string, len(cfg.DeviceIPs))
		for k, v := range cfg.DeviceIPs {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if k != "" && v != "" {
				cleaned[k] = v
			}
		}
		cfg.DeviceIPs = cleaned
	}
}
