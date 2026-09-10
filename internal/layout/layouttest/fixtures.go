// Package layouttest provides the six-monitor fixtures the suites share:
// a 2x3 grid as the user pinned it, and the linear row mutter falls back
// to after a hotplug.
package layouttest

import (
	"fmt"
	"slices"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

// Width and Height are the fixture monitors' native size.
const (
	Width  = 1920
	Height = 1200
)

// ModeID is the fixture monitors' preferred and current mode.
const ModeID = "1920x1200@59.950"

// AltModeID is a second mode every fixture monitor offers, never preferred.
const AltModeID = "1920x1080@60.000"

// AltModeID50 is a third mode at the same resolution as AltModeID.
const AltModeID50 = "1920x1080@50.000"

// Scale43 is the single-precision value mutter reports for a 4/3 scale.
const Scale43 = 1.3333333730697632

type cell struct {
	connector string
	serial    string
	x, y      int
}

// The grid: connector names are unsorted on purpose.
var grid = []cell{
	{"DP-4", "SER-A", 0, 0},
	{"DP-22", "SER-B", Width, 0},
	{"DP-16", "SER-C", 2 * Width, 0},
	{"DP-6", "SER-D", 0, Height},
	{"DP-20", "SER-E", Width, Height},
	{"DP-9", "SER-F", 2 * Width, Height},
}

func modes() []layout.Mode {
	return []layout.Mode{
		{
			ID: ModeID, Width: Width, Height: Height, Refresh: 59.95, PreferredScale: 1,
			Scales:    []float64{1, 1.25, Scale43, 1.5, 1.6666666269302368, 2},
			Preferred: true, Current: true,
		},
		{
			ID: AltModeID, Width: 1920, Height: 1080, Refresh: 60, PreferredScale: 1,
			Scales: []float64{1, 1.25, 1.5, 2},
		},
		{
			ID: AltModeID50, Width: 1920, Height: 1080, Refresh: 50, PreferredScale: 1,
			Scales: []float64{1, 1.25, 1.5, 2},
		},
	}
}

// ModeSize returns the native size of a fixture mode, or of any mode ID
// of the form WxH@R, so a spec can size a cell the way mutter would.
func ModeSize(id string) (width, height int, ok bool) {
	for _, m := range modes() {
		if m.ID == id {
			return m.Width, m.Height, true
		}
	}
	var refresh float64
	if _, err := fmtSscanf(id, &width, &height, &refresh); err != nil {
		return 0, 0, false
	}
	return width, height, true
}

func fmtSscanf(id string, width, height *int, refresh *float64) (int, error) {
	return fmt.Sscanf(id, "%dx%d@%f", width, height, refresh)
}

// Monitors returns the six connected monitors.
func Monitors() []layout.Monitor {
	out := make([]layout.Monitor, 0, len(grid))
	for _, c := range grid {
		out = append(out, layout.Monitor{
			Connector: c.connector,
			ID:        layout.ID{Vendor: "DEL", Product: "DELL U2413", Serial: c.serial},
			Modes:     modes(),
		})
	}
	return out
}

// GridLayout returns the pinned 2x3 layout, primary at the origin.
func GridLayout() layout.Layout {
	l := layout.Layout{Monitors: make([]layout.Placement, 0, len(grid))}
	for _, c := range grid {
		l.Monitors = append(l.Monitors, layout.Placement{
			Vendor:  "DEL",
			Product: "DELL U2413",
			Serial:  c.serial,
			X:       c.x,
			Y:       c.y,
			Scale:   1,
			Primary: c.x == 0 && c.y == 0,
			Mode:    ModeID,
		})
	}
	return l
}

// GridState returns mutter's state when the grid is in effect.
func GridState() layout.State {
	s := layout.State{Serial: 7, Units: layout.UnitsLogical, CanSetUnits: true, Monitors: Monitors()}
	for _, c := range grid {
		s.Logical = append(s.Logical, layout.Logical{
			X: c.x, Y: c.y, Scale: 1, Primary: c.x == 0 && c.y == 0, Connectors: []string{c.connector},
		})
	}
	return s
}

// LinearState returns mutter's fallback: every monitor in one row, the
// primary first, the rest in monitor order.
func LinearState() layout.State {
	s := layout.State{Serial: 8, Units: layout.UnitsLogical, CanSetUnits: true, Monitors: Monitors()}
	x := 0
	for _, c := range grid {
		s.Logical = append(s.Logical, layout.Logical{
			X: x, Y: 0, Scale: 1, Primary: x == 0, Connectors: []string{c.connector},
		})
		x += Width
	}
	return s
}

// Without returns s with the named connectors unplugged: gone from the
// monitor list and from every logical monitor.
func Without(s layout.State, connectors ...string) layout.State {
	out := Disabled(s, connectors...)
	out.Monitors = slices.DeleteFunc(slices.Clone(s.Monitors), func(m layout.Monitor) bool {
		return slices.Contains(connectors, m.Connector)
	})
	return out
}

// Disabled returns s with the named connectors still connected but shown
// nowhere, as mutter reports a monitor switched off in Settings.
func Disabled(s layout.State, connectors ...string) layout.State {
	gone := func(c string) bool { return slices.Contains(connectors, c) }
	out := layout.State{Serial: s.Serial, Units: s.Units, CanSetUnits: s.CanSetUnits, Monitors: s.Monitors}
	for _, l := range s.Logical {
		l.Connectors = slices.DeleteFunc(slices.Clone(l.Connectors), gone)
		if len(l.Connectors) > 0 {
			out.Logical = append(out.Logical, l)
		}
	}
	return out
}

// WithSerial returns s carrying serial, as mutter reports it after a
// hardware re-read.
func WithSerial(s layout.State, serial uint32) layout.State {
	s.Serial = serial
	return s
}
