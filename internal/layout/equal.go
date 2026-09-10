package layout

import (
	"math"
	"slices"
)

// Equal reports whether s already shows t: every cell of t is shown at the
// same position, scale, transform, primary flag, and mode, and s shows
// nothing else.
func Equal(t Target, s State) bool {
	n := 0
	for _, l := range s.Logical {
		n += len(l.Connectors)
	}
	if n != len(t.Cells) {
		return false
	}
	for _, c := range t.Cells {
		if !shows(s, c) {
			return false
		}
	}
	return true
}

func shows(s State, c Cell) bool {
	for _, l := range s.Logical {
		if !slices.Contains(l.Connectors, c.Connector) {
			continue
		}
		return l.X == c.X && l.Y == c.Y && l.Transform == c.Transform && l.Primary == c.Primary &&
			math.Abs(l.Scale-c.Scale) < scaleTolerance && modeOf(s, c.Connector) == c.Mode
	}
	return false
}

func modeOf(s State, connector string) string {
	for _, m := range s.Monitors {
		if m.Connector == connector {
			return currentMode(m)
		}
	}
	return ""
}
