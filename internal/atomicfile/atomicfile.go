// Package atomicfile schreibt JSON-Dateien so, dass nach einem Absturz oder Stromausfall entweder
// die alte oder die neue Fassung dasteht – nie eine halbe.
package atomicfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// WriteJSON schreibt v eingerückt in eine temporäre Datei daneben, synchronisiert sie und benennt
// sie dann um. perm sind die Rechte der fertigen Datei.
func WriteJSON(path string, v any, perm os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return SyncDir(filepath.Dir(path))
}

// SyncDir sorgt unter Linux dafür, dass eine Umbenennung oder Löschung auch nach einem Stromausfall
// erhalten bleibt. Windows kann Verzeichnisse nicht synchronisieren; dort genügt das Rename.
func SyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
