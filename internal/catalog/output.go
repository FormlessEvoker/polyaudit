package catalog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"

	"polyaudit/internal/model"
)

// Each run gets immutable packs. The inventory is the commit point, replaced
// only after all referenced packs have been closed and moved into place.
type output struct {
	root  *os.Root
	lock  *os.File
	stage string
	final string
}

func openOutput(dir string) (*output, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	lock, err := r.OpenFile(".polyaudit.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		r.Close()
		return nil, fmt.Errorf("acquire output lock (another scan or stale lock): %w", err)
	}
	o := &output{root: r, lock: lock}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		o.close()
		return nil, err
	}
	id := hex.EncodeToString(nonce[:])
	o.stage = ".staging-" + id
	o.final = path.Join("repo_packs", id)
	if err = r.Mkdir(o.stage, 0700); err != nil {
		o.close()
		return nil, err
	}
	return o, nil
}

func (o *output) close() {
	if o.stage != "" {
		_ = o.root.RemoveAll(o.stage)
	}
	_ = o.lock.Close()
	_ = o.root.Remove(".polyaudit.lock")
	_ = o.root.Close()
}

func (o *output) publish(inv *model.Inventory) error {
	data, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp := path.Join(o.stage, "inventory.json")
	f, err := o.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if info, err := o.root.Lstat("repo_packs"); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("repo_packs must be a real directory")
	}
	if err = o.root.MkdirAll("repo_packs", 0700); err != nil {
		return err
	}
	if err = o.root.Rename(o.stage, o.final); err != nil {
		return err
	}
	// Rename over the old inventory is atomic on supported Unix filesystems.
	if err = o.root.Rename(path.Join(o.final, "inventory.json"), "catalog_inventory.json"); err != nil {
		return err
	}
	return nil
}
