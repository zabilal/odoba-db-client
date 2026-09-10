package store

import (
	"os"
	"path/filepath"
)

// writeFileAtomic replaces a file so that a reader, or a crash, sees either
// the old contents or the new, never a torn mix of the two.
//
// The data goes to a temporary file in the same directory, which is fsynced
// and then renamed over the target. Rename within one filesystem is atomic.
// The directory is fsynced afterwards so that the rename itself survives a
// power cut. Without that, a POSIX system can come back with the old name
// pointing at nothing.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()

	if err = tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir makes a rename durable. Windows cannot open a directory for fsync,
// and NTFS journals renames itself, so the error is deliberately ignored.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
