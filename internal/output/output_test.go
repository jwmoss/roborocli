package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestJSON(t *testing.T) {
	var stdout bytes.Buffer
	formatter := New(&stdout, &bytes.Buffer{}, true, false, false, true)
	if err := formatter.JSON(map[string]string{"hello": "world"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"hello": "world"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestQuietSuppressesTable(t *testing.T) {
	var stdout bytes.Buffer
	formatter := New(&stdout, &bytes.Buffer{}, false, false, true, true)
	formatter.Table([]string{"A"}, [][]string{{"B"}})
	if stdout.String() != "" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestTermDumbDisablesColor(t *testing.T) {
	t.Setenv("TERM", "dumb")
	var stdout bytes.Buffer
	formatter := New(&stdout, &bytes.Buffer{}, false, false, false, false)
	formatter.Success("saved")
	if strings.Contains(stdout.String(), "\x1b[") {
		t.Fatalf("stdout contains color escape: %q", stdout.String())
	}
}
