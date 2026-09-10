package layout

import (
	"fmt"
	"slices"
	"strconv"
)

// Transform is a mutter viewport transform, numbered as the
// org.gnome.Mutter.DisplayConfig interface numbers them.
type Transform uint32

// The transforms mutter defines, in wire order.
const (
	Normal Transform = iota
	Rotate90
	Rotate180
	Rotate270
	Flipped
	Flipped90
	Flipped180
	Flipped270
)

var transformNames = [...]string{
	Normal:     "normal",
	Rotate90:   "90",
	Rotate180:  "180",
	Rotate270:  "270",
	Flipped:    "flipped",
	Flipped90:  "flipped-90",
	Flipped180: "flipped-180",
	Flipped270: "flipped-270",
}

// String returns the name gdctl and the layout file use.
func (t Transform) String() string {
	if int(t) < len(transformNames) {
		return transformNames[t]
	}
	return "transform(" + strconv.FormatUint(uint64(t), 10) + ")"
}

// MarshalText renders t by name.
func (t Transform) MarshalText() ([]byte, error) {
	if int(t) >= len(transformNames) {
		return nil, fmt.Errorf("unknown transform %d", uint32(t))
	}
	return []byte(transformNames[t]), nil
}

// UnmarshalText parses a transform name. The empty string is Normal, so a
// hand-written layout may omit the field.
func (t *Transform) UnmarshalText(text []byte) error {
	name := string(text)
	if name == "" {
		*t = Normal
		return nil
	}
	i := slices.Index(transformNames[:], name)
	if i < 0 {
		return fmt.Errorf("unknown transform %q", name)
	}
	*t = Transform(i) //nolint:gosec // i is an index into an 8-element array
	return nil
}

// Rotated reports whether t swaps a monitor's width and height.
func (t Transform) Rotated() bool {
	return t == Rotate90 || t == Rotate270 || t == Flipped90 || t == Flipped270
}
