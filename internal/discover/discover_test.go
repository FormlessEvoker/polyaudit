package discover

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/FormlessEvoker/polyaudit/internal/policy"
)

func TestWideTreeDoesNotDeadlock(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		for j := 0; j < 10; j++ {
			p := filepath.Join(dir, fmt.Sprintf("group%02d/project%02d", i, j))
			if err := os.MkdirAll(p, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(p, "package.json"), []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	projects, diagnostics, err := Scan(ctx, r, policy.Policy{}, 2)
	if err != nil || len(diagnostics) > 0 || len(projects) != 200 {
		t.Fatalf("wide traversal failed: %d projects, %v, %v", len(projects), diagnostics, err)
	}
}

func TestCanceledDiscovery(t *testing.T) {
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = Scan(ctx, r, policy.Policy{}, 4)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
}
