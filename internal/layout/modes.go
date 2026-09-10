package layout

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// scaleTolerance is how far a scale may sit from a supported one and still
// be that one. Mutter's supported scales are at least 0.08 apart, so a
// hand-written 1.333 snaps to 1.3333333730697632 while 1.75 on a mode
// without it is refused.
const scaleTolerance = 0.01

// refreshTolerance is how far a fallback mode's refresh may sit from the
// pinned one before the substitution is worth a warning.
const refreshTolerance = 1.0

// preferredMode returns m's preferred mode, else its current one, else the
// first; ok is false when m offers no modes at all.
func preferredMode(m Monitor) (mode Mode, ok bool) {
	if len(m.Modes) == 0 {
		return Mode{}, false
	}
	if i := slices.IndexFunc(m.Modes, func(md Mode) bool { return md.Preferred }); i >= 0 {
		return m.Modes[i], true
	}
	if i := slices.IndexFunc(m.Modes, func(md Mode) bool { return md.Current }); i >= 0 {
		return m.Modes[i], true
	}
	return m.Modes[0], true
}

// resolveMode finds the mode want names: by ID, else a mode with the same
// resolution and the closest refresh rate, since mutter has renamed mode
// IDs across versions; note says so when the refresh moved by more than
// refreshTolerance. An empty want is the preferred mode. ok is false when
// want names a resolution m does not offer, or m offers nothing.
func resolveMode(m Monitor, want string) (mode Mode, note string, ok bool) {
	if want == "" {
		mode, ok = preferredMode(m)
		return mode, "", ok
	}
	if i := slices.IndexFunc(m.Modes, func(md Mode) bool { return md.ID == want }); i >= 0 {
		return m.Modes[i], "", true
	}
	width, height, refresh, parsed := parseModeID(want)
	if !parsed {
		return Mode{}, "", false
	}
	for _, md := range m.Modes {
		if md.Width != width || md.Height != height {
			continue
		}
		if !ok || math.Abs(md.Refresh-refresh) < math.Abs(mode.Refresh-refresh) {
			mode, ok = md, true
		}
	}
	if ok && math.Abs(mode.Refresh-refresh) > refreshTolerance {
		note = fmt.Sprintf("pinned mode %s unavailable, using %s", want, mode.ID)
	}
	return mode, note, ok
}

// parseModeID reads the "WxH@R" shape mutter gives every mode ID, ignoring
// an interlace marker after the height and a "+vrr" suffix after the rate.
func parseModeID(id string) (width, height int, refresh float64, ok bool) {
	res, rate, found := strings.Cut(id, "@")
	if !found {
		return 0, 0, 0, false
	}
	ws, hs, found := strings.Cut(res, "x")
	if !found {
		return 0, 0, 0, false
	}
	rate, _, _ = strings.Cut(rate, "+")
	width, errW := strconv.Atoi(ws)
	height, errH := strconv.Atoi(strings.TrimSuffix(hs, "i"))
	refresh, errR := strconv.ParseFloat(rate, 64)
	if errW != nil || errH != nil || errR != nil {
		return 0, 0, 0, false
	}
	return width, height, refresh, true
}

// snapScale returns the supported scale nearest want, which is the value
// mutter itself reports, so the applied and reported scales compare equal.
// Zero means the mode's preferred scale. A want no supported scale is near
// falls back to the preferred scale, and the warning says so. A mode that
// lists no supported scales gets want rounded to single precision, which
// is what mutter stores.
func snapScale(mode Mode, want float64) (scale float64, warning string) {
	preferred := cmp.Or(mode.PreferredScale, 1)
	if want == 0 {
		return preferred, ""
	}
	if len(mode.Scales) == 0 {
		return float64(float32(want)), ""
	}
	best := mode.Scales[0]
	for _, s := range mode.Scales[1:] {
		if math.Abs(s-want) < math.Abs(best-want) {
			best = s
		}
	}
	if math.Abs(best-want) <= scaleTolerance {
		return best, ""
	}
	return preferred, fmt.Sprintf("scale %g not supported by mode %s, using %g", want, mode.ID, preferred)
}

// size returns the width and height a mode occupies at scale under t. In
// logical units that is the native size divided by the scale, which is
// exact for every scale mutter offers; in physical units it is the native
// size.
func size(m Mode, scale float64, t Transform, u Units) (width, height int) {
	width, height = m.Width, m.Height
	if t.Rotated() {
		width, height = height, width
	}
	if u == UnitsPhysical {
		return width, height
	}
	return int(math.Round(float64(width) / scale)), int(math.Round(float64(height) / scale))
}
