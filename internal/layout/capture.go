package layout

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
)

// ErrMirrored reports a logical monitor showing more than one connector,
// which this tool does not pin.
var ErrMirrored = errors.New("mirrored logical monitors are not supported")

// Entry is one connected monitor with where mutter currently shows it, or
// a disabled placement when mutter shows it nowhere.
type Entry struct {
	Connector string
	Placement
}

// Snapshot lists every monitor in s: the shown ones ordered top to bottom
// then left to right, then the connected but switched-off ones as
// disabled placements in connector order. A monitor leased to another
// compositor is left out.
func Snapshot(s State) ([]Entry, error) {
	byConnector := make(map[string]Monitor, len(s.Monitors))
	for _, m := range s.Monitors {
		byConnector[m.Connector] = m
	}
	shown := make(map[string]bool, len(s.Logical))
	entries := make([]Entry, 0, len(s.Monitors))
	for _, l := range s.Logical {
		if len(l.Connectors) != 1 {
			return nil, fmt.Errorf("%w: logical monitor at %d,%d shows %d connectors", ErrMirrored, l.X, l.Y, len(l.Connectors))
		}
		c := l.Connectors[0]
		m, ok := byConnector[c]
		if !ok {
			return nil, fmt.Errorf("logical monitor at %d,%d names unknown connector %s", l.X, l.Y, c)
		}
		if shown[c] {
			return nil, fmt.Errorf("connector %s is shown by two logical monitors", c)
		}
		shown[c] = true
		p := Placement{
			ID:        m.ID,
			X:         l.X,
			Y:         l.Y,
			Scale:     l.Scale,
			Transform: l.Transform,
			Primary:   l.Primary,
			Mode:      currentMode(m),
		}
		entries = append(entries, Entry{Connector: c, Placement: p})
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(a.Y, b.Y), cmp.Compare(a.X, b.X))
	})
	off := make([]Entry, 0, len(s.Monitors))
	for _, m := range s.Monitors {
		if shown[m.Connector] || m.ForLease {
			continue
		}
		off = append(off, Entry{Connector: m.Connector, ID: m.ID, Disabled: true})
	}
	slices.SortFunc(off, func(a, b Entry) int { return cmp.Compare(a.Connector, b.Connector) })
	return append(entries, off...), nil
}

// Capture builds the Layout that pins s as it is, switched-off monitors
// included.
func Capture(s State) (Layout, error) {
	entries, err := Snapshot(s)
	if err != nil {
		return Layout{}, err
	}
	l := Layout{Monitors: make([]Placement, 0, len(entries))}
	for _, e := range entries {
		l.Monitors = append(l.Monitors, e.Placement)
	}
	return l, nil
}

func currentMode(m Monitor) string {
	for _, md := range m.Modes {
		if md.Current {
			return md.ID
		}
	}
	return ""
}
