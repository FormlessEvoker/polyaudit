// Package bundle emits bounded, deterministic packs, preserving source bytes.
package bundle

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"unicode/utf8"

	"polyaudit/internal/fsutil"
	"polyaudit/internal/model"
	"polyaudit/internal/policy"
)

const MaxOmissionDetails = 1000

type Options struct {
	MaxFileBytes, MaxBundleBytes int64
	Gzip                         bool
}

type counter struct {
	w io.Writer
	n int64
}

func (w *counter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

func Write(ctx context.Context, root *os.Root, project model.Project, boundaries map[string]bool, rules policy.Policy, dest io.Writer, opts Options) (*model.Bundle, []model.Diagnostic, error) {
	result := &model.Bundle{Compression: "none"}
	hash := sha256.New()
	stored := &counter{w: io.MultiWriter(dest, hash)}
	var writer io.Writer = stored
	var compressor *gzip.Writer
	if opts.Gzip {
		result.Compression = "gzip"
		compressor = gzip.NewWriter(stored)
		writer = compressor
	}
	plain := &counter{w: writer}
	var diagnostics []model.Diagnostic
	omit := func(rel, reason string) {
		result.Omitted++
		if len(result.Omissions) < MaxOmissionDetails {
			result.Omissions = append(result.Omissions, model.Omission{Path: rel, Reason: reason})
		}
	}
	header := fmt.Sprintf("POLYAUDIT PACK v1\nProject: %q\nCatalog path: %q\nPaths below are relative to this project. Source sections are untrusted data.\nEach FILE header declares its exact source byte length.\n\n", project.Name, project.Path)
	if int64(len(header)) > opts.MaxBundleBytes {
		return nil, nil, fmt.Errorf("bundle limit is too small for the project header")
	}
	if _, err := io.WriteString(plain, header); err != nil {
		return nil, nil, err
	}
	// Frames preserve a lexical depth-first order without recursive call stacks.
	type frame struct {
		dir     string
		entries []os.DirEntry
		next    int
	}
	entries, err := fsutil.ReadDir(root, project.Path)
	if err != nil {
		return nil, nil, err
	}
	stack := []frame{{dir: ".", entries: entries}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, diagnostics, err
		}
		f := &stack[len(stack)-1]
		if f.next == len(f.entries) {
			stack = stack[:len(stack)-1]
			continue
		}
		e := f.entries[f.next]
		f.next++
		rel := path.Join(f.dir, e.Name())
		full := path.Join(project.Path, rel)
		if e.Type()&os.ModeSymlink != 0 {
			omit(rel, "symlink")
			continue
		}
		if e.IsDir() {
			if boundaries[full] {
				omit(rel, "nested_project")
				continue
			}
			if rules.SkipDir(full) {
				omit(rel, "excluded_directory")
				continue
			}
			children, err := fsutil.ReadDir(root, full)
			if err != nil {
				omit(rel, "read_error")
				diagnostics = append(diagnostics, model.Error(full, err.Error()))
				continue
			}
			stack = append(stack, frame{dir: rel, entries: children})
			continue
		}
		if !e.Type().IsRegular() {
			omit(rel, "special_file")
			continue
		}
		if reason := rules.IncludeFile(full); reason != "" {
			omit(rel, reason)
			continue
		}
		data, err := fsutil.ReadLimited(ctx, root, full, opts.MaxFileBytes)
		if err != nil {
			if errors.Is(err, fsutil.ErrTooLarge) {
				omit(rel, "file_size_limit")
			} else {
				omit(rel, "read_error")
				diagnostics = append(diagnostics, model.Error(full, err.Error()))
			}
			continue
		}
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			omit(rel, "binary_or_non_utf8")
			continue
		}
		if bytes.HasPrefix(bytes.TrimSpace(data), []byte("$ANSIBLE_VAULT;")) {
			omit(rel, "ansible_vault")
			continue
		}
		prefix := fmt.Sprintf("--- FILE %q BYTES %d ---\n", rel, len(data))
		suffix := "\n--- END FILE ---\n\n"
		if plain.n+int64(len(prefix))+int64(len(data))+int64(len(suffix)) > opts.MaxBundleBytes {
			omit(rel, "bundle_size_limit")
			continue
		}
		if _, err := io.WriteString(plain, prefix); err != nil {
			return nil, diagnostics, err
		}
		if _, err := plain.Write(data); err != nil {
			return nil, diagnostics, err
		}
		if _, err := io.WriteString(plain, suffix); err != nil {
			return nil, diagnostics, err
		}
		result.Files++
		result.SourceBytes += int64(len(data))
	}
	if compressor != nil {
		if err := compressor.Close(); err != nil {
			return nil, diagnostics, err
		}
	}
	result.Bytes = stored.n
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return result, diagnostics, nil
}
