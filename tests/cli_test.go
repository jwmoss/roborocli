package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cli-tests-*")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "cli.exe")
	build := exec.Command("go", "build", "-o", binary, "../cmd/roborocli")
	if output, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		panic(fmt.Sprintf("build CLI: %v\n%s", err, output))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func run(t *testing.T, path, input string, env []string, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, append([]string{"--config", path}, args...)...)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "ROBOROCLI_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "NO_COLOR=1")
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if ctx.Err() != nil {
		t.Fatal("CLI exceeded its deadline")
	}
	return code, stdout.String(), stderr.String()
}

// writeConfig stores a config file that points at a test server.
func writeConfig(t *testing.T, baseURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := fmt.Sprintf(`base_url: %s
username: user@example.com
client_id: test-client-id
user_data:
  rriot:
    u: user-1
    s: sess-secret
    h: hawk-secret
    k: device-secret
    r:
      a: %s
      m: ssl://mqtt.example.com:8883
  region: us
`, baseURL, baseURL)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocalContracts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	code, stdout, stderr := run(t, path, "", nil, "config", "init", "--json", "--email", "user@example.com")
	if code != 0 || !json.Valid([]byte(stdout)) || stderr != "" {
		t.Fatalf("config init: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run(t, path, "", nil, "config", "show", "--json")
	var cfg map[string]any
	if err := json.Unmarshal([]byte(stdout), &cfg); err != nil || code != 0 || cfg["path"] != path {
		t.Fatalf("config show: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if cfg["username"] != "user@example.com" {
		t.Fatalf("config show username = %v", cfg["username"])
	}
	if strings.Contains(stdout, "hawk-secret") || strings.Contains(stdout, "device-secret") {
		t.Fatal("config show leaked cloud credentials")
	}

	if err := os.WriteFile(path, []byte("user_data: [\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--version"}, {"version", "--json"}, {"--help"}, {"completion", "bash"},
	} {
		code, stdout, stderr = run(t, path, "", nil, args...)
		if code != 0 || stdout == "" || stderr != "" {
			t.Errorf("%v depends on config: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}

func TestUsageExitCodes(t *testing.T) {
	for _, args := range [][]string{
		{"--unknown"}, {"unknown-command"}, {"version", "extra"},
		{"raw", "POST", "/", "--body", "{"},
		{"control", "rooms"}, {"dnd", "set", "25:00", "06:00"},
		{"version", "--json", "--plain"}, {"config", "show", "--json", "--plain"},
		{"doctor", "--timeout", "0s", "--base-url", "http://127.0.0.1:1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, stdout, stderr := run(t, filepath.Join(t.TempDir(), "config.yaml"), "", nil, args...)
			if code != 2 || stdout != "" || stderr == "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestConfigWriteSafety(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	code, _, _ := run(t, path, "", nil, "--dry-run", "config", "init")
	if _, err := os.Stat(path); !os.IsNotExist(err) || code == 0 {
		t.Fatal("dry-run created a config file or reported success")
	}
	original := []byte("base_url: https://example.invalid\n")
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "init"}, {"--dry-run", "config", "init", "--force"},
	} {
		code, _, _ = run(t, path, "", nil, args...)
		data, err := os.ReadFile(path)
		if err != nil || code == 0 || !bytes.Equal(data, original) {
			t.Fatalf("%v changed existing config", args)
		}
	}
	code, _, stderr := run(t, path, "", nil, "config", "init", "--force", "--email", "user@example.com")
	if code != 0 {
		t.Fatal(stderr)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	before, _ := os.ReadFile(path)
	code, _, _ = run(t, link, "", nil, "config", "init", "--force")
	after, _ := os.ReadFile(path)
	if code == 0 || !bytes.Equal(before, after) {
		t.Fatal("config init followed a destination symlink")
	}
}

func TestCredentialBoundaries(t *testing.T) {
	captured := make(chan http.Header, 8)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- r.Header.Clone()
		_, _ = io.WriteString(w, `{}`)
	}))
	defer sink.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, sink.URL, http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"msg":"invalid credential"}`)
	}))
	defer origin.Close()

	path := writeConfig(t, origin.URL)

	// A cross-origin redirect must not carry the Hawk header to another host.
	code, stdout, stderr := run(t, path, "", nil, "raw", "GET", "/redirect")
	if code == 0 {
		t.Error("cross-origin redirect succeeded")
	}
	if !strings.Contains(stderr, "redirect") {
		t.Errorf("unexpected redirect error: %q", stderr)
	}
	_ = stdout
	select {
	case header := <-captured:
		if header.Get("Authorization") != "" {
			t.Error("redirect leaked the Hawk header")
		}
	default:
	}

	// Cloud errors must not echo stored secrets.
	code, stdout, stderr = run(t, path, "", nil, "raw", "GET", "/error")
	if code != 1 || stdout != "" {
		t.Errorf("API error: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	_ = stdout
	for _, secret := range []string{"hawk-secret", "device-secret", "sess-secret"} {
		if strings.Contains(stderr, secret) {
			t.Errorf("stderr leaked %q", secret)
		}
	}
}

func TestRawJSONContract(t *testing.T) {
	const exact = `{"id":9007199254740993}`
	bodies := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			bodies <- string(body)
		}
		_, _ = io.WriteString(w, exact)
	}))
	defer server.Close()
	path := writeConfig(t, server.URL)

	code, stdout, stderr := run(t, path, "", nil, "raw", "POST", "/", "--body", exact, "--json")
	if code != 0 || !strings.Contains(stdout, "9007199254740993") {
		t.Fatalf("JSON precision: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if sent := <-bodies; !strings.Contains(sent, "9007199254740993") {
		t.Fatalf("request body lost precision: %q", sent)
	}
}

func TestDryRunRefusesMutations(t *testing.T) {
	path := writeConfig(t, "http://127.0.0.1:1")
	for _, args := range [][]string{
		{"control", "start"}, {"control", "pause"}, {"control", "stop"},
		{"control", "charge"}, {"control", "rooms", "16"},
		{"logout"}, {"login", "--email", "user@example.com"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, _, stderr := run(t, path, "", nil, append([]string{"--dry-run"}, args...)...)
			if code == 0 || !strings.Contains(stderr, "dry-run") {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
		})
	}
}
