package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/FormlessEvoker/polyaudit/internal/model"
)

func TestFlagsAfterRootAndDryRunJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"demo"}`), 0644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"scan", "--workers", "2", dir, "--clean", "--dry-run"}, &stdout, &stderr, "test")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var i model.Inventory
	if err := json.Unmarshal(stdout.Bytes(), &i); err != nil {
		t.Fatal(err)
	}
	if !i.DryRun || len(i.Projects) != 1 || i.ToolVersion != "test" {
		t.Fatalf("unexpected inventory: %+v", i)
	}
}

func TestExitCodes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args []string
		want int
	}{{[]string{"scan", dir, "--dry-run"}, 3}, {[]string{"scan", "--unknown"}, 2}, {[]string{"scan", "--workers"}, 2}, {[]string{"scan", "one", "two"}, 2}, {[]string{"scan", "--help"}, 0}, {[]string{"version"}, 0}} {
		var stdout, stderr bytes.Buffer
		got := Run(context.Background(), tt.args, &stdout, &stderr, "test")
		if got != tt.want {
			t.Errorf("%v: exit %d, want %d: %s", tt.args, got, tt.want, stderr.String())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if got := Run(ctx, []string{"scan", dir}, &out, &out, "test"); got != 130 {
		t.Fatalf("cancel exit %d", got)
	}
}
