package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jwmoss/roborocli/internal/config"
	"github.com/jwmoss/roborocli/internal/roborock"
)

// writeConfig creates a config file holding synthetic credentials.
func writeConfig(t *testing.T, serverURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		BaseURL:  serverURL,
		Username: "user@example.com",
		ClientID: "test-client-id",
		UserData: &roborock.UserData{
			RRiot: roborock.RRiot{
				U: "user-1",
				S: "sess-1",
				H: "hawk-key",
				K: "device-key",
				R: roborock.Reference{
					A: serverURL,
					M: "ssl://mqtt.example.com:8883",
				},
			},
			Region: "us",
		},
	}
	if err := config.Save(path, cfg, false); err != nil {
		t.Fatalf("save config: %v", err)
	}
	return path
}

func TestVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"--json", "version"}, strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"version": "dev"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestVersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"--version"}, strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "version dev") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRawUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"raw", "GET"}, strings.NewReader(""), &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("code = %d stderr = %s", code, stderr.String())
	}
}

func TestDoctorReportsProblemsWithoutCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":{"url":"` + "http://" + r.Host + `","country":"US","countrycode":"1"}}`))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("base_url: "+server.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"--config", path, "--json", "doctor"}, strings.NewReader(""), &stdout, &stderr)
	if code == exitOK {
		t.Fatalf("expected a non-zero exit code, stdout = %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"gateway"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRawSendsSignedRequest(t *testing.T) {
	var gotAuth, gotSDK string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotSDK = r.Header.Get("X-IOTSDK-VERSION")
		_, _ = w.Write([]byte(`{"success":true,"result":{"ok":true}}`))
	}))
	defer server.Close()

	path := writeConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(),
		[]string{"--config", path, "raw", "GET", "/user/homes/1"},
		strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d stderr = %s", code, stderr.String())
	}
	if !strings.HasPrefix(gotAuth, `Hawk id="user-1", s="sess-1"`) {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotSDK != "1.0.1" {
		t.Fatalf("X-IOTSDK-VERSION = %q", gotSDK)
	}
	if !strings.Contains(stdout.String(), `"ok":true`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestDevicesListsAccountDevices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "getUrlByEmail"):
			_, _ = w.Write([]byte(`{"code":200,"data":{"url":"` + "http://" + r.Host + `","country":"US","countrycode":"1"}}`))
		case strings.Contains(r.URL.Path, "getHomeDetail"):
			_, _ = w.Write([]byte(`{"code":200,"data":{"rrHomeId":42}}`))
		case strings.Contains(r.URL.Path, "/user/homes/42"):
			_, _ = w.Write([]byte(`{"success":true,"result":{
				"id":42,
				"name":"Home",
				"products":[{"id":"p1","name":"Q7","model":"roborock.vacuum.a40","category":"robot.vacuum.cleaner"}],
				"devices":[{"duid":"dev-1","name":"Vac","local_key":"local_key_16chr","product_id":"p1","pv":"1.0","fv":"1.2.3"}]
			}}`))
		default:
			_, _ = w.Write([]byte(`{"code":404,"msg":"not found"}`))
		}
	}))
	defer server.Close()

	path := writeConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(),
		[]string{"--config", path, "devices"},
		strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"dev-1", "Vac", "roborock.vacuum.a40", "1.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestCommandRefusesMutatingMethodUnderDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("username: user@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(),
		[]string{"--config", path, "--dry-run", "command", "app_start"},
		strings.NewReader(""), &stdout, &stderr)
	if code == exitOK {
		t.Fatal("expected dry-run refusal")
	}
	if !strings.Contains(stderr.String(), "dry-run") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestCommandAllowsReadOnlyMethodUnderDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("username: user@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// The read-only method passes the dry-run gate, then fails later because
	// no device is reachable. A dry-run refusal must not be the error.
	var stdout, stderr bytes.Buffer
	Execute(context.Background(),
		[]string{"--config", path, "--dry-run", "command", "get_status"},
		strings.NewReader(""), &stdout, &stderr)
	if strings.Contains(stderr.String(), "dry-run") {
		t.Fatalf("read-only command should not be refused: %s", stderr.String())
	}
}

func TestWhoamiShowsStoredAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	path := writeConfig(t, server.URL)

	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(),
		[]string{"--config", path, "--json", "whoami"},
		strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d stderr = %s", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode whoami output: %v (%s)", err, stdout.String())
	}
	if payload["email"] != "user@example.com" || payload["logged_in"] != true {
		t.Fatalf("whoami payload = %+v", payload)
	}
}

func TestSelectDeviceRequiresExplicitChoice(t *testing.T) {
	home := &roborock.HomeData{Devices: []roborock.Device{
		{DUID: "a", Name: "One"},
		{DUID: "b", Name: "Two"},
	}}
	if _, err := selectDevice(home, "", ""); err == nil {
		t.Fatal("expected an error when several devices exist")
	}
	device, err := selectDevice(home, "", "b")
	if err != nil || device.DUID != "b" {
		t.Fatalf("selectDevice = %+v, %v", device, err)
	}
	device, err = selectDevice(home, "One", "")
	if err != nil || device.DUID != "a" {
		t.Fatalf("selectDevice by name = %+v, %v", device, err)
	}
	if _, err := selectDevice(home, "zzz", ""); err == nil {
		t.Fatal("expected an error for an unknown device")
	}
}
