package catalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FormlessEvoker/polyaudit/internal/model"
)

func put(t *testing.T, root, name, value string) {
	t.Helper()
	full := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}
func options(root string) Options { o := Defaults(); o.Root = root; o.Workers = 3; return o }
func run(t *testing.T, o Options) *model.Inventory {
	t.Helper()
	i, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func contents(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func project(t *testing.T, i *model.Inventory, p string) model.Project {
	t.Helper()
	for _, v := range i.Projects {
		if v.Path == p {
			return v
		}
	}
	t.Fatalf("missing project %s", p)
	return model.Project{}
}

func TestCatalogBoundariesAndDeterminism(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "package.json", `{"name":"parent"}`)
	put(t, dir, "main.js", "parent source\n")
	put(t, dir, "packages/child/go.mod", "module example.test/child\n\ngo 1.26.0\n")
	put(t, dir, "packages/child/main.go", "package child\n")
	put(t, dir, "node_modules/fake/package.json", `{"name":"must-not-discover"}`)
	put(t, dir, ".env", "PASSWORD=secret")
	put(t, dir, "binary.js", "\x00binary")
	if err := os.Symlink("main.js", filepath.Join(dir, "alias.js")); err != nil {
		t.Fatal(err)
	}
	o := options(dir)
	first := run(t, o)
	if first.HasErrors() || len(first.Projects) != 2 {
		t.Fatalf("unexpected inventory: %+v", first)
	}
	p := project(t, first, ".")
	child := project(t, first, "packages/child")
	if child.ParentID != p.ID {
		t.Fatal("missing parent edge")
	}
	pack := contents(t, filepath.Join(first.OutputDirectory, p.Bundle.Path))
	if strings.Contains(string(pack), "package child") || strings.Contains(string(pack), "PASSWORD") || strings.Contains(string(pack), "binary source") {
		t.Fatalf("unexpected content: %s", pack)
	}
	if p.Bundle.Files != 2 || child.Bundle.Files != 2 {
		t.Fatalf("unexpected files: %+v %+v", p.Bundle, child.Bundle)
	}
	seen := map[string]string{}
	for _, v := range p.Bundle.Omissions {
		seen[v.Path] = v.Reason
	}
	if seen["packages/child"] != "nested_project" || seen[".env"] != "sensitive_filename" || seen["alias.js"] != "symlink" || seen["binary.js"] != "binary_or_non_utf8" {
		t.Fatalf("missing exclusions: %v", seen)
	}
	second := run(t, o)
	p2 := project(t, second, ".")
	if p.Bundle.SHA256 != p2.Bundle.SHA256 || !bytes.Equal(pack, contents(t, filepath.Join(second.OutputDirectory, p2.Bundle.Path))) {
		t.Fatal("packs changed between identical scans")
	}
	if p.Bundle.Path == p2.Bundle.Path {
		t.Fatal("expected immutable generation paths")
	}
	if _, err := os.Stat(filepath.Join(first.OutputDirectory, p.Bundle.Path)); err != nil {
		t.Fatal("old generation was removed")
	}
	var saved model.Inventory
	if err := json.Unmarshal(contents(t, filepath.Join(second.OutputDirectory, "catalog_inventory.json")), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Projects[0].Bundle.Path != second.Projects[0].Bundle.Path {
		t.Fatal("inventory did not commit latest generation")
	}
}

func TestCleanDryRunAndScope(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	put(t, outside, "keep.txt", "keep")
	put(t, dir, "app/package.json", `{"name":"app"}`)
	put(t, dir, "app/src/main.js", "source")
	for _, name := range []string{"node_modules", "_build", "deps", "dist", ".next", "target"} {
		put(t, dir, "app/"+name+"/artifact.txt", "artifact")
	}
	put(t, dir, "app/custom/dist/keep.txt", "excluded")
	put(t, dir, "loose/dist/keep.txt", "outside project")
	if err := os.Symlink(outside, filepath.Join(dir, "app/src/target")); err != nil {
		t.Fatal(err)
	}
	o := options(dir)
	o.Clean = true
	o.DryRun = true
	o.Excludes = []string{"app/custom/dist/keep.txt"}
	i := run(t, o)
	if len(i.Projects) != 1 || len(i.Projects[0].Cleanup) != 7 {
		t.Fatalf("unexpected cleanup: %+v", i)
	}
	if _, err := os.Stat(filepath.Join(dir, "_ai_audit_ready")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote output")
	}
	if _, err := os.Stat(filepath.Join(dir, "app/node_modules/artifact.txt")); err != nil {
		t.Fatal("dry run removed files")
	}
	o.DryRun = false
	i = run(t, o)
	if i.HasErrors() {
		t.Fatalf("cleanup failed: %+v", i)
	}
	for _, name := range []string{"node_modules", "_build", "deps", "dist", ".next", "target"} {
		if _, err := os.Lstat(filepath.Join(dir, "app", name)); !os.IsNotExist(err) {
			t.Fatalf("artifact remains: %s", name)
		}
	}
	for _, name := range []string{"app/src/main.js", "app/custom/dist/keep.txt", "loose/dist/keep.txt", "app/src/target/keep.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("protected file lost: %s: %v", name, err)
		}
	}
}

func TestCleanProtectsOutputInsideBuildDirectory(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "package.json", "{}")
	put(t, dir, "dist/keep.txt", "keep")
	o := options(dir)
	o.Clean = true
	o.Output = filepath.Join(dir, "dist", "audit")
	i := run(t, o)
	if i.HasErrors() {
		t.Fatalf("unexpected diagnostics: %+v", i)
	}
	if _, err := os.Stat(filepath.Join(dir, "dist/keep.txt")); err != nil {
		t.Fatal("output parent was deleted")
	}
}

