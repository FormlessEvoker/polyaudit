// Package fsutil implements bounded reads through an anchored directory handle.
package fsutil

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// NoSymlinks rejects every symlink component. os.Root additionally prevents
// escape from the root if a concurrent filesystem change races this check.
func NoSymlinks(root *os.Root, name string) error {
	if name == "." {
		return nil
	}
	current := ""
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("invalid relative path %q", name)
		}
		current = path.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink excluded: %s", current)
		}
	}
	return nil
}

func ReadLimited(ctx context.Context, root *os.Root, name string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := NoSymlinks(root, name); err != nil {
		return nil, err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", name)
	}
	if info.Size() > limit {
		return nil, ErrTooLarge
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("file changed while opening: %s", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

var ErrTooLarge = fmt.Errorf("file exceeds size limit")

// ReadDir returns sorted entries; sorting is required for reproducible bundles.
func ReadDir(root *os.Root, name string) ([]os.DirEntry, error) {
	if err := NoSymlinks(root, name); err != nil {
		return nil, err
	}
	return readDir(root, name)
}
