package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// yamlUnmarshal parses the stored config for assertions.
func yamlUnmarshal(data []byte, out any) error { return yaml.Unmarshal(data, out) }

// TestLoginFlowSignsInAndStoresCredentials walks the whole login sequence the
// CLI uses against a mock gateway: send code, sign the challenge, exchange
// the code, then read the stored config.
func TestLoginFlowSignsInAndStoresCredentials(t *testing.T) {
	var (
		signedKey string
		loginForm map[string]string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f, _ := os.OpenFile("/tmp/roborocli-login-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		fmt.Fprintf(f, "%s %s\n", r.Method, r.URL.String())
		f.Close()
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v1/getUrlByEmail"):
			_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":{"url":"` + "http://" + r.Host +
				`","country":"US","countrycode":"1"}}`))
		case strings.HasSuffix(r.URL.Path, "/api/v4/email/code/send"):
			_, _ = w.Write([]byte(`{"code":200,"msg":"ok"}`))
		case strings.HasSuffix(r.URL.Path, "/api/v3/key/sign"):
			signedKey = r.URL.Query().Get("s")
			_, _ = w.Write([]byte(`{"code":200,"data":{"k":"signed-` + signedKey + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/api/v4/auth/email/login/code"):
			loginForm = map[string]string{
				"email":       r.FormValue("email"),
				"code":        r.FormValue("code"),
				"country":     r.FormValue("country"),
				"countryCode": r.FormValue("countryCode"),
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{
				"rriot":{"u":"user-1","s":"sess-1","h":"hawk-1","k":"key-1",
					"r":{"a":"` + "http://" + r.Host + `","m":"ssl://mqtt.example.com:8883"}},
				"token":"tok-1","region":"us","country":"US"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"msg":"not found"}`))
		}
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("ROBOROCLI_BASE_URL", server.URL)
	t.Setenv("ROBOROCLI_DISCOVER_REGIONS", "1")
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(),
		[]string{"--config", path, "login", "--email", "user@example.com", "--code", "123456"},
		strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("login failed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	if loginForm["code"] != "123456" || loginForm["email"] != "user@example.com" {
		t.Fatalf("login form = %+v", loginForm)
	}
	if loginForm["country"] != "US" || loginForm["countryCode"] != "1" {
		t.Fatalf("login region fields = %+v", loginForm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var stored struct {
		Username string `yaml:"username"`
		UserData struct {
			RRiot struct {
				U string `yaml:"u"`
				H string `yaml:"h"`
				K string `yaml:"k"`
			} `yaml:"rriot"`
			Token string `yaml:"token"`
		} `yaml:"user_data"`
	}
	if err := yamlUnmarshal(data, &stored); err != nil {
		t.Fatalf("parse stored config: %v", err)
	}
	if stored.Username != "user@example.com" {
		t.Fatalf("stored username = %q", stored.Username)
	}
	if stored.UserData.RRiot.U != "user-1" || stored.UserData.RRiot.H != "hawk-1" || stored.UserData.Token != "tok-1" {
		t.Fatalf("stored user data = %+v", stored.UserData)
	}

	// The stored credentials must work for follow-up commands.
	stdout.Reset()
	stderr.Reset()
	code = Execute(context.Background(),
		[]string{"--config", path, "--json", "whoami"},
		strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("whoami after login: code=%d stderr=%q", code, stderr.String())
	}
	var whoami map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &whoami); err != nil {
		t.Fatalf("decode whoami: %v", err)
	}
	if whoami["logged_in"] != true {
		t.Fatalf("whoami logged_in = %v", whoami["logged_in"])
	}
}