func TestPartialFailureStillPublishes(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "bad/package.json", "{")
	put(t, dir, "good/package.json", `{"name":"good"}`)
	i := run(t, options(dir))
	if !i.HasErrors() || len(i.Projects) != 2 || project(t, i, "good").Bundle == nil {
		t.Fatalf("partial results missing: %+v", i)
	}
	_ = contents(t, filepath.Join(i.OutputDirectory, "catalog_inventory.json"))
}

func TestCancellationPreservesPriorInventory(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "go.mod", "module example.test/cancel\ngo 1.26.0\n")
	o := options(dir)
	i := run(t, o)
	before := contents(t, filepath.Join(i.OutputDirectory, "catalog_inventory.json"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, o)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if !bytes.Equal(before, contents(t, filepath.Join(i.OutputDirectory, "catalog_inventory.json"))) {
		t.Fatal("previous inventory changed")
	}
}

func TestGzipAndLimits(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "package.json", "{}")
	put(t, dir, "a.js", strings.Repeat("a", 2000))
	put(t, dir, "b.js", strings.Repeat("b", 700))
	put(t, dir, "c.js", strings.Repeat("c", 700))
	o := options(dir)
	o.MaxFileBytes = 1000
	o.MaxBundleBytes = 1100
	o.Gzip = true
	i := run(t, o)
	p := i.Projects[0]
	data := contents(t, filepath.Join(i.OutputDirectory, p.Bundle.Path))
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if len(plain) > 1100 || p.Bundle.Compression != "gzip" {
		t.Fatal("compression or limit incorrect")
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != p.Bundle.SHA256 || int64(len(data)) != p.Bundle.Bytes {
		t.Fatal("checksum/size mismatch")
	}
	reasons := map[string]bool{}
	for _, v := range p.Bundle.Omissions {
		reasons[v.Reason] = true
	}
	if !reasons["file_size_limit"] || !reasons["bundle_size_limit"] {
		t.Fatalf("missing limit details: %+v", p.Bundle)
	}
	j := run(t, o)
	if p.Bundle.SHA256 != j.Projects[0].Bundle.SHA256 {
		t.Fatal("gzip output is not deterministic")
	}
}

func TestValidationAndOutputSymlink(t *testing.T) {
	dir := t.TempDir()
	for _, mutate := range []func(*Options){func(o *Options) { o.Workers = 0 }, func(o *Options) { o.Output = dir }, func(o *Options) { o.Output = filepath.Dir(dir) }, func(o *Options) { o.Excludes = []string{"../outside"} }, func(o *Options) { o.MaxFileBytes = -1 }} {
		o := options(dir)
		mutate(&o)
		if _, err := Run(context.Background(), o); err == nil {
			t.Fatal("expected validation error")
		}
	}
	outside := t.TempDir()
	link := filepath.Join(dir, "output")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	o := options(dir)
	o.Output = link
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("symlink output accepted")
	}
}

func TestOutputLock(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "package.json", "{}")
	put(t, dir, "_ai_audit_ready/.polyaudit.lock", "")
	if _, err := Run(context.Background(), options(dir)); err == nil {
		t.Fatal("concurrent output lock ignored")
	}
}

func TestExternalSymlinkIsNotDiscovered(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	put(t, outside, "package.json", `{"name":"outside"}`)
	if err := os.Symlink(outside, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	i := run(t, options(dir))
	if len(i.Projects) != 0 {
		t.Fatal("symlink project was discovered")
	}
}

func TestFailedPublicationPreservesInventory(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "package.json", "{}")
	put(t, dir, "_ai_audit_ready/catalog_inventory.json", "previous inventory")
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "_ai_audit_ready/repo_packs")); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), options(dir)); err == nil {
		t.Fatal("symlink pack destination accepted")
	}
	if string(contents(t, filepath.Join(dir, "_ai_audit_ready/catalog_inventory.json"))) != "previous inventory" {
		t.Fatal("old inventory damaged")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "_ai_audit_ready"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") || e.Name() == ".polyaudit.lock" {
			t.Fatalf("failed run left temporary file: %s", e.Name())
		}
	}
}

func BenchmarkCatalog(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 100; i++ {
		p := filepath.Join(dir, fmt.Sprintf("app%03d", i))
		if err := os.MkdirAll(p, 0700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "package.json"), []byte(`{"name":"benchmark"}`), 0600); err != nil {
			b.Fatal(err)
		}
		for j := 0; j < 5; j++ {
			if err := os.WriteFile(filepath.Join(p, fmt.Sprintf("source%d.js", j)), []byte(strings.Repeat("// representative source\n", 100)), 0600); err != nil {
				b.Fatal(err)
			}
		}
	}
	o := options(dir)
	o.Workers = 8
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inv, err := Run(context.Background(), o)
		if err != nil {
			b.Fatal(err)
		}
		if inv.HasErrors() || len(inv.Projects) != 100 {
			b.Fatal("incomplete benchmark scan")
		}
	}
}
