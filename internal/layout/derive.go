package layout

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
)

// ErrNoPinnedMonitor reports a layout none of whose monitors is connected.
var ErrNoPinnedMonitor = errors.New("no pinned monitor is connected")

// ErrNotAdjacent reports a layout that compaction could not make one
// connected region, which mutter would refuse.
var ErrNotAdjacent = errors.New("layout cannot be made adjacent")

// ErrOverlap reports two cells sharing area, which happens only when a
// fallback mode is larger than the pinned one.
var ErrOverlap = errors.New("logical monitors overlap")

// candidates orders the connected monitors that share an identity, the
// ones mutter is showing first, so a pinned entry lands on a lit monitor
// before a dark duplicate.
func candidates(s State, shown map[string]bool) map[ID][]Monitor {
	byID := make(map[ID][]Monitor, len(s.Monitors))
	for _, m := range s.Monitors {
		if shown[m.Connector] && !m.ForLease {
			byID[m.ID] = append(byID[m.ID], m)
		}
	}
	for _, m := range s.Monitors {
		if !shown[m.Connector] && !m.ForLease {
			byID[m.ID] = append(byID[m.ID], m)
		}
	}
	return byID
}

// Derive builds the Target that pins l onto the monitors in s: pinned
// monitors are matched by identity (by order when identities repeat),
// absent ones are dropped and reported in Target.Missing, a pinned-off
// monitor is left out so mutter disables it, a band of the layout left
// with no monitor closes, survivors that touch only at corners are
// compacted, the result starts at the origin with exactly one primary,
// and monitors s shows that l does not pin are appended to the right and
// reported in Target.Unknown. A connected monitor s does not show stays
// off unless l pins it, a built-in panel s does not show is absent even
// when pinned, and a leased monitor is ignored.
func Derive(l Layout, s State) (Target, error) {
	shown := make(map[string]bool)
	for _, lg := range s.Logical {
		for _, c := range lg.Connectors {
			shown[c] = true
		}
	}
	byID := candidates(s, shown)

	t := Target{Serial: s.Serial, Units: s.Units, CanSetUnits: s.CanSetUnits}
	taken := make(map[ID]int, len(l.Monitors))
	pinned := make(map[string]bool, len(l.Monitors))
	var cells []box
	for _, p := range l.Monitors {
		options := byID[p.ID]
		k := taken[p.ID]
		if k >= len(options) {
			t.Missing = append(t.Missing, p.ID)
			continue
		}
		m := options[k]
		taken[p.ID] = k + 1
		pinned[m.Connector] = true
		if len(options) > 1 {
			t.Warnings = append(t.Warnings, fmt.Sprintf("%d monitors share identity %s %s %s; entry %d pinned to %s by order",
				len(options), p.Vendor, p.Product, p.Serial, k+1, m.Connector))
		}
		if p.Disabled {
			continue
		}
		if m.Builtin && !shown[m.Connector] {
			t.Missing = append(t.Missing, p.ID)
			t.Warnings = append(t.Warnings, m.Connector+": built-in panel is not shown (closed lid?), leaving it off")
			continue
		}
		mode, note, ok := resolveMode(m, p.Mode)
		if !ok {
			mode, ok = preferredMode(m)
			if !ok {
				return Target{}, fmt.Errorf("monitor %s (%s %s) offers no modes", m.Connector, m.ID.Product, m.ID.Serial)
			}
			note = fmt.Sprintf("pinned mode %s unavailable, using preferred %s at the pinned position", p.Mode, mode.ID)
		}
		if note != "" {
			t.Warnings = append(t.Warnings, m.Connector+": "+note)
		}
		scale, warning := snapScale(mode, p.Scale)
		if warning != "" {
			t.Warnings = append(t.Warnings, m.Connector+": "+warning)
		}
		w, h := size(mode, scale, p.Transform, s.Units)
		cells = append(cells, box{
			Connector: m.Connector, Mode: mode.ID, X: p.X, Y: p.Y, Scale: scale, Transform: p.Transform, Primary: p.Primary,
			w: w, h: h,
		})
	}
	if len(cells) == 0 {
		return Target{}, ErrNoPinnedMonitor
	}

	normalize(cells)
	closeGaps(refs(cells), xAxis)
	closeGaps(refs(cells), yAxis)
	if err := overlapping(cells); err != nil {
		return Target{}, err
	}
	for !connected(cells) {
		moved := compact(cells)
		normalize(cells)
		if !moved {
			return Target{}, fmt.Errorf("%w: %s", ErrNotAdjacent, connectors(cells))
		}
	}
	if err := overlapping(cells); err != nil {
		return Target{}, err
	}
	ensurePrimary(cells)

	right, top := edge(cells)
	for _, m := range s.Monitors {
		if pinned[m.Connector] || !shown[m.Connector] || m.ForLease {
			continue
		}
		mode, ok := preferredMode(m)
		if !ok {
			return Target{}, fmt.Errorf("monitor %s (%s %s) offers no modes", m.Connector, m.ID.Product, m.ID.Serial)
		}
		scale := cmp.Or(mode.PreferredScale, 1)
		transform := shownTransform(s, m.Connector)
		w, h := size(mode, scale, transform, s.Units)
		cells = append(cells, box{Connector: m.Connector, Mode: mode.ID, X: right, Y: top, Scale: scale, Transform: transform, w: w, h: h})
		right += w
		t.Unknown = append(t.Unknown, m.Connector)
	}

	t.Cells = make([]Cell, 0, len(cells))
	for _, b := range cells {
		t.Cells = append(t.Cells, b.Cell)
	}
	return t, nil
}

// overlapping reports the first pair of cells sharing area, which
// compaction must never produce and a fallback mode larger than the
// pinned one can.
func overlapping(cells []box) error {
	if i, j, found := firstOverlap(cells); found {
		return fmt.Errorf("%w: %s and %s", ErrOverlap, cells[j].Connector, cells[i].Connector)
	}
	return nil
}

// shownTransform returns the transform mutter is driving connector with,
// so a monitor the layout does not pin keeps the rotation the user gave
// it rather than being straightened.
func shownTransform(s State, connector string) Transform {
	for _, l := range s.Logical {
		if slices.Contains(l.Connectors, connector) {
			return l.Transform
		}
	}
	return Normal
}
