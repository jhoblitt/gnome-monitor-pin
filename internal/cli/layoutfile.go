package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

// ErrLayoutExists reports a refusal to overwrite a layout file without
// --force.
var ErrLayoutExists = errors.New("layout file exists; pass --force to overwrite")

func readLayout(path string) (layout.Layout, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return layout.Layout{}, fmt.Errorf("no layout file at %s: arrange the monitors in Settings, then run save", path)
	}
	if err != nil {
		return layout.Layout{}, fmt.Errorf("reading layout %s: %w", path, err)
	}
	var l layout.Layout
	if err := json.Unmarshal(data, &l); err != nil {
		return layout.Layout{}, fmt.Errorf("parsing layout %s: %w", path, err)
	}
	if len(l.Monitors) == 0 {
		return layout.Layout{}, fmt.Errorf("layout %s pins no monitors", path)
	}
	return l, nil
}

// writeLayout writes l to path atomically, through a temporary file in the
// same directory and a rename, so a watch reloading the file never sees a
// torn write. A symlink at path is followed so its target is replaced and
// the link survives.
func writeLayout(path string, l layout.Layout, force bool) error {
	path = filepath.Clean(path)
	if _, err := os.Stat(path); err == nil {
		if !force {
			return fmt.Errorf("%w: %s", ErrLayoutExists, path)
		}
		if target, err := filepath.EvalSymlinks(path); err == nil {
			path = target
		}
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding layout: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".layout-*.json")
	if err != nil {
		return fmt.Errorf("creating temporary layout in %s: %w", dir, err)
	}
	defer removeQuietly(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(fmt.Errorf("writing layout %s: %w", tmp.Name(), err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing layout %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("moving layout into place at %s: %w", path, err)
	}
	return nil
}

// removeQuietly removes the temporary file a failed write left behind;
// after a successful rename there is nothing to remove, which is not an
// error worth reporting.
func removeQuietly(name string) {
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("removing temporary layout", slog.String("path", name), slog.Any("error", err))
	}
}
