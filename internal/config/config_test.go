package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jwmoss/roborocli/internal/roborock"
)

func TestHostEnvSetsDefaultHost(t *testing.T) {
	t.Setenv(EnvPrefix+"_HOST", "192.168.2.54")
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultHost != "192.168.2.54" {
		t.Fatalf("DefaultHost = %q", cfg.DefaultHost)
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	contents := "base_url: https://file.example\nusername: file@example.com\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPrefix+"_BASE_URL", "https://env.example/")
	t.Setenv(EnvPrefix+"_EMAIL", "env@example.com")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://env.example" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.Username != "env@example.com" {
		t.Fatalf("Username = %q", cfg.Username)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, DefaultBaseURL)
	}
}

func TestSaveWritesConfigPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	cfg := Config{
		BaseURL:  "https://api.example.com",
		Username: "user@example.com",
		UserData: &roborock.UserData{RRiot: roborock.RRiot{U: "u1", S: "s1", H: "h1", K: "k1"}},
	}
	if err := Save(path, cfg, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0600 {
		t.Fatalf("mode = %v, want 0600", got)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UserData == nil || loaded.UserData.RRiot.K != "k1" {
		t.Fatalf("stored user data did not round trip: %+v", loaded.UserData)
	}
}

func TestSaveRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(path, Config{BaseURL: "https://a.example"}, false); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Config{BaseURL: "https://b.example"}, false); err == nil {
		t.Fatal("expected overwrite refusal")
	}
	if err := Save(path, Config{BaseURL: "https://b.example"}, true); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
}

func TestValidateAuthRequiresLogin(t *testing.T) {
	cfg := Config{Username: "user@example.com"}
	if err := cfg.ValidateAuth(); err == nil {
		t.Fatal("expected auth error without user data")
	}
	cfg.UserData = &roborock.UserData{RRiot: roborock.RRiot{U: "user1"}}
	if err := cfg.ValidateAuth(); err != nil {
		t.Fatalf("ValidateAuth: %v", err)
	}
}

func TestRedactedHidesSecrets(t *testing.T) {
	cfg := Config{
		BaseURL:  "https://usiot.roborock.com",
		Username: "user@example.com",
		UserData: &roborock.UserData{RRiot: roborock.RRiot{U: "u1", H: "secret-h", K: "secret-k"}},
	}
	redacted := cfg.Redacted()
	if redacted["logged_in"] != true {
		t.Fatalf("logged_in = %v", redacted["logged_in"])
	}
	data := cfg.Redacted()["api_url"]
	if data != "" {
		t.Fatalf("unexpected api_url %v", data)
	}
}
