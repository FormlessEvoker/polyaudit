package bundle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/FormlessEvoker/polyaudit/internal/model"
	"github.com/FormlessEvoker/polyaudit/internal/policy"
)

type cancelWriter struct{ cancel context.CancelFunc }

func (w cancelWriter) Write(p []byte) (int, error) { w.cancel(); return len(p), nil }

func TestCancellationDuringBundle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, err = Write(ctx, r, model.Project{Name: "test", Path: "."}, nil, policy.Policy{}, cancelWriter{cancel}, Options{MaxFileBytes: 1024, MaxBundleBytes: 4096})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected mid-bundle cancellation, got %v", err)
	}
}

func TestBoundedOmissionDetails(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxOmissionDetails+5; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%04d.unsupported", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out bytes.Buffer
	b, _, err := Write(context.Background(), r, model.Project{Name: "test", Path: "."}, nil, policy.Policy{}, &out, Options{MaxFileBytes: 1024, MaxBundleBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if b.Omitted != MaxOmissionDetails+5 || len(b.Omissions) != MaxOmissionDetails {
		t.Fatalf("incorrect omission bounds: %d %d", b.Omitted, len(b.Omissions))
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteFailure(t *testing.T) {
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, _, err = Write(context.Background(), r, model.Project{Name: "test", Path: "."}, nil, policy.Policy{}, failingWriter{}, Options{MaxFileBytes: 1024, MaxBundleBytes: 4096})
	if err == nil || err.Error() != "disk full" {
		t.Fatalf("write error was lost: %v", err)
	}
}
