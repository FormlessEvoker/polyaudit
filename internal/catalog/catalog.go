// Package catalog coordinates discovery, per-project work, and publication.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"polyaudit/internal/bundle"
	"polyaudit/internal/clean"
	"polyaudit/internal/discover"
	"polyaudit/internal/manifest"
	"polyaudit/internal/model"
	"polyaudit/internal/policy"
)

type Options struct {
	Root, Output, Version        string
	Workers                      int
	Clean, DryRun, Gzip          bool
	MaxFileBytes, MaxBundleBytes int64
	Excludes                     []string
}

func Defaults() Options {
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	return Options{Root: ".", Version: "dev", Workers: workers, MaxFileBytes: 1 << 20, MaxBundleBytes: 32 << 20}
}

func (o *Options) prepare() (policy.Policy, error) {
	rules := policy.Policy{}
	if o.Workers < 1 || o.Workers > 256 {
		return rules, fmt.Errorf("workers must be between 1 and 256")
	}
	if o.MaxFileBytes < 1 || o.MaxFileBytes > 1<<30 {
		return rules, fmt.Errorf("max-file-bytes must be between 1 and 1073741824")
	}
	if o.MaxBundleBytes < 1024 || o.MaxBundleBytes > 1<<40 {
		return rules, fmt.Errorf("max-bundle-bytes must be between 1024 and 1099511627776")
	}
	abs, err := filepath.Abs(o.Root)
	if err != nil {
		return rules, err
	}
	o.Root, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return rules, err
	}
	if o.Output == "" {
		o.Output = filepath.Join(o.Root, "_ai_audit_ready")
	}
	o.Output, err = futurePath(o.Output)
	if err != nil {
		return rules, err
	}
	back, err := filepath.Rel(o.Output, o.Root)
	if err != nil {
		return rules, err
	}
	if back == "." || filepath.IsLocal(back) {
		return rules, fmt.Errorf("output must not equal or contain the scan root")
	}
	rel, err := filepath.Rel(o.Root, o.Output)
	if err != nil {
		return rules, err
	}
	if filepath.IsLocal(rel) {
		rules.OutputRelative = filepath.ToSlash(rel)
	}
	for _, s := range o.Excludes {
		e, err := policy.ValidateExclude(s)
		if err != nil {
			return rules, err
		}
		rules.Excludes = append(rules.Excludes, e)
	}
	o.Excludes = rules.Excludes
	return rules, nil
}

// Resolve existing ancestors so the scan/output overlap checks also work when
// callers use system aliases such as /tmp -> /private/tmp on macOS.
func futurePath(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("output directory must not be a symlink")
	}
	current := abs
	var tail []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			base, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				base = filepath.Join(base, tail[i])
			}
			return base, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		tail = append(tail, filepath.Base(current))
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		current = parent
	}
}

func Run(ctx context.Context, opts Options) (*model.Inventory, error) {
	rules, err := opts.prepare()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var out *output
	if !opts.DryRun {
		out, err = openOutput(opts.Output)
		if err != nil {
			return nil, err
		}
		defer out.close()
	}
	candidates, diagnostics, err := discover.Scan(ctx, root, rules, opts.Workers)
	if err != nil {
		return nil, err
	}
	inv := &model.Inventory{SchemaVersion: 1, ToolVersion: opts.Version, GeneratedAt: time.Now().UTC().Format(time.RFC3339), ScanRoot: opts.Root, OutputDirectory: opts.Output, DryRun: opts.DryRun, Projects: make([]model.Project, len(candidates)), Diagnostics: diagnostics,
		Settings: model.Settings{Workers: opts.Workers, Clean: opts.Clean, Gzip: opts.Gzip, MaxFileBytes: opts.MaxFileBytes, MaxBundleBytes: opts.MaxBundleBytes, Excludes: opts.Excludes}}
	boundaries := make(map[string]bool, len(candidates))
	ids := make(map[string]string, len(candidates))
	for _, c := range candidates {
		boundaries[c.Path] = true
		hash := sha256.Sum256([]byte(c.Path))
		ids[c.Path] = "p-" + hex.EncodeToString(hash[:])
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range opts.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					continue
				}
				inv.Projects[index] = process(ctx, root, candidates[index], ids, boundaries, rules, out, opts)
			}
		}()
	}
	for i := range candidates {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if out != nil {
		if err = out.publish(inv); err != nil {
			return nil, fmt.Errorf("publish inventory: %w", err)
		}
	}
	return inv, nil
}

func process(ctx context.Context, root *os.Root, c discover.Candidate, ids map[string]string, boundaries map[string]bool, rules policy.Policy, out *output, opts Options) model.Project {
	p := model.Project{ID: ids[c.Path], Path: c.Path, Name: path.Base(c.Path), Languages: []string{}, Manifests: []model.Manifest{}}
	if p.Name == "." {
		p.Name = filepath.Base(opts.Root)
	}
	if c.Path != "." {
		for parent := path.Dir(c.Path); ; parent = path.Dir(parent) {
			if id, ok := ids[parent]; ok {
				p.ParentID = id
				break
			}
			if parent == "." {
				break
			}
		}
	}
	languages := map[string]bool{}
	for _, file := range c.Signatures {
		m := manifest.Read(ctx, root, c.Path, file)
		p.Manifests = append(p.Manifests, m)
		languages[m.Language] = true
		if p.PrimaryLanguage == "" {
			p.PrimaryLanguage = m.Language
			if m.Name != "" {
				p.Name = m.Name
			}
		}
	}
	for lang := range languages {
		p.Languages = append(p.Languages, lang)
	}
	sort.Strings(p.Languages)
	if opts.Clean {
		p.Cleanup, p.Diagnostics = clean.Run(ctx, root, c.Path, boundaries, rules, opts.DryRun)
	}
	if opts.DryRun || ctx.Err() != nil {
		return p
	}
	filename := p.ID + ".txt"
	if opts.Gzip {
		filename += ".gz"
	}
	staged := path.Join(out.stage, filename)
	f, err := out.root.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		p.Diagnostics = append(p.Diagnostics, model.Error(c.Path, err.Error()))
		return p
	}
	b, diagnostics, writeErr := bundle.Write(ctx, root, p, boundaries, rules, f, bundle.Options{MaxFileBytes: opts.MaxFileBytes, MaxBundleBytes: opts.MaxBundleBytes, Gzip: opts.Gzip})
	p.Diagnostics = append(p.Diagnostics, diagnostics...)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = out.root.Remove(staged)
		p.Diagnostics = append(p.Diagnostics, model.Error(c.Path, "bundle: "+writeErr.Error()))
		return p
	}
	b.Path = path.Join(out.final, filename)
	p.Bundle = b
	sort.Slice(p.Cleanup, func(i, j int) bool { return p.Cleanup[i].Path < p.Cleanup[j].Path })
	return p
}

// Summary provides stable counts without exposing source contents.
func Summary(inv *model.Inventory) string {
	files, omitted, deleted := 0, 0, 0
	var bytes int64
	for _, p := range inv.Projects {
		if p.Bundle != nil {
			files += p.Bundle.Files
			omitted += p.Bundle.Omitted
			bytes += p.Bundle.Bytes
		}
		for _, a := range p.Cleanup {
			if a.Status == "deleted" {
				deleted++
			}
		}
	}
	return strings.TrimSpace(fmt.Sprintf("%d projects; %d bundled files; %d bytes; %d omissions; %d directories deleted", len(inv.Projects), files, bytes, omitted, deleted))
}
