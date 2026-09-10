# gnome-monitor-pin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go binary that captures a GNOME multi-monitor layout keyed on EDID identity and re-applies it through mutter's D-Bus interface whenever a hardware change flattens it, while leaving deliberate changes alone.

**Architecture:** Four `internal/` packages, one per concern: `layout` is the pure model and rules (capture, derive with mutter's adjacency rules, compare), `displayconfig` is the godbus client and codec for `org.gnome.Mutter.DisplayConfig`, `pin` orchestrates fix-once and the serial-gated debounced watch loop against a narrow `Display` interface it declares, and `cli` is the cobra tree plus the layout-file store. Every package is unit-tested without a bus; one integration spec behind a build tag talks to the real session bus with the verify-only method.

**Tech Stack:** Go 1.27, cobra v1.10.2, viper v1.21.0, `github.com/godbus/dbus/v5` v5.2.2, `log/slog`, Ginkgo v2.32.1, Gomega v1.43.0, counterfeiter v6.12.2 (as a `go tool`).

**Spec:** `docs/superpowers/specs/2026-09-09-gnome-monitor-pin-design.md`

## Global Constraints

- Module `github.com/jhoblitt/gnome-monitor-pin`, binary `gnome-monitor-pin`, environment prefix `GNOME_MONITOR_PIN_`.
- `go 1.27` directive, no `toolchain` line. The go-conventions canon applies to every line: cobra and viper for the CLI, `log/slog` JSON to stderr with static lowercase messages and attribute-only arguments, Ginkgo and Gomega for every test, counterfeiter fakes generated into `<pkg>fakes/` and committed, godoc on every exported symbol.
- `make check` (generate-check, fmt-check, vet, lint, fix-check, tidy-check, test) is green at the end of every task. `generate-check` diffs tracked files, so stage every change to a tracked file (`git add`) before running it. Run `go fix ./...` before the gate: Go 1.27 permits promoted fields in composite literals and its `embedlit` modernizer requires that form, which the code below already uses.
- US spelling everywhere, in code, comments, and spec names: "canceled", "neighbor", "normalizes", "behavior". misspell gates it.
- Exported type names do not repeat the package name (revive's `exported` rule reports the stutter), so mutter's "layout mode" is `layout.Units`.
- Commit subjects are Conventional Commits. Every commit body ends with the trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.
- Work happens on the `init` branch. Never commit to `main`.
- Positions are logical pixels exactly as mutter reports them. Monitors are matched by (vendor, product, serial), never by connector.
- The apply method is always temporary (1); verify (0) runs first; persistent (2) is never used. Every D-Bus call carries a 30 s timeout.
- Mutter's rules that the code mirrors: two logical monitors are adjacent only when they share an edge of positive length (a corner is not adjacency); the whole layout must be one connected component; no two logical monitors overlap; the bounding box starts at (0, 0); exactly one primary; a scale must be one the mode supports; a monitor omitted from the configuration is disabled; the `layout-mode` property is accepted only when `supports-changing-layout-mode` is reported.

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/layout/types.go` | `ID`, `Placement`, `Layout`, `Mode`, `Monitor`, `Logical`, `Units`, `State`, `Cell`, `Target` |
| `internal/layout/transform.go` | `Transform` enum with text marshalling and `Rotated` |
| `internal/layout/capture.go` | `Snapshot`, `Capture`: the current state as placements, switched-off monitors included |
| `internal/layout/modes.go` | Mode resolution by ID or resolution, scale snapping, logical size |
| `internal/layout/geometry.go` | `box`, normalize, band closing, adjacency, connectivity, overlap, compaction, primary |
| `internal/layout/derive.go` | `Derive`: pinned layout + state to `Target` |
| `internal/layout/equal.go` | `Equal`: idempotency comparison |
| `internal/layout/layouttest/fixtures.go` | Six-monitor fixtures shared by every suite |
| `internal/displayconfig/codec.go` | Wire structs matching mutter's signatures; decode and encode |
| `internal/displayconfig/client.go` | `Connect`, `Client` with `CurrentState`, `Verify`, `Apply`, `Subscribe`, `Close` |
| `internal/pin/pin.go` | `Display` interface, `Pinner`, `Fix` with retries |
| `internal/pin/watch.go` | `Watch`: serial-gated debounced loop |
| `internal/pin/pinfakes/` | Generated fake for `Display` |
| `internal/cli/root.go` | Root command, persistent flags, `Run`, `RunWith`, `Display`, `Connect` |
| `internal/cli/layoutfile.go` | `readLayout`, `writeLayout` (atomic) |
| `internal/cli/print.go` | Tabular output helpers |
| `internal/cli/show.go`, `save.go`, `fix.go`, `watch.go` | One subcommand each |
| `internal/cli/clifakes/` | Generated fake for `cli.Display` |
| `contrib/gnome-monitor-pin.service` | systemd user unit |
| `packaging/gnome-monitor-pin.spec` | RPM spec: binary, unit, docs |
| `packaging/build-rpm.sh` | Builds one RPM from a prebuilt binary inside a Fedora container |
| `.github/workflows/release.yml` | Existing goreleaser job plus the `rpm` matrix job |
| `README.md` | Install, usage, daily workflow, troubleshooting |

---

### Task 1: layout model and transforms

**Files:**
- Create: `internal/layout/types.go`
- Create: `internal/layout/transform.go`
- Create: `internal/layout/layout_suite_test.go`
- Test: `internal/layout/transform_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: every type in `types.go` below, `Transform` and its constants, `Transform.String`, `MarshalText`, `UnmarshalText`, `Rotated`, `UnitsLogical`, `UnitsPhysical`.

- [ ] **Step 1: Write the suite bootstrap**

`internal/layout/layout_suite_test.go`:

```go
package layout_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Managed by go-conventions (references/testing.md owns the bootstrap):
// spec order is randomized within the suite and a committed Pending or
// focused spec fails the run under plain go test.
func TestLayout(t *testing.T) {
	RegisterFailHandler(Fail)
	suiteCfg, reporterCfg := GinkgoConfiguration()
	suiteCfg.RandomizeAllSpecs = true
	suiteCfg.FailOnPending = true
	RunSpecs(t, "layout suite", suiteCfg, reporterCfg)
}
```

- [ ] **Step 2: Write the failing transform and JSON specs**

`internal/layout/transform_test.go`:

```go
package layout_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

var _ = Describe("Transform", func() {
	DescribeTable("round-trips through its name",
		func(t layout.Transform, name string) {
			text, err := t.MarshalText()
			Expect(err).NotTo(HaveOccurred())
			Expect(string(text)).To(Equal(name), "transform %d", uint32(t))

			var back layout.Transform
			Expect(back.UnmarshalText([]byte(name))).To(Succeed())
			Expect(back).To(Equal(t), "name %q", name)
		},
		Entry("normal", layout.Normal, "normal"),
		Entry("rotated 90", layout.Rotate90, "90"),
		Entry("flipped and rotated 270", layout.Flipped270, "flipped-270"),
	)

	It("treats an empty name as normal so a hand-written file may omit it", func() {
		t := layout.Rotate180
		Expect(t.UnmarshalText(nil)).To(Succeed())
		Expect(t).To(Equal(layout.Normal))
	})

	It("rejects an unknown name", func() {
		var t layout.Transform
		Expect(t.UnmarshalText([]byte("sideways"))).To(MatchError(ContainSubstring("sideways")))
	})

	DescribeTable("knows which transforms swap width and height",
		func(t layout.Transform, rotated bool) {
			Expect(t.Rotated()).To(Equal(rotated), "transform %s", t)
		},
		Entry("normal keeps them", layout.Normal, false),
		Entry("90 swaps them", layout.Rotate90, true),
		Entry("180 keeps them", layout.Rotate180, false),
		Entry("flipped-270 swaps them", layout.Flipped270, true),
	)
})

var _ = Describe("Placement JSON", func() {
	It("flattens the identity and names the transform", func() {
		p := layout.Placement{
			Vendor:    "DEL",
			Product:   "DELL U2413",
			Serial:    "ABC",
			X:         1920,
			Y:         0,
			Scale:     1,
			Transform: layout.Rotate90,
			Primary:   true,
			Mode:      "1920x1200@59.950",
		}
		data, err := json.Marshal(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(MatchJSON(`{
			"vendor": "DEL", "product": "DELL U2413", "serial": "ABC",
			"x": 1920, "scale": 1, "transform": "90",
			"primary": true, "mode": "1920x1200@59.950"
		}`))
	})

	It("omits every field a disabled entry has no use for when unset", func() {
		data, err := json.Marshal(layout.Placement{})
		Expect(err).NotTo(HaveOccurred())
		for _, field := range []string{"x", "y", "scale", "transform", "primary", "mode", "disabled"} {
			Expect(string(data)).NotTo(ContainSubstring(field), "field %s", field)
		}
	})

	It("records a switched-off monitor as disabled", func() {
		data, err := json.Marshal(layout.Placement{Vendor: "DEL", Product: "P", Serial: "S", Disabled: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(MatchJSON(`{"vendor":"DEL","product":"P","serial":"S","disabled":true}`))
	})

	It("reads a minimal hand-written entry", func() {
		var l layout.Layout
		Expect(json.Unmarshal([]byte(`{"monitors":[{"vendor":"DEL","product":"P","serial":"S","x":0,"y":0}]}`), &l)).To(Succeed())
		Expect(l.Monitors).To(HaveLen(1))
		Expect(l.Monitors[0].Transform).To(Equal(layout.Normal))
		Expect(l.Monitors[0].Serial).To(Equal("S"))
		Expect(l.Monitors[0].Disabled).To(BeFalse())
	})
})
```

- [ ] **Step 3: Run the suite to verify it fails**

Run: `go test ./internal/layout/...`
Expected: build failure, `no non-test Go files in .../internal/layout` (the package does not exist yet).

- [ ] **Step 4: Write the types**

`internal/layout/types.go`:

```go
// Package layout is the pure model of a pinned monitor layout: what the
// user wants, what mutter currently shows, and how to derive one from the
// other under mutter's own placement rules. It does no I/O.
package layout

// ID identifies a physical monitor by the EDID fields mutter reports.
// Unlike the connector name, these survive a DisplayPort MST
// re-enumeration. Mutter substitutes "unknown" for a field the EDID lacks,
// so an ID is not always unique.
type ID struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	Serial  string `json:"serial"`
}

// Placement is where one pinned monitor sits and how it is driven. A
// disabled placement pins the monitor off; its position is meaningless.
type Placement struct {
	ID
	X         int       `json:"x,omitzero"`
	Y         int       `json:"y,omitzero"`
	Scale     float64   `json:"scale,omitzero"`
	Transform Transform `json:"transform,omitzero"`
	Primary   bool      `json:"primary,omitzero"`
	Mode      string    `json:"mode,omitzero"`
	Disabled  bool      `json:"disabled,omitzero"`
}

// Layout is the pinned layout, the contents of the layout file.
type Layout struct {
	Monitors []Placement `json:"monitors"`
}

// Mode is one mode a connected monitor offers.
type Mode struct {
	ID             string
	Width          int
	Height         int
	Refresh        float64
	PreferredScale float64
	// Scales lists the scales mutter accepts for this mode, as the
	// single-precision values mutter reports.
	Scales    []float64
	Preferred bool
	Current   bool
}

// Monitor is a connected monitor as mutter reports it.
type Monitor struct {
	Connector string
	ID        ID
	Modes     []Mode
	// Builtin marks a laptop panel, which mutter refuses to activate while
	// the lid is closed.
	Builtin bool
	// ForLease marks a monitor handed to another compositor.
	ForLease bool
}

// Logical is one logical monitor mutter currently drives: a position on
// the desktop and the connectors shown there.
type Logical struct {
	X          int
	Y          int
	Scale      float64
	Transform  Transform
	Primary    bool
	Connectors []string
}

// Units is how mutter measures logical monitors, what its D-Bus interface
// calls the layout mode, numbered as that interface numbers it.
type Units uint32

// The units mutter defines. The zero value means the state did not report
// them and is treated as logical.
const (
	UnitsLogical  Units = 1
	UnitsPhysical Units = 2
)

// State is what mutter's GetCurrentState reports.
type State struct {
	Serial uint32
	Units  Units
	// CanSetUnits reports mutter's supports-changing-layout-mode: only then
	// may an apply name the units.
	CanSetUnits bool
	Monitors    []Monitor
	Logical     []Logical
}

// Cell is one single-monitor logical monitor of a derived Target, in the
// shape ApplyMonitorsConfig takes.
type Cell struct {
	Connector string
	Mode      string
	X         int
	Y         int
	Scale     float64
	Transform Transform
	Primary   bool
}

// Target is the configuration derived from a Layout for a State, ready to
// apply, plus what Derive had to leave out or improvise.
type Target struct {
	Serial      uint32
	Units       Units
	CanSetUnits bool
	Cells       []Cell
	// Missing lists pinned monitors that are not connected, or that cannot
	// be activated.
	Missing []ID
	// Unknown lists connectors mutter shows that the layout does not pin;
	// Derive places them to the right of the pinned region.
	Unknown []string
	// Warnings describes every improvisation Derive made: duplicate
	// identities matched by order, a pinned mode or scale replaced.
	Warnings []string
}
```

`internal/layout/transform.go`:

```go
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
```

- [ ] **Step 5: Run the suite to verify it passes**

Run: `go test ./internal/layout/...`
Expected: PASS.

- [ ] **Step 6: Run the gate and commit**

Run: `go fix ./... && make check`
Expected: exit 0. gosec G115 fires on the `Transform(i)` conversion, so the `//nolint` stays.

```bash
git add internal/layout
git commit -m "feat(layout): model, transforms, and JSON shape" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: fixtures, Snapshot, and Capture

**Files:**
- Create: `internal/layout/layouttest/fixtures.go`
- Create: `internal/layout/capture.go`
- Test: `internal/layout/capture_test.go`

**Interfaces:**
- Consumes: Task 1 types.
- Produces: `layouttest.Monitors() []layout.Monitor`, `layouttest.GridLayout() layout.Layout`, `layouttest.GridState() layout.State`, `layouttest.LinearState() layout.State`, `layouttest.Without(s layout.State, connectors ...string) layout.State`, `layouttest.Disabled(s layout.State, connectors ...string) layout.State`, `layouttest.WithSerial(s layout.State, serial uint32) layout.State`, `layouttest.ModeSize(id string) (width, height int, ok bool)`; `layout.Entry`, `layout.Snapshot(State) ([]Entry, error)`, `layout.Capture(State) (Layout, error)`, `layout.ErrMirrored`.

- [ ] **Step 1: Write the fixtures**

`internal/layout/layouttest/fixtures.go`. Six identical 1920x1200 monitors in a 2x3 grid, primary top left, with deliberately unsorted connector names so no spec can pass by accident of ordering. The supported scales are the ones mutter reports for a 1920x1200 mode on the real desktop. Two 1920x1080 modes let specs tell "closest refresh" from "first with that resolution".

```go
// Package layouttest provides the six-monitor fixtures the suites share:
// a 2x3 grid as the user pinned it, and the linear row mutter falls back
// to after a hotplug.
package layouttest

import (
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
```

`fmtSscanf` is a one-line wrapper so the import list stays honest:

```go
func fmtSscanf(id string, width, height *int, refresh *float64) (int, error) {
	return fmt.Sscanf(id, "%dx%d@%f", width, height, refresh)
}
```

Add `"fmt"` to the imports. If `unparam` or `unused` objects to the wrapper, inline the `fmt.Sscanf` call in `ModeSize` instead.

- [ ] **Step 2: Write the failing Snapshot and Capture specs**

`internal/layout/capture_test.go`:

```go
package layout_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
	"github.com/jhoblitt/gnome-monitor-pin/internal/layout/layouttest"
)

var _ = Describe("Snapshot", func() {
	It("lists every shown monitor top to bottom then left to right with its connector", func() {
		entries, err := layout.Snapshot(layouttest.GridState())
		Expect(err).NotTo(HaveOccurred())
		var order []string
		for _, e := range entries {
			order = append(order, e.Connector)
		}
		Expect(order).To(Equal([]string{"DP-4", "DP-22", "DP-16", "DP-6", "DP-20", "DP-9"}))
		Expect(entries[0].Primary).To(BeTrue(), "top-left is primary")
		Expect(entries[0].Mode).To(Equal(layouttest.ModeID))
		Expect(entries[5].Serial).To(Equal("SER-F"))
		Expect(entries[5].X).To(Equal(2 * layouttest.Width))
		Expect(entries[5].Y).To(Equal(layouttest.Height))
	})

	It("lists a connected monitor mutter is not showing last, as disabled", func() {
		entries, err := layout.Snapshot(layouttest.Disabled(layouttest.GridState(), "DP-9"))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(6))
		Expect(entries[5].Connector).To(Equal("DP-9"))
		Expect(entries[5].Disabled).To(BeTrue())
		Expect(entries[5].Serial).To(Equal("SER-F"))
		Expect(entries[5].Mode).To(BeEmpty())
	})

	It("leaves out a monitor leased to another compositor", func() {
		s := layouttest.Disabled(layouttest.GridState(), "DP-9")
		s.Monitors[5].ForLease = true
		entries, err := layout.Snapshot(s)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(5))
	})

	It("rejects a mirrored logical monitor", func() {
		s := layouttest.GridState()
		s.Logical[0].Connectors = append(s.Logical[0].Connectors, "DP-22")
		_, err := layout.Snapshot(s)
		Expect(err).To(MatchError(layout.ErrMirrored))
	})

	It("rejects a logical monitor naming a connector that is not connected", func() {
		s := layouttest.GridState()
		s.Logical[0].Connectors = []string{"DP-99"}
		_, err := layout.Snapshot(s)
		Expect(err).To(MatchError(ContainSubstring("DP-99")))
	})
})

var _ = Describe("Capture", func() {
	It("pins the current state exactly", func() {
		l, err := layout.Capture(layouttest.GridState())
		Expect(err).NotTo(HaveOccurred())
		Expect(l).To(Equal(layouttest.GridLayout()))
	})

	It("records the current mode, not the preferred one", func() {
		s := layouttest.GridState()
		s.Monitors[0].Modes[0].Current = false
		s.Monitors[0].Modes[1].Current = true
		l, err := layout.Capture(s)
		Expect(err).NotTo(HaveOccurred())
		Expect(l.Monitors[0].Mode).To(Equal(layouttest.AltModeID))
	})

	It("pins a switched-off monitor as disabled", func() {
		l, err := layout.Capture(layouttest.Disabled(layouttest.GridState(), "DP-9"))
		Expect(err).NotTo(HaveOccurred())
		Expect(l.Monitors).To(HaveLen(6))
		Expect(l.Monitors[5]).To(Equal(layout.Placement{Vendor: "DEL", Product: "DELL U2413", Serial: "SER-F", Disabled: true}))
	})
})
```

The `Monitors[0].Modes[0]` edit works because `layouttest.Monitors` builds a fresh mode slice per monitor.

- [ ] **Step 3: Run the suite to verify it fails**

Run: `go test ./internal/layout/...`
Expected: compile failure, `undefined: layout.Snapshot`.

- [ ] **Step 4: Write Snapshot and Capture**

`internal/layout/capture.go`:

```go
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
	off := make([]Entry, 0, len(s.Monitors)-len(entries))
	for _, m := range s.Monitors {
		if shown[m.Connector] || m.ForLease {
			continue
		}
		off = append(off, Entry{Connector: m.Connector, Placement: Placement{ID: m.ID, Disabled: true}})
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
```

`Placement{ID: m.ID, ...}` names a field with a variable, not a literal of the embedded type, so `embedlit` leaves it alone; `Entry{Connector: c, Placement: p}` likewise.

- [ ] **Step 5: Run the suite to verify it passes**

Run: `go test ./internal/layout/...`
Expected: PASS.

- [ ] **Step 6: Run the gate and commit**

Run: `go fix ./... && make check`
Expected: exit 0.

```bash
git add internal/layout
git commit -m "feat(layout): snapshot and capture the current state" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: Derive

**Files:**
- Create: `internal/layout/modes.go`
- Create: `internal/layout/geometry.go`
- Create: `internal/layout/derive.go`
- Test: `internal/layout/derive_test.go`

**Interfaces:**
- Consumes: Task 1 types, Task 2 fixtures.
- Produces: `layout.Derive(Layout, State) (Target, error)`, `layout.ErrNoPinnedMonitor`, `layout.ErrNotAdjacent`, `layout.ErrOverlap`; unexported `scaleTolerance` (used by Task 4).

- [ ] **Step 1: Write the failing Derive specs**

`internal/layout/derive_test.go`. The helper `mutterAccepts` mirrors mutter's verification (`mtk_rectangle_is_adjacent_to` with its strict inequalities, `is_connected_to_all`, overlap, origin, one primary), sizing each cell from its mode, scale, and transform, so every derived layout is checked against the rules the compositor applies rather than the implementation's own idea of them.

```go
package layout_test

import (
	"errors"
	"fmt"
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
	"github.com/jhoblitt/gnome-monitor-pin/internal/layout/layouttest"
)

// at finds the cell for connector in t.
func at(t layout.Target, connector string) layout.Cell {
	GinkgoHelper()
	for _, c := range t.Cells {
		if c.Connector == connector {
			return c
		}
	}
	Fail("no cell for " + connector)
	return layout.Cell{}
}

func connectorsOf(t layout.Target) []string {
	out := make([]string, 0, len(t.Cells))
	for _, c := range t.Cells {
		out = append(out, c.Connector)
	}
	return out
}

func primaries(t layout.Target) []string {
	var out []string
	for _, c := range t.Cells {
		if c.Primary {
			out = append(out, c.Connector)
		}
	}
	return out
}

type rect struct{ x1, y1, x2, y2 int }

// rectOf sizes a cell the way mutter does in logical units: the mode's
// native size, swapped when rotated, divided by the scale.
func rectOf(c layout.Cell) rect {
	GinkgoHelper()
	w, h, ok := layouttest.ModeSize(c.Mode)
	Expect(ok).To(BeTrue(), "mode %q", c.Mode)
	if c.Transform.Rotated() {
		w, h = h, w
	}
	w = int(math.Round(float64(w) / c.Scale))
	h = int(math.Round(float64(h) / c.Scale))
	return rect{c.X, c.Y, c.X + w, c.Y + h}
}

// adjacentRects is mtk_rectangle_is_adjacent_to: a shared edge of
// positive length; a shared corner is not adjacency.
func adjacentRects(a, b rect) bool {
	if (a.x1 == b.x2 || a.x2 == b.x1) && a.y2 > b.y1 && a.y1 < b.y2 {
		return true
	}
	if (a.y1 == b.y2 || a.y2 == b.y1) && a.x2 > b.x1 && a.x1 < b.x2 {
		return true
	}
	return false
}

func overlapRects(a, b rect) bool {
	return a.x1 < b.x2 && b.x1 < a.x2 && a.y1 < b.y2 && b.y1 < a.y2
}

// mutterAccepts applies meta_verify_monitors_config's placement rules.
func mutterAccepts(cells []layout.Cell) error {
	rects := make([]rect, len(cells))
	minX, minY, primaries := 0, 0, 0
	for i, c := range cells {
		rects[i] = rectOf(c)
		if i == 0 || c.X < minX {
			minX = c.X
		}
		if i == 0 || c.Y < minY {
			minY = c.Y
		}
		if c.Primary {
			primaries++
		}
	}
	if minX != 0 || minY != 0 {
		return fmt.Errorf("logical monitors positions are offset by %d,%d", minX, minY)
	}
	if primaries != 1 {
		return fmt.Errorf("%d primary logical monitors", primaries)
	}
	for i := range rects {
		for j := range rects[:i] {
			if overlapRects(rects[i], rects[j]) {
				return fmt.Errorf("logical monitors %s and %s overlap", cells[i].Connector, cells[j].Connector)
			}
		}
	}
	seen := map[int]bool{0: true}
	stack := []int{0}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for j := range rects {
			if !seen[j] && adjacentRects(rects[i], rects[j]) {
				seen[j] = true
				stack = append(stack, j)
			}
		}
	}
	if len(seen) != len(rects) {
		return errors.New("logical monitors not adjacent")
	}
	return nil
}

// mixedGrid is the user's grid with the top-left monitor at scale 1.25
// (1536x960) and the top row snapped as Settings does.
func mixedGrid() layout.Layout {
	l := layouttest.GridLayout()
	l.Monitors[0].Scale = 1.25
	l.Monitors[1].X = 1536
	l.Monitors[2].X = 3456
	return l
}

var _ = Describe("Derive", func() {
	const w, h = layouttest.Width, layouttest.Height
	all := []string{"DP-4", "DP-22", "DP-16", "DP-6", "DP-20", "DP-9"}

	It("rebuilds the grid from the linear fallback using today's connectors", func() {
		t, err := layout.Derive(layouttest.GridLayout(), layouttest.LinearState())
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Serial).To(Equal(uint32(8)))
		Expect(t.Units).To(Equal(layout.UnitsLogical))
		Expect(t.CanSetUnits).To(BeTrue())
		Expect(t.Cells).To(HaveLen(6))
		Expect(t.Missing).To(BeEmpty())
		Expect(t.Unknown).To(BeEmpty())
		Expect(t.Warnings).To(BeEmpty())
		Expect(at(t, "DP-4")).To(Equal(layout.Cell{Connector: "DP-4", Mode: layouttest.ModeID, X: 0, Y: 0, Scale: 1, Primary: true}))
		Expect(at(t, "DP-9")).To(Equal(layout.Cell{Connector: "DP-9", Mode: layouttest.ModeID, X: 2 * w, Y: h, Scale: 1}))
		Expect(primaries(t)).To(Equal([]string{"DP-4"}))
	})

	It("follows a monitor whose connector was renamed", func() {
		s := layouttest.LinearState()
		s.Monitors[5].Connector = "DP-31"
		s.Logical[5].Connectors = []string{"DP-31"}
		t, err := layout.Derive(layouttest.GridLayout(), s)
		Expect(err).NotTo(HaveOccurred())
		Expect(at(t, "DP-31").X).To(Equal(2 * w))
		Expect(at(t, "DP-31").Y).To(Equal(h))
	})

	DescribeTable("derives a layout mutter accepts for every subset",
		func(l layout.Layout) {
			for mask := range 1 << len(all) {
				if mask == 0 {
					continue
				}
				var off []string
				for i, c := range all {
					if mask&(1<<i) == 0 {
						off = append(off, c)
					}
				}
				t, err := layout.Derive(l, layouttest.Without(layouttest.LinearState(), off...))
				Expect(err).NotTo(HaveOccurred(), "off %v", off)
				Expect(mutterAccepts(t.Cells)).To(Succeed(), "off %v", off)
			}
		},
		Entry("of the uniform grid", layouttest.GridLayout()),
		Entry("of the grid with one monitor at scale 1.25", mixedGrid()),
	)

	DescribeTable("keeps the other five in place when one monitor is off",
		func(connector string) {
			t, err := layout.Derive(layouttest.GridLayout(), layouttest.Without(layouttest.LinearState(), connector))
			Expect(err).NotTo(HaveOccurred())
			Expect(t.Cells).To(HaveLen(5))
			Expect(t.Missing).To(HaveLen(1))
			for _, c := range t.Cells {
				want := at(layout.Target{Cells: gridCells()}, c.Connector)
				Expect(c.X).To(Equal(want.X), "x of %s", c.Connector)
				Expect(c.Y).To(Equal(want.Y), "y of %s", c.Connector)
			}
			Expect(primaries(t)).To(HaveLen(1))
		},
		Entry("top left (the primary)", "DP-4"),
		Entry("top middle", "DP-22"),
		Entry("top right", "DP-16"),
		Entry("bottom left", "DP-6"),
		Entry("bottom middle", "DP-20"),
		Entry("bottom right", "DP-9"),
	)

	Describe("the primary", func() {
		It("is promoted to the top-left-most monitor when the pinned primary is off", func() {
			t, err := layout.Derive(layouttest.GridLayout(), layouttest.Without(layouttest.LinearState(), "DP-4"))
			Expect(err).NotTo(HaveOccurred())
			Expect(primaries(t)).To(Equal([]string{"DP-22"}), "top middle is the first in reading order")
		})

		It("is the first pinned primary that is present, wherever it sits in the file", func() {
			l := layouttest.GridLayout()
			l.Monitors[0].Primary = false
			l.Monitors[2].Primary = true
			l.Monitors[4].Primary = true
			t, err := layout.Derive(l, layouttest.Without(layouttest.LinearState(), "DP-16"))
			Expect(err).NotTo(HaveOccurred())
			Expect(primaries(t)).To(Equal([]string{"DP-20"}), "the top-right primary is off, the bottom-middle one is next in the file")
		})
	})

	It("closes an empty column", func() {
		t, err := layout.Derive(layouttest.GridLayout(), layouttest.Without(layouttest.LinearState(), "DP-22", "DP-20"))
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Cells).To(HaveLen(4))
		Expect(at(t, "DP-16").X).To(Equal(w), "right column moved left")
		Expect(at(t, "DP-9").X).To(Equal(w))
		Expect(at(t, "DP-9").Y).To(Equal(h))
	})

	It("closes an empty row", func() {
		t, err := layout.Derive(layouttest.GridLayout(), layouttest.Without(layouttest.LinearState(), "DP-4", "DP-22", "DP-16"))
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Cells).To(HaveLen(3))
		for _, c := range t.Cells {
			Expect(c.Y).To(BeZero(), "bottom row moved up: %s", c.Connector)
		}
		Expect(primaries(t)).To(Equal([]string{"DP-6"}))
	})

	It("compacts rows then columns when band closing leaves survivors touching only at corners", func() {
		// Top middle and bottom left off: the top left touches the bottom
		// middle only at a corner, which mutter does not count.
		t, err := layout.Derive(layouttest.GridLayout(), layouttest.Without(layouttest.LinearState(), "DP-22", "DP-6"))
		Expect(err).NotTo(HaveOccurred())
		Expect(at(t, "DP-4")).To(HaveField("X", 0))
		Expect(at(t, "DP-16").X).To(Equal(w), "top row closed leftward")
		Expect(at(t, "DP-20")).To(HaveField("X", 0))
		Expect(at(t, "DP-20")).To(HaveField("Y", h))
		Expect(at(t, "DP-9").X).To(Equal(w), "bottom row closed leftward")
		Expect(mutterAccepts(t.Cells)).To(Succeed())
	})

	It("stacks two diagonal survivors", func() {
		t, err := layout.Derive(layouttest.GridLayout(), layouttest.Without(layouttest.LinearState(), "DP-22", "DP-16", "DP-6", "DP-20"))
		Expect(err).NotTo(HaveOccurred())
		Expect(at(t, "DP-4")).To(Equal(layout.Cell{Connector: "DP-4", Mode: layouttest.ModeID, Scale: 1, Primary: true}))
		Expect(at(t, "DP-9")).To(HaveField("X", 0))
		Expect(at(t, "DP-9")).To(HaveField("Y", h))
	})

	It("needs a second compaction pass on the mixed-scale grid with the top middle off", func() {
		t, err := layout.Derive(mixedGrid(), layouttest.Without(layouttest.LinearState(), "DP-22"))
		Expect(err).NotTo(HaveOccurred())
		Expect(mutterAccepts(t.Cells)).To(Succeed())
		Expect(at(t, "DP-4")).To(HaveField("X", 0))
		Expect(at(t, "DP-4")).To(HaveField("Y", 0))
	})

	It("normalizes a layout that starts in negative coordinates", func() {
		l := layouttest.GridLayout()
		for i := range l.Monitors {
			l.Monitors[i].X -= 100
			l.Monitors[i].Y -= 50
		}
		t, err := layout.Derive(l, layouttest.LinearState())
		Expect(err).NotTo(HaveOccurred())
		Expect(at(t, "DP-4")).To(HaveField("X", 0))
		Expect(at(t, "DP-4")).To(HaveField("Y", 0))
		Expect(at(t, "DP-9")).To(HaveField("X", 2*w))
	})

	Describe("monitors the layout does not pin", func() {
		extra := func(s layout.State, connector string) layout.State {
			s.Monitors = append(s.Monitors, layout.Monitor{
				Connector: connector,
				ID:        layout.ID{Vendor: "ACR", Product: "Extra", Serial: "X"},
				Modes: []layout.Mode{
					{ID: "1280x720@60.000", Width: 1280, Height: 720, PreferredScale: 1, Scales: []float64{1}},
					{ID: "2560x1440@60.000", Width: 2560, Height: 1440, PreferredScale: 2, Scales: []float64{1, 2}, Preferred: true},
				},
			})
			s.Logical = append(s.Logical, layout.Logical{X: 6 * w, Y: 0, Scale: 2, Connectors: []string{connector}})
			return s
		}

		// rotated turns the last logical monitor of s, the one extra just
		// appended, a quarter turn.
		rotated := func(s layout.State) layout.State {
			s.Logical[len(s.Logical)-1].Transform = layout.Rotate90
			return s
		}

		It("appends a shown one to the right at its preferred mode and scale", func() {
			t, err := layout.Derive(layouttest.GridLayout(), extra(layouttest.LinearState(), "HDMI-1"))
			Expect(err).NotTo(HaveOccurred())
			Expect(t.Unknown).To(Equal([]string{"HDMI-1"}))
			c := at(t, "HDMI-1")
			Expect(c.X).To(Equal(3*w), "right of the grid")
			Expect(c.Y).To(BeZero())
			Expect(c.Mode).To(Equal("2560x1440@60.000"))
			Expect(c.Scale).To(Equal(2.0))
			Expect(c.Primary).To(BeFalse())
			Expect(mutterAccepts(t.Cells)).To(Succeed())
		})

		It("keeps a shown one's rotation", func() {
			t, err := layout.Derive(layouttest.GridLayout(), rotated(extra(layouttest.LinearState(), "HDMI-1")))
			Expect(err).NotTo(HaveOccurred())
			c := at(t, "HDMI-1")
			Expect(c.Transform).To(Equal(layout.Rotate90))
			Expect(mutterAccepts(t.Cells)).To(Succeed())
		})

		It("aligns it with the topmost cell on the right edge so they share an edge", func() {
			s := extra(layouttest.Without(layouttest.LinearState(), "DP-16"), "HDMI-1")
			t, err := layout.Derive(layouttest.GridLayout(), s)
			Expect(err).NotTo(HaveOccurred())
			c := at(t, "HDMI-1")
			Expect(c.X).To(Equal(3 * w))
			Expect(c.Y).To(Equal(h), "the bottom right cell is the only one on that edge")
			Expect(mutterAccepts(t.Cells)).To(Succeed())
		})

		It("leaves a connected but switched-off one alone", func() {
			l := layouttest.GridLayout()
			l.Monitors = l.Monitors[:5]
			t, err := layout.Derive(l, layouttest.Disabled(layouttest.LinearState(), "DP-9"))
			Expect(err).NotTo(HaveOccurred())
			Expect(connectorsOf(t)).NotTo(ContainElement("DP-9"))
			Expect(t.Unknown).To(BeEmpty())
		})

		It("switches a switched-off one on when the layout pins it", func() {
			t, err := layout.Derive(layouttest.GridLayout(), layouttest.Disabled(layouttest.LinearState(), "DP-9"))
			Expect(err).NotTo(HaveOccurred())
			Expect(at(t, "DP-9").X).To(Equal(2 * w))
			Expect(at(t, "DP-9").Y).To(Equal(h))
		})

		It("keeps a pinned-off monitor off even after mutter's fallback switched it on", func() {
			l := layouttest.GridLayout()
			l.Monitors[5] = layout.Placement{Vendor: "DEL", Product: "DELL U2413", Serial: "SER-F", Disabled: true}
			t, err := layout.Derive(l, layouttest.LinearState())
			Expect(err).NotTo(HaveOccurred())
			Expect(t.Cells).To(HaveLen(5))
			Expect(connectorsOf(t)).NotTo(ContainElement("DP-9"))
			Expect(t.Unknown).To(BeEmpty(), "a pinned-off monitor is not unknown")
			Expect(t.Missing).To(BeEmpty())
		})

		It("treats a built-in panel mutter is not showing as absent even when pinned", func() {
			s := layouttest.Disabled(layouttest.LinearState(), "DP-9")
			s.Monitors[5].Builtin = true
			t, err := layout.Derive(layouttest.GridLayout(), s)
			Expect(err).NotTo(HaveOccurred())
			Expect(connectorsOf(t)).NotTo(ContainElement("DP-9"))
			Expect(t.Missing).To(ConsistOf(layout.ID{Vendor: "DEL", Product: "DELL U2413", Serial: "SER-F"}))
			Expect(t.Warnings).To(ContainElement(ContainSubstring("DP-9")))
		})

		It("ignores a monitor leased to another compositor", func() {
			s := layouttest.LinearState()
			s.Monitors[5].ForLease = true
			t, err := layout.Derive(layouttest.GridLayout(), s)
			Expect(err).NotTo(HaveOccurred())
			Expect(connectorsOf(t)).NotTo(ContainElement("DP-9"))
			Expect(t.Unknown).To(BeEmpty())
			Expect(t.Missing).To(HaveLen(1))
		})
	})

	Describe("monitors that share an identity", func() {
		It("are matched by order with a warning naming both", func() {
			s := layouttest.LinearState()
			s.Monitors[1].ID.Serial = "SER-A"
			l := layouttest.GridLayout()
			l.Monitors[1].Serial = "SER-A"
			t, err := layout.Derive(l, s)
			Expect(err).NotTo(HaveOccurred())
			Expect(at(t, "DP-4")).To(HaveField("X", 0))
			Expect(at(t, "DP-22")).To(HaveField("X", w))
			Expect(t.Missing).To(BeEmpty())
			Expect(t.Warnings).To(ContainElement(SatisfyAll(ContainSubstring("SER-A"), ContainSubstring("DP-22"))))
		})

		It("prefer the one mutter is showing", func() {
			s := layouttest.Disabled(layouttest.LinearState(), "DP-4")
			s.Monitors[1].ID.Serial = "SER-A"
			l := layouttest.GridLayout()
			l.Monitors = l.Monitors[:1]
			t, err := layout.Derive(l, s)
			Expect(err).NotTo(HaveOccurred())
			Expect(at(t, "DP-22")).To(HaveField("X", 0), "the shown duplicate takes the single pinned entry")
			Expect(connectorsOf(t)).NotTo(ContainElement("DP-4"), "the dark duplicate stays off")
		})
	})

	Describe("modes", func() {
		It("uses the pinned mode when the monitor offers it", func() {
			l := layouttest.GridLayout()
			l.Monitors[0].Mode = layouttest.AltModeID
			t, err := layout.Derive(l, layouttest.LinearState())
			Expect(err).NotTo(HaveOccurred())
			Expect(at(t, "DP-4").Mode).To(Equal(layouttest.AltModeID))
		})

		It("falls back to the same resolution at the closest refresh when the ID is gone", func() {
			l := layouttest.GridLayout()
			l.Monitors[0].Mode = "1920x1080@49.000+vrr"
			t, err := layout.Derive(l, layouttest.LinearState())
			Expect(err).NotTo(HaveOccurred())
			Expect(at(t, "DP-4").Mode).To(Equal(layouttest.AltModeID50), "50 Hz is closer than 60 Hz, though listed second")
			Expect(t.Warnings).To(BeEmpty(), "one hertz is not worth a warning")
		})

		It("warns when the closest refresh is far from the pinned one", func() {
			l := layouttest.GridLayout()
			l.Monitors[0].Mode = "1920x1200@30.000"
			t, err := layout.Derive(l, layouttest.LinearState())
			Expect(err).NotTo(HaveOccurred())
			Expect(at(t, "DP-4").Mode).To(Equal(layouttest.ModeID))
			Expect(t.Warnings).To(ContainElement(SatisfyAll(ContainSubstring("30.000"), ContainSubstring(layouttest.ModeID))))
		})

		It("keeps a monitor whose pinned resolution is gone in place at its preferred mode", func() {
			l := layouttest.GridLayout()
			l.Monitors[0].Mode = "1600x1200@60.000"
			t, err := layout.Derive(l, layouttest.LinearState())
			Expect(err).NotTo(HaveOccurred())
			Expect(at(t, "DP-4")).To(Equal(layout.Cell{Connector: "DP-4", Mode: layouttest.ModeID, Scale: 1, Primary: true}))
			Expect(t.Warnings).To(ContainElement(ContainSubstring("1600x1200@60.000")))
		})

		It("fails with ErrOverlap when the fallback mode no longer fits beside its neighbor", func() {
			l := layout.Layout{Monitors: []layout.Placement{
				{Vendor: "DEL", Product: "DELL U2413", Serial: "SER-A", X: 0, Y: 0, Scale: 1, Primary: true, Mode: "1600x1200@60.000"},
				{Vendor: "DEL", Product: "DELL U2413", Serial: "SER-B", X: 1600, Y: 0, Scale: 1, Mode: layouttest.ModeID},
			}}
			_, err := layout.Derive(l, layouttest.LinearState())
			Expect(err).To(MatchError(layout.ErrOverlap))
			Expect(err).To(MatchError(SatisfyAll(ContainSubstring("DP-4"), ContainSubstring("DP-22"))))
		})

		It("fails when a pinned monitor offers no modes", func() {
			s := layouttest.LinearState()
			s.Monitors[0].Modes = nil
			_, err := layout.Derive(layouttest.GridLayout(), s)
			Expect(err).To(MatchError(ContainSubstring("DP-4")))
		})
	})

	Describe("scales", func() {
		It("snaps a hand-written scale to the value mutter reports", func() {
			l := layouttest.GridLayout()
			l.Monitors[0].Scale = 1.333
			t, err := layout.Derive(l, layouttest.LinearState())
			Expect(err).NotTo(HaveOccurred())
			Expect(at(t, "DP-4").Scale).To(Equal(layouttest.Scale43))
			Expect(at(t, "DP-22").X).To(Equal(1440), "the scaled cell is 1440 wide and its neighbor closes up")
		})

		It("uses the mode's preferred scale when the file has none", func() {
			l := layouttest.GridLayout()
			l.Monitors[0].Scale = 0
			t, err := layout.Derive(l, layouttest.LinearState())
			Expect(err).NotTo(HaveOccurred())
			Expect(at(t, "DP-4").Scale).To(Equal(1.0))
		})

		It("replaces a scale the mode does not support and says so", func() {
			l := layouttest.GridLayout()
			l.Monitors[0].Scale = 1.75
			t, err := layout.Derive(l, layouttest.LinearState())
			Expect(err).NotTo(HaveOccurred())
			Expect(at(t, "DP-4").Scale).To(Equal(1.0))
			Expect(t.Warnings).To(ContainElement(ContainSubstring("1.75")))
		})

		It("rounds a scale to single precision when the mode lists no supported scales", func() {
			s := layouttest.LinearState()
			s.Monitors[0].Modes[0].Scales = nil
			l := layouttest.GridLayout()
			l.Monitors[0].Scale = 4.0 / 3.0
			t, err := layout.Derive(l, s)
			Expect(err).NotTo(HaveOccurred())
			Expect(at(t, "DP-4").Scale).To(Equal(layouttest.Scale43))
		})

		It("does not divide by the scale in physical units", func() {
			l := layouttest.GridLayout()
			l.Monitors[0].Scale = 2
			s := layouttest.LinearState()
			s.Units = layout.UnitsPhysical
			t, err := layout.Derive(l, s)
			Expect(err).NotTo(HaveOccurred())
			Expect(t.Units).To(Equal(layout.UnitsPhysical))
			Expect(at(t, "DP-22").X).To(Equal(w), "a scale-2 cell is still Width wide in physical pixels")
		})
	})

	It("sizes a rotated monitor by its swapped dimensions", func() {
		l := layout.Layout{Monitors: []layout.Placement{
			{Vendor: "DEL", Product: "DELL U2413", Serial: "SER-A", X: 0, Y: 0, Scale: 1, Transform: layout.Rotate90, Primary: true},
			{Vendor: "DEL", Product: "DELL U2413", Serial: "SER-B", X: 5000, Y: 0, Scale: 1},
		}}
		t, err := layout.Derive(l, layouttest.LinearState())
		Expect(err).NotTo(HaveOccurred())
		Expect(at(t, "DP-22").X).To(Equal(h), "the rotated monitor is Height wide, and the gap after it closes")
		Expect(mutterAccepts(t.Cells)).To(Succeed())
	})

	It("fails when no pinned monitor is connected", func() {
		l := layout.Layout{Monitors: []layout.Placement{{Vendor: "X", Product: "Y", Serial: "Z"}}}
		_, err := layout.Derive(l, layouttest.LinearState())
		Expect(err).To(MatchError(layout.ErrNoPinnedMonitor))
	})
})

// gridCells is the grid as cells keyed by the LinearState connectors.
func gridCells() []layout.Cell {
	t, err := layout.Derive(layouttest.GridLayout(), layouttest.LinearState())
	Expect(err).NotTo(HaveOccurred())
	return t.Cells
}
```

- [ ] **Step 2: Run the suite to verify it fails**

Run: `go test ./internal/layout/...`
Expected: compile failure, `undefined: layout.Derive`.

- [ ] **Step 3: Write the mode helpers**

`internal/layout/modes.go`:

```go
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
```

- [ ] **Step 4: Write the geometry**

`internal/layout/geometry.go`:

```go
package layout

import (
	"cmp"
	"slices"
	"strings"
)

// box is a Cell with the size it occupies.
type box struct {
	Cell
	w, h int
}

func refs(cells []box) []*box {
	out := make([]*box, len(cells))
	for i := range cells {
		out[i] = &cells[i]
	}
	return out
}

func connectors(cells []box) string {
	names := make([]string, 0, len(cells))
	for _, b := range cells {
		names = append(names, b.Connector)
	}
	return strings.Join(names, ", ")
}

// axis selects the coordinate and extent a sweep works along.
type axis struct {
	pos func(*box) *int
	ext func(*box) int
}

var (
	xAxis = axis{pos: func(b *box) *int { return &b.X }, ext: func(b *box) int { return b.w }}
	yAxis = axis{pos: func(b *box) *int { return &b.Y }, ext: func(b *box) int { return b.h }}
)

// normalize shifts every cell so the bounding box starts at the origin.
func normalize(cells []box) {
	minX, minY := cells[0].X, cells[0].Y
	for _, b := range cells[1:] {
		minX = min(minX, b.X)
		minY = min(minY, b.Y)
	}
	for i := range cells {
		cells[i].X -= minX
		cells[i].Y -= minY
	}
}

// closeGaps shifts cells toward the origin along a until no band of that
// axis is empty: a column or row that lost every monitor closes, and the
// cells beyond it move over by its width. Cells that share a band stay
// where they are relative to each other.
func closeGaps(cells []*box, a axis) {
	order := slices.Clone(cells)
	slices.SortFunc(order, func(p, q *box) int { return cmp.Compare(*a.pos(p), *a.pos(q)) })
	covered, shift := 0, 0
	for _, b := range order {
		start := *a.pos(b)
		if start > covered {
			shift += start - covered
			covered = start
		}
		covered = max(covered, start+a.ext(b))
		*a.pos(b) = start - shift
	}
}

// bands groups cells whose extents along a overlap, in order along a:
// with yAxis the groups are rows, with xAxis they are columns.
func bands(cells []box, a axis) [][]*box {
	order := refs(cells)
	slices.SortFunc(order, func(p, q *box) int { return cmp.Compare(*a.pos(p), *a.pos(q)) })
	var groups [][]*box
	end := 0
	for _, b := range order {
		start := *a.pos(b)
		if len(groups) == 0 || start >= end {
			groups = append(groups, nil)
			end = start
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], b)
		end = max(end, start+a.ext(b))
	}
	return groups
}

// compact closes up each row leftward, then each column upward, and
// reports whether any cell moved. It runs only while band closing has left
// the layout disconnected; on a grid one pass turns any set of survivors
// into a compact block, mixed sizes can need another.
func compact(cells []box) (moved bool) {
	before := slices.Clone(cells)
	for _, row := range bands(cells, yAxis) {
		closeGaps(row, xAxis)
	}
	for _, col := range bands(cells, xAxis) {
		closeGaps(col, yAxis)
	}
	for i := range cells {
		if cells[i].X != before[i].X || cells[i].Y != before[i].Y {
			return true
		}
	}
	return false
}

// adjacent mirrors mtk_rectangle_is_adjacent_to: the cells share an edge
// of positive length. A shared corner does not count.
func adjacent(a, b box) bool {
	ax2, ay2 := a.X+a.w, a.Y+a.h
	bx2, by2 := b.X+b.w, b.Y+b.h
	if (a.X == bx2 || ax2 == b.X) && ay2 > b.Y && a.Y < by2 {
		return true
	}
	if (a.Y == by2 || ay2 == b.Y) && ax2 > b.X && a.X < bx2 {
		return true
	}
	return false
}

// overlap reports whether the cells share any area.
func overlap(a, b box) bool {
	return a.X < b.X+b.w && b.X < a.X+a.w && a.Y < b.Y+b.h && b.Y < a.Y+a.h
}

// firstOverlap returns the first pair of overlapping cells.
func firstOverlap(cells []box) (i, j int, found bool) {
	for i := range cells {
		for j := range cells[:i] {
			if overlap(cells[i], cells[j]) {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

// connected mirrors mutter's is_connected_to_all: every cell is reachable
// from the first through adjacent cells.
func connected(cells []box) bool {
	if len(cells) <= 1 {
		return true
	}
	seen := make([]bool, len(cells))
	seen[0] = true
	stack := []int{0}
	count := 1
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for j := range cells {
			if seen[j] || !adjacent(cells[i], cells[j]) {
				continue
			}
			seen[j] = true
			count++
			stack = append(stack, j)
		}
	}
	return count == len(cells)
}

// ensurePrimary leaves exactly one primary: the first one pinned, or when
// none is present the cell with the smallest y, then the smallest x.
func ensurePrimary(cells []box) {
	seen := false
	for i := range cells {
		if cells[i].Primary {
			cells[i].Primary = !seen
			seen = true
		}
	}
	if seen {
		return
	}
	best := 0
	for i, b := range cells {
		if b.Y < cells[best].Y || (b.Y == cells[best].Y && b.X < cells[best].X) {
			best = i
		}
	}
	cells[best].Primary = true
}

// edge returns the right edge of the region and the y of the topmost cell
// on that edge, so a cell placed at (right, top) shares an edge with it.
func edge(cells []box) (right, top int) {
	for i, b := range cells {
		r := b.X + b.w
		if i == 0 || r > right || (r == right && b.Y < top) {
			right, top = r, b.Y
		}
	}
	return right, top
}
```

- [ ] **Step 5: Write Derive**

`internal/layout/derive.go`:

```go
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
			t.Warnings = append(t.Warnings, fmt.Sprintf("%s: built-in panel is not shown (closed lid?), leaving it off", m.Connector))
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
```

The `box{Connector: ..., w: w, h: h}` literals name the promoted `Cell` fields directly, the form Go 1.27's `embedlit` modernizer requires. The overlap check runs before compaction: an overlap there can only come from a fallback mode larger than the pinned one, and must be reported as such rather than as a failure to connect. It runs again after compaction, which is meant to move cells only into free space, so a placement bug surfaces as a named error instead of a configuration mutter rejects. `for !connected(cells)` terminates: every compaction pass moves cells only toward the origin, and a pass that moves nothing ends the loop with an error.

- [ ] **Step 6: Run the suite to verify it passes**

Run: `go test ./internal/layout/...`
Expected: PASS. If the mixed-scale entry of the every-subset spec fails with "not adjacent", check that `compact` is called in a loop and that `normalize` runs between passes.

- [ ] **Step 7: Run the gate and commit**

Run: `go fix ./... && make check`
Expected: exit 0.

```bash
git add internal/layout
git commit -m "feat(layout): derive a target from a pinned layout" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: Equal

**Files:**
- Create: `internal/layout/equal.go`
- Test: `internal/layout/equal_test.go`

**Interfaces:**
- Consumes: Task 1 types, Task 2 fixtures, Task 3 `Derive` and `scaleTolerance`.
- Produces: `layout.Equal(Target, State) bool`.

- [ ] **Step 1: Write the failing Equal specs**

`internal/layout/equal_test.go`:

```go
package layout_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
	"github.com/jhoblitt/gnome-monitor-pin/internal/layout/layouttest"
)

var _ = Describe("Equal", func() {
	gridTarget := func() layout.Target {
		GinkgoHelper()
		t, err := layout.Derive(layouttest.GridLayout(), layouttest.GridState())
		Expect(err).NotTo(HaveOccurred())
		return t
	}

	It("is true when the state already shows the target", func() {
		Expect(layout.Equal(gridTarget(), layouttest.GridState())).To(BeTrue())
	})

	It("is false for the linear fallback", func() {
		t, err := layout.Derive(layouttest.GridLayout(), layouttest.LinearState())
		Expect(err).NotTo(HaveOccurred())
		Expect(layout.Equal(t, layouttest.LinearState())).To(BeFalse())
	})

	It("is false when the state shows a monitor the target does not", func() {
		t, err := layout.Derive(layouttest.GridLayout(), layouttest.Without(layouttest.GridState(), "DP-9"))
		Expect(err).NotTo(HaveOccurred())
		Expect(layout.Equal(t, layouttest.GridState())).To(BeFalse())
	})

	It("is false when only the mode differs", func() {
		l := layouttest.GridLayout()
		l.Monitors[0].Mode = layouttest.AltModeID
		t, err := layout.Derive(l, layouttest.GridState())
		Expect(err).NotTo(HaveOccurred())
		Expect(layout.Equal(t, layouttest.GridState())).To(BeFalse())
	})

	It("is false when only the primary differs", func() {
		l := layouttest.GridLayout()
		l.Monitors[0].Primary = false
		l.Monitors[1].Primary = true
		t, err := layout.Derive(l, layouttest.GridState())
		Expect(err).NotTo(HaveOccurred())
		Expect(layout.Equal(t, layouttest.GridState())).To(BeFalse())
	})

	It("is false when only the transform differs", func() {
		s := layouttest.GridState()
		s.Logical[0].Transform = layout.Rotate180
		Expect(layout.Equal(gridTarget(), s)).To(BeFalse())
	})

	It("is false when the scale differs by more than the tolerance", func() {
		s := layouttest.GridState()
		s.Logical[0].Scale = 1.25
		Expect(layout.Equal(gridTarget(), s)).To(BeFalse())
	})

	It("is false when the state mirrors two monitors at the target's position", func() {
		s := layouttest.Disabled(layouttest.GridState(), "DP-22")
		s.Logical[0].Connectors = []string{"DP-4", "DP-22"}
		Expect(layout.Equal(gridTarget(), s)).To(BeFalse())
	})

	It("tolerates a scale inside the tolerance but not one outside it", func() {
		s := layouttest.GridState()
		s.Logical[0].Scale = 1.005
		Expect(layout.Equal(gridTarget(), s)).To(BeTrue(), "half the tolerance apart")
		s.Logical[0].Scale = 1.02
		Expect(layout.Equal(gridTarget(), s)).To(BeFalse(), "twice the tolerance apart")
	})
})
```

- [ ] **Step 2: Run the suite to verify it fails**

Run: `go test ./internal/layout/...`
Expected: compile failure, `undefined: layout.Equal`.

- [ ] **Step 3: Write Equal**

`internal/layout/equal.go`:

```go
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
```

- [ ] **Step 4: Run the suite to verify it passes**

Run: `go test ./internal/layout/...`
Expected: PASS.

- [ ] **Step 5: Run the gate and commit**

Run: `go fix ./... && make check`
Expected: exit 0.

```bash
git add internal/layout
git commit -m "feat(layout): compare a target with the current state" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: displayconfig codec

**Files:**
- Modify: `go.mod`, `go.sum` (add godbus)
- Create: `internal/displayconfig/codec.go`
- Create: `internal/displayconfig/displayconfig_suite_test.go`
- Test: `internal/displayconfig/codec_test.go`

**Interfaces:**
- Consumes: `layout.State`, `layout.Target`, `layout.Transform`, `layout.Units`.
- Produces: unexported wire types `monitorSpec`, `mode`, `monitor`, `logical`, `applyMonitor`, `applyLogical`; `decodeState(serial uint32, monitors []monitor, logicals []logical, props map[string]dbus.Variant) layout.State`; `encodeTarget(t layout.Target) []applyLogical`; `encodeProperties(t layout.Target) map[string]dbus.Variant`. Task 6 uses these from inside the package.

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/godbus/dbus/v5@v5.2.2`
Expected: `go.mod` gains the require, marked indirect until code imports it. Do not tidy before Step 4.

- [ ] **Step 2: Write the suite bootstrap and failing codec specs**

`internal/displayconfig/displayconfig_suite_test.go`:

```go
package displayconfig_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Managed by go-conventions (references/testing.md owns the bootstrap):
// spec order is randomized within the suite and a committed Pending or
// focused spec fails the run under plain go test.
func TestDisplayconfig(t *testing.T) {
	RegisterFailHandler(Fail)
	suiteCfg, reporterCfg := GinkgoConfiguration()
	suiteCfg.RandomizeAllSpecs = true
	suiteCfg.FailOnPending = true
	RunSpecs(t, "displayconfig suite", suiteCfg, reporterCfg)
}
```

The codec is unexported, so its specs are an internal test file in `package displayconfig`. Ginkgo registers specs from both packages into the one suite above.

`internal/displayconfig/codec_test.go`. The fixture has the shape godbus's decoder hands `Store`: a D-Bus struct is `[]any`, and an array of structs is `[][]any`, never `[]any` of `[]any`.

```go
package displayconfig

import (
	"github.com/godbus/dbus/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

var _ = Describe("decodeState", func() {
	var wire []any

	BeforeEach(func() {
		spec := []any{"DP-4", "DEL", "DELL U2413", "SER-A"}
		wire = []any{
			uint32(42),
			[][]any{
				{
					spec,
					[][]any{
						{"1920x1200@59.950", int32(1920), int32(1200), 59.95, 1.0, []float64{1, 1.25}, map[string]dbus.Variant{
							"is-current":   dbus.MakeVariant(true),
							"is-preferred": dbus.MakeVariant(true),
						}},
						{"1920x1080@60.000", int32(1920), int32(1080), 60.0, 1.0, []float64{1}, map[string]dbus.Variant{}},
					},
					map[string]dbus.Variant{"is-builtin": dbus.MakeVariant(true), "is-for-lease": dbus.MakeVariant(false)},
				},
			},
			[][]any{
				{int32(1920), int32(0), 1.0, uint32(1), true, [][]any{spec}, map[string]dbus.Variant{}},
			},
			map[string]dbus.Variant{
				"layout-mode":                   dbus.MakeVariant(uint32(1)),
				"supports-changing-layout-mode": dbus.MakeVariant(true),
			},
		}
	})

	It("stores mutter's reply into the wire structs and decodes it", func() {
		var (
			serial   uint32
			monitors []monitor
			logicals []logical
			props    map[string]dbus.Variant
		)
		Expect(dbus.Store(wire, &serial, &monitors, &logicals, &props)).To(Succeed())

		s := decodeState(serial, monitors, logicals, props)
		Expect(s.Serial).To(Equal(uint32(42)))
		Expect(s.Units).To(Equal(layout.UnitsLogical))
		Expect(s.CanSetUnits).To(BeTrue())
		Expect(s.Monitors).To(HaveLen(1))
		Expect(s.Monitors[0].Connector).To(Equal("DP-4"))
		Expect(s.Monitors[0].ID).To(Equal(layout.ID{Vendor: "DEL", Product: "DELL U2413", Serial: "SER-A"}))
		Expect(s.Monitors[0].Builtin).To(BeTrue())
		Expect(s.Monitors[0].ForLease).To(BeFalse())
		Expect(s.Monitors[0].Modes).To(Equal([]layout.Mode{
			{ID: "1920x1200@59.950", Width: 1920, Height: 1200, Refresh: 59.95, PreferredScale: 1, Scales: []float64{1, 1.25}, Preferred: true, Current: true},
			{ID: "1920x1080@60.000", Width: 1920, Height: 1080, Refresh: 60, PreferredScale: 1, Scales: []float64{1}},
		}))
		Expect(s.Logical).To(Equal([]layout.Logical{
			{X: 1920, Y: 0, Scale: 1, Transform: layout.Rotate90, Primary: true, Connectors: []string{"DP-4"}},
		}))
	})

	It("treats missing properties as zero values", func() {
		s := decodeState(1, nil, nil, map[string]dbus.Variant{})
		Expect(s.Units).To(BeZero())
		Expect(s.CanSetUnits).To(BeFalse())
	})
})

var _ = Describe("encodeTarget", func() {
	It("produces the signature ApplyMonitorsConfig declares", func() {
		t := layout.Target{Serial: 1, Cells: []layout.Cell{
			{Connector: "DP-4", Mode: "1920x1200@59.950", X: 0, Y: 0, Scale: 1, Primary: true},
			{Connector: "DP-22", Mode: "1920x1200@59.950", X: 1920, Y: 0, Scale: 1, Transform: layout.Rotate180},
		}}
		encoded := encodeTarget(t)
		Expect(dbus.SignatureOf(encoded)).To(Equal(dbus.ParseSignatureMust("a(iiduba(ssa{sv}))")))
		Expect(encoded).To(HaveLen(2))
		Expect(encoded[1].X).To(Equal(int32(1920)))
		Expect(encoded[1].Transform).To(Equal(uint32(2)))
		Expect(encoded[1].Monitors).To(Equal([]applyMonitor{{Connector: "DP-22", Mode: "1920x1200@59.950", Props: map[string]dbus.Variant{}}}))
		Expect(encoded[0].Primary).To(BeTrue())
	})
})

var _ = Describe("encodeProperties", func() {
	It("echoes the layout mode when mutter allows setting it", func() {
		props := encodeProperties(layout.Target{Units: layout.UnitsPhysical, CanSetUnits: true})
		Expect(props).To(HaveKeyWithValue("layout-mode", dbus.MakeVariant(uint32(2))))
		Expect(dbus.SignatureOf(props)).To(Equal(dbus.ParseSignatureMust("a{sv}")))
	})

	It("sends no layout mode when mutter does not allow setting it", func() {
		Expect(encodeProperties(layout.Target{Units: layout.UnitsPhysical})).To(BeEmpty())
	})

	It("sends no layout mode when the state reported none", func() {
		Expect(encodeProperties(layout.Target{CanSetUnits: true})).To(BeEmpty())
	})
})
```

- [ ] **Step 3: Run the suite to verify it fails**

Run: `go test ./internal/displayconfig/...`
Expected: compile failure, `undefined: monitor`.

- [ ] **Step 4: Write the codec**

`internal/displayconfig/codec.go`:

```go
// Package displayconfig talks to mutter's org.gnome.Mutter.DisplayConfig
// interface on the session bus: it reads the current monitor state,
// applies a derived target, and reports display changes.
package displayconfig

import (
	"github.com/godbus/dbus/v5"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

// The wire structs below mirror the D-Bus signatures mutter publishes.
// godbus stores a struct by field order, so the order here is the
// signature's and must not change.

// monitorSpec is (ssss): connector, vendor, product, serial.
type monitorSpec struct {
	Connector string
	Vendor    string
	Product   string
	Serial    string
}

// mode is (siiddada{sv}).
type mode struct {
	ID             string
	Width          int32
	Height         int32
	Refresh        float64
	PreferredScale float64
	Scales         []float64
	Props          map[string]dbus.Variant
}

// monitor is ((ssss)a(siiddada{sv})a{sv}).
type monitor struct {
	Spec  monitorSpec
	Modes []mode
	Props map[string]dbus.Variant
}

// logical is (iiduba(ssss)a{sv}) as GetCurrentState reports it.
type logical struct {
	X         int32
	Y         int32
	Scale     float64
	Transform uint32
	Primary   bool
	Monitors  []monitorSpec
	Props     map[string]dbus.Variant
}

// applyMonitor is (ssa{sv}): connector, mode id, properties.
type applyMonitor struct {
	Connector string
	Mode      string
	Props     map[string]dbus.Variant
}

// applyLogical is (iiduba(ssa{sv})) as ApplyMonitorsConfig takes it.
type applyLogical struct {
	X         int32
	Y         int32
	Scale     float64
	Transform uint32
	Primary   bool
	Monitors  []applyMonitor
}

func decodeState(serial uint32, monitors []monitor, logicals []logical, props map[string]dbus.Variant) layout.State {
	s := layout.State{
		Serial:      serial,
		Units:       layout.Units(uintProp(props, "layout-mode")),
		CanSetUnits: boolProp(props, "supports-changing-layout-mode"),
	}
	for _, m := range monitors {
		out := layout.Monitor{
			Connector: m.Spec.Connector,
			ID:        layout.ID{Vendor: m.Spec.Vendor, Product: m.Spec.Product, Serial: m.Spec.Serial},
			Builtin:   boolProp(m.Props, "is-builtin"),
			ForLease:  boolProp(m.Props, "is-for-lease"),
		}
		for _, md := range m.Modes {
			out.Modes = append(out.Modes, layout.Mode{
				ID:             md.ID,
				Width:          int(md.Width),
				Height:         int(md.Height),
				Refresh:        md.Refresh,
				PreferredScale: md.PreferredScale,
				Scales:         md.Scales,
				Preferred:      boolProp(md.Props, "is-preferred"),
				Current:        boolProp(md.Props, "is-current"),
			})
		}
		s.Monitors = append(s.Monitors, out)
	}
	for _, l := range logicals {
		out := layout.Logical{
			X:         int(l.X),
			Y:         int(l.Y),
			Scale:     l.Scale,
			Transform: layout.Transform(l.Transform),
			Primary:   l.Primary,
		}
		for _, spec := range l.Monitors {
			out.Connectors = append(out.Connectors, spec.Connector)
		}
		s.Logical = append(s.Logical, out)
	}
	return s
}

func boolProp(props map[string]dbus.Variant, key string) bool {
	v, ok := props[key]
	if !ok {
		return false
	}
	b, ok := v.Value().(bool)
	return ok && b
}

func uintProp(props map[string]dbus.Variant, key string) uint32 {
	v, ok := props[key]
	if !ok {
		return 0
	}
	u, ok := v.Value().(uint32)
	if !ok {
		return 0
	}
	return u
}

func encodeTarget(t layout.Target) []applyLogical {
	out := make([]applyLogical, 0, len(t.Cells))
	for _, c := range t.Cells {
		out = append(out, applyLogical{
			X:         int32(c.X), //nolint:gosec // positions arrive from mutter as int32 and go back unchanged
			Y:         int32(c.Y), //nolint:gosec // positions arrive from mutter as int32 and go back unchanged
			Scale:     c.Scale,
			Transform: uint32(c.Transform),
			Primary:   c.Primary,
			Monitors: []applyMonitor{{
				Connector: c.Connector,
				Mode:      c.Mode,
				Props:     map[string]dbus.Variant{},
			}},
		})
	}
	return out
}

// encodeProperties echoes the layout mode the state reported, as gdctl
// does, but only when mutter said it may be set: without that capability
// mutter rejects the property outright, and omitting it selects the only
// mode such a mutter has.
func encodeProperties(t layout.Target) map[string]dbus.Variant {
	props := map[string]dbus.Variant{}
	if t.CanSetUnits && t.Units != 0 {
		props["layout-mode"] = dbus.MakeVariant(uint32(t.Units))
	}
	return props
}
```

- [ ] **Step 5: Run the suite to verify it passes**

Run: `go test ./internal/displayconfig/...`
Expected: PASS. If `dbus.Store` reports a type mismatch, a wire struct's field order or type disagrees with the signature in its comment, provided the fixture keeps the `[][]any` shape for every array of structs.

- [ ] **Step 6: Tidy, stage, gate, commit**

Run: `go mod tidy && git add go.mod go.sum internal/displayconfig && go fix ./... && make check`
Expected: exit 0; `go.mod` keeps godbus as a direct requirement because `codec.go` imports it.

```bash
git add go.mod go.sum internal/displayconfig
git commit -m "feat(displayconfig): codec for GetCurrentState and ApplyMonitorsConfig" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: displayconfig client

**Files:**
- Create: `internal/displayconfig/client.go`
- Test: `internal/displayconfig/client_integration_test.go` (behind `//go:build integration`)

**Interfaces:**
- Consumes: Task 5 codec.
- Produces: `displayconfig.Connect(ctx) (*Client, error)`; `(*Client).CurrentState(ctx) (layout.State, error)`; `(*Client).Verify(ctx, layout.Target) error`; `(*Client).Apply(ctx, layout.Target) error`; `(*Client).Subscribe(ctx) (<-chan struct{}, error)`; `(*Client).Close() error`.

- [ ] **Step 1: Write the integration spec**

`internal/displayconfig/client_integration_test.go`:

```go
//go:build integration

package displayconfig_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jhoblitt/gnome-monitor-pin/internal/displayconfig"
	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

// Needs a GNOME session on the session bus. Runs only with
// `go test -tags integration ./internal/displayconfig/...` and never
// changes the desktop: the only write is a verify-only apply of the
// layout already in effect.
var _ = Describe("Client", Label("integration"), func() {
	It("reads the current state and verifies it back unchanged", func(ctx SpecContext) {
		c, err := displayconfig.Connect(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(c.Close)

		s, err := c.CurrentState(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Monitors).NotTo(BeEmpty())
		Expect(s.Logical).NotTo(BeEmpty())
		Expect(s.Units).NotTo(BeZero())

		l, err := layout.Capture(s)
		Expect(err).NotTo(HaveOccurred())
		t, err := layout.Derive(l, s)
		Expect(err).NotTo(HaveOccurred())
		Expect(layout.Equal(t, s)).To(BeTrue(), "capture then derive must reproduce the state")
		Expect(c.Verify(ctx, t)).To(Succeed())
	})
})
```

- [ ] **Step 2: Run the integration spec to verify it fails**

Run: `go test -tags integration ./internal/displayconfig/...`
Expected: compile failure, `undefined: displayconfig.Connect`.

- [ ] **Step 3: Write the client**

`internal/displayconfig/client.go`:

```go
package displayconfig

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

const (
	busName                    = "org.gnome.Mutter.DisplayConfig"
	objectPath dbus.ObjectPath = "/org/gnome/Mutter/DisplayConfig"
	iface                      = "org.gnome.Mutter.DisplayConfig"

	monitorsChanged  = iface + ".MonitorsChanged"
	nameOwnerChanged = "org.freedesktop.DBus.NameOwnerChanged"

	// callTimeout bounds every bus round trip: a compositor wedged
	// mid-modeset must surface as an error, not stall the watch loop.
	callTimeout = 30 * time.Second
)

// ApplyMonitorsConfig's method argument.
const (
	methodVerify    uint32 = 0
	methodTemporary uint32 = 1
)

// Client is a connection to mutter's display configuration.
type Client struct {
	conn *dbus.Conn
	obj  dbus.BusObject
}

// Connect opens the session bus.
func Connect(ctx context.Context) (*Client, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connecting to the session bus: %w", err)
	}
	if ctx.Err() != nil {
		return nil, errors.Join(ctx.Err(), conn.Close())
	}
	return &Client{conn: conn, obj: conn.Object(busName, objectPath)}, nil
}

// Close closes the bus connection; a channel from Subscribe closes with it.
func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) call(ctx context.Context, method string, args ...any) *dbus.Call {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return c.obj.CallWithContext(ctx, iface+"."+method, dbus.FlagNoAutoStart, args...)
}

// CurrentState reads what mutter shows now.
func (c *Client) CurrentState(ctx context.Context) (layout.State, error) {
	var (
		serial   uint32
		monitors []monitor
		logicals []logical
		props    map[string]dbus.Variant
	)
	if err := c.call(ctx, "GetCurrentState").Store(&serial, &monitors, &logicals, &props); err != nil {
		return layout.State{}, fmt.Errorf("reading state from %s: %w", busName, err)
	}
	return decodeState(serial, monitors, logicals, props), nil
}

// Verify asks mutter whether t is applicable without applying it.
func (c *Client) Verify(ctx context.Context, t layout.Target) error {
	if err := c.apply(ctx, t, methodVerify); err != nil {
		return fmt.Errorf("verifying with %s: %w", busName, err)
	}
	return nil
}

// Apply makes t the current configuration without persisting it to
// mutter's monitors.xml.
func (c *Client) Apply(ctx context.Context, t layout.Target) error {
	if err := c.apply(ctx, t, methodTemporary); err != nil {
		return fmt.Errorf("applying with %s: %w", busName, err)
	}
	return nil
}

func (c *Client) apply(ctx context.Context, t layout.Target, method uint32) error {
	return c.call(ctx, "ApplyMonitorsConfig", t.Serial, method, encodeTarget(t), encodeProperties(t)).Err
}

// Subscribe delivers a value after each MonitorsChanged signal from mutter
// and after each change of owner of mutter's bus name, coalescing a burst
// into one, and closes the channel when ctx ends or the bus connection
// closes.
func (c *Client) Subscribe(ctx context.Context) (<-chan struct{}, error) {
	signals := make(chan *dbus.Signal, 16)
	c.conn.Signal(signals)
	matches := [][]dbus.MatchOption{
		{
			dbus.WithMatchSender(busName),
			dbus.WithMatchObjectPath(objectPath),
			dbus.WithMatchInterface(iface),
			dbus.WithMatchMember("MonitorsChanged"),
		},
		{
			dbus.WithMatchSender("org.freedesktop.DBus"),
			dbus.WithMatchInterface("org.freedesktop.DBus"),
			dbus.WithMatchMember("NameOwnerChanged"),
			dbus.WithMatchArg(0, busName),
		},
	}
	for _, m := range matches {
		if err := c.addMatch(ctx, m); err != nil {
			c.conn.RemoveSignal(signals)
			return nil, fmt.Errorf("subscribing to display signals: %w", err)
		}
	}
	events := make(chan struct{}, 1)
	go func() {
		defer close(events)
		defer c.conn.RemoveSignal(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				if !relevant(sig) {
					continue
				}
				select {
				case events <- struct{}{}:
				default:
				}
			}
		}
	}()
	return events, nil
}

func (c *Client) addMatch(ctx context.Context, options []dbus.MatchOption) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return c.conn.AddMatchSignalContext(ctx, options...)
}

// relevant accepts mutter's MonitorsChanged, and a NameOwnerChanged that
// announces a new owner of mutter's name; a name going away is not an
// event, the next owner's arrival is.
func relevant(sig *dbus.Signal) bool {
	switch sig.Name {
	case monitorsChanged:
		return true
	case nameOwnerChanged:
		if len(sig.Body) < 3 {
			return false
		}
		owner, ok := sig.Body[2].(string)
		return ok && owner != ""
	default:
		return false
	}
}
```

The signal channel is registered before the match rules are added, so no signal can arrive between the rule taking effect and the channel existing. The connection is not tied to a context: the CLI closes it explicitly, and every call carries its own timeout.

- [ ] **Step 4: Run the integration spec to verify it passes**

Run, from a shell inside the GNOME session (the Bash sandbox cannot reach the session bus, so run this unsandboxed): `go test -tags integration -race ./internal/displayconfig/...`
Expected: PASS. If it fails on `Equal`, `show` (Task 9) or `gdctl show` will reveal a monitor mutter lists but does not show, which `Derive` must leave alone.

- [ ] **Step 5: Run the gate and commit**

Run: `go fix ./... && make check`
Expected: exit 0; plain `go test` skips the integration file, and golangci-lint analyzes it through `run.build-tags`.

```bash
git add internal/displayconfig
git commit -m "feat(displayconfig): session bus client" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 7: pin.Fix

**Files:**
- Create: `internal/pin/pin.go`
- Create: `internal/pin/pinfakes/fake_display.go` (generated)
- Create: `internal/pin/pin_suite_test.go`
- Test: `internal/pin/pin_test.go`

**Interfaces:**
- Consumes: `layout.Derive`, `layout.Equal`, `layout.State`, `layout.Target`, `layouttest` fixtures.
- Produces: `pin.Display` interface (`CurrentState`, `Verify`, `Apply`, `Subscribe`, same signatures as Task 6's `Client`); `pin.Pinner{Display, Load, Retries, RetryDelay}`; `(*Pinner).Fix(ctx, dryRun bool) (Result, error)`; `pin.Result{State, Target, Changed, Applied}`; `pin.ErrRejected`; `pinfakes.FakeDisplay`.

- [ ] **Step 1: Write the interface and generate the fake**

`internal/pin/pin.go`, first the interface only, so the fake can be generated before the specs are written:

```go
// Package pin holds a desktop to a pinned layout: it derives the target
// for mutter's current state and applies it when the two differ.
package pin

import (
	"context"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

//go:generate go tool counterfeiter -generate

// Display is what pinning needs from mutter's display configuration.
//
//counterfeiter:generate . Display
type Display interface {
	CurrentState(ctx context.Context) (layout.State, error)
	Verify(ctx context.Context, t layout.Target) error
	Apply(ctx context.Context, t layout.Target) error
	Subscribe(ctx context.Context) (<-chan struct{}, error)
}
```

Run: `go generate ./internal/pin/...`
Expected: `internal/pin/pinfakes/fake_display.go` appears.

- [ ] **Step 2: Write the suite bootstrap and failing Fix specs**

`internal/pin/pin_suite_test.go`:

```go
package pin_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Managed by go-conventions (references/testing.md owns the bootstrap):
// spec order is randomized within the suite and a committed Pending or
// focused spec fails the run under plain go test.
func TestPin(t *testing.T) {
	RegisterFailHandler(Fail)
	suiteCfg, reporterCfg := GinkgoConfiguration()
	suiteCfg.RandomizeAllSpecs = true
	suiteCfg.FailOnPending = true
	RunSpecs(t, "pin suite", suiteCfg, reporterCfg)
}
```

`internal/pin/pin_test.go`:

```go
package pin_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
	"github.com/jhoblitt/gnome-monitor-pin/internal/layout/layouttest"
	"github.com/jhoblitt/gnome-monitor-pin/internal/pin"
	"github.com/jhoblitt/gnome-monitor-pin/internal/pin/pinfakes"
)

var _ = Describe("Pinner.Fix", func() {
	var (
		display *pinfakes.FakeDisplay
		loads   int
		pinner  *pin.Pinner
	)

	BeforeEach(func() {
		display = &pinfakes.FakeDisplay{}
		loads = 0
		pinner = &pin.Pinner{
			Display: display,
			Load: func() (layout.Layout, error) {
				loads++
				return layouttest.GridLayout(), nil
			},
			Retries:    3,
			RetryDelay: time.Millisecond,
		}
	})

	It("verifies then applies when the state differs", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.LinearState(), nil)

		res, err := pinner.Fix(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Changed).To(BeTrue())
		Expect(res.Applied).To(BeTrue())
		Expect(res.State.Serial).To(Equal(uint32(8)))
		Expect(display.VerifyCallCount()).To(Equal(1))
		Expect(display.ApplyCallCount()).To(Equal(1))
		_, applied := display.ApplyArgsForCall(0)
		Expect(applied.Serial).To(Equal(uint32(8)), "the serial of the state the target was derived from")
		Expect(applied.Cells).To(HaveLen(6))
	})

	It("loads the layout on every call", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.GridState(), nil)
		_, err := pinner.Fix(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		_, err = pinner.Fix(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(loads).To(Equal(2))
	})

	It("reports a layout that cannot be loaded without touching the display", func(ctx SpecContext) {
		pinner.Load = func() (layout.Layout, error) { return layout.Layout{}, errors.New("no such file") }
		_, err := pinner.Fix(ctx, false)
		Expect(err).To(MatchError(ContainSubstring("no such file")))
		Expect(display.CurrentStateCallCount()).To(BeZero())
	})

	It("does nothing when the layout is already pinned", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.GridState(), nil)

		res, err := pinner.Fix(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Changed).To(BeFalse())
		Expect(res.Applied).To(BeFalse())
		Expect(display.VerifyCallCount()).To(BeZero())
		Expect(display.ApplyCallCount()).To(BeZero())
	})

	It("verifies but does not apply on a dry run", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.LinearState(), nil)

		res, err := pinner.Fix(ctx, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Changed).To(BeTrue())
		Expect(res.Applied).To(BeFalse())
		Expect(display.VerifyCallCount()).To(Equal(1))
		Expect(display.ApplyCallCount()).To(BeZero())
	})

	It("reports a rejection without applying or retrying", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.LinearState(), nil)
		display.VerifyReturns(errors.New("Logical monitors not adjacent"))

		res, err := pinner.Fix(ctx, false)
		Expect(err).To(MatchError(pin.ErrRejected))
		Expect(err).To(MatchError(ContainSubstring("not adjacent")))
		Expect(res.State.Serial).To(Equal(uint32(8)), "the rejected state is still reported")
		Expect(display.ApplyCallCount()).To(BeZero())
		Expect(display.VerifyCallCount()).To(Equal(1))
		Expect(display.CurrentStateCallCount()).To(Equal(2), "one read for the target, one to rule out a stale serial")
	})

	It("retries when the state changed underneath the verify", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.WithSerial(layouttest.LinearState(), 9), nil)
		display.CurrentStateReturnsOnCall(0, layouttest.LinearState(), nil)
		display.VerifyReturnsOnCall(0, errors.New("The requested configuration is based on stale information"))

		res, err := pinner.Fix(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Applied).To(BeTrue())
		_, applied := display.ApplyArgsForCall(0)
		Expect(applied.Serial).To(Equal(uint32(9)))
		Expect(display.VerifyCallCount()).To(Equal(2))
	})

	It("retries when the re-read after a failed verify also fails", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.LinearState(), nil)
		display.CurrentStateReturnsOnCall(1, layout.State{}, errors.New("bus went away"))
		display.VerifyReturnsOnCall(0, errors.New("Logical monitors not adjacent"))

		res, err := pinner.Fix(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Applied).To(BeTrue())
		Expect(display.VerifyCallCount()).To(Equal(2))
	})

	It("retries a verify that timed out", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.LinearState(), nil)
		display.VerifyReturnsOnCall(0, context.DeadlineExceeded)

		res, err := pinner.Fix(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Applied).To(BeTrue())
		Expect(display.VerifyCallCount()).To(Equal(2))
	})

	It("does not retry a layout that pins no connected monitor", func(ctx SpecContext) {
		pinner.Load = func() (layout.Layout, error) {
			return layout.Layout{Monitors: []layout.Placement{{Vendor: "X", Product: "Y", Serial: "Z"}}}, nil
		}
		display.CurrentStateReturns(layouttest.LinearState(), nil)

		res, err := pinner.Fix(ctx, false)
		Expect(err).To(MatchError(layout.ErrNoPinnedMonitor))
		Expect(res.State.Serial).To(Equal(uint32(8)), "the state is reported even when derivation fails")
		Expect(display.CurrentStateCallCount()).To(Equal(1))
	})

	It("retries from a fresh state after a failed read", func(ctx SpecContext) {
		display.CurrentStateReturnsOnCall(0, layout.State{}, errors.New("bus went away"))
		display.CurrentStateReturnsOnCall(1, layouttest.LinearState(), nil)

		res, err := pinner.Fix(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Applied).To(BeTrue())
		Expect(display.CurrentStateCallCount()).To(Equal(2))
	})

	It("retries from a fresh state after a failed apply", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.LinearState(), nil)
		display.ApplyReturnsOnCall(0, errors.New("serial mismatch"))

		res, err := pinner.Fix(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Applied).To(BeTrue())
		Expect(display.CurrentStateCallCount()).To(Equal(2))
		Expect(display.ApplyCallCount()).To(Equal(2))
	})

	It("gives up after the retry budget", func(ctx SpecContext) {
		display.CurrentStateReturns(layout.State{}, errors.New("bus went away"))

		_, err := pinner.Fix(ctx, false)
		Expect(err).To(MatchError(ContainSubstring("bus went away")))
		Expect(err).To(MatchError(ContainSubstring("3 attempts")))
		Expect(display.CurrentStateCallCount()).To(Equal(3))
	})
})
```

- [ ] **Step 3: Run the suite to verify it fails**

Run: `go test ./internal/pin/...`
Expected: compile failure, `undefined: pin.Pinner`.

- [ ] **Step 4: Write Pinner and Fix**

Replace `internal/pin/pin.go` with:

```go
// Package pin holds a desktop to a pinned layout: it derives the target
// for mutter's current state and applies it when the two differ.
package pin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

//go:generate go tool counterfeiter -generate

// Display is what pinning needs from mutter's display configuration.
//
//counterfeiter:generate . Display
type Display interface {
	CurrentState(ctx context.Context) (layout.State, error)
	Verify(ctx context.Context, t layout.Target) error
	Apply(ctx context.Context, t layout.Target) error
	Subscribe(ctx context.Context) (<-chan struct{}, error)
}

// ErrRejected reports that mutter refused the derived configuration; the
// wrapped error carries mutter's reason.
var ErrRejected = errors.New("configuration rejected by mutter")

// transient marks a failure worth retrying from a fresh state.
type transient struct{ err error }

func (t transient) Error() string { return t.err.Error() }
func (t transient) Unwrap() error { return t.err }

// Pinner holds a desktop to the layout Load returns.
type Pinner struct {
	Display Display
	// Load returns the pinned layout; it runs on every Fix so a rewritten
	// layout file takes effect without a restart.
	Load func() (layout.Layout, error)
	// Retries is the number of attempts at a transient failure; zero means 3.
	Retries int
	// RetryDelay is the pause between attempts; zero means 500ms.
	RetryDelay time.Duration
}

// Result is what one Fix saw and did.
type Result struct {
	// State is what mutter showed when the target was derived; it is
	// filled whenever a state was read, even when the fix then failed.
	State  layout.State
	Target layout.Target
	// Changed reports that the target differed from what mutter showed.
	Changed bool
	// Applied reports that the target was applied.
	Applied bool
}

func (p *Pinner) attempts() int { return cmp.Or(p.Retries, 3) }

func (p *Pinner) delay() time.Duration { return cmp.Or(p.RetryDelay, 500*time.Millisecond) }

// Fix loads the layout, derives the target for the current state, and
// applies it when the two differ. With dryRun it stops after mutter
// verifies the target. A transient failure (reading the state, a call
// that timed out, a state that changed underneath the verify, a failed
// apply) is retried from a fresh state; a rejection by mutter, a layout
// that cannot be loaded, and a layout that fits no monitor are not.
func (p *Pinner) Fix(ctx context.Context, dryRun bool) (Result, error) {
	var err error
	for attempt := range p.attempts() {
		if attempt > 0 {
			slog.WarnContext(ctx, "retrying after transient failure", slog.Int("attempt", attempt+1), slog.Any("error", err))
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(p.delay()):
			}
		}
		var res Result
		res, err = p.fixOnce(ctx, dryRun)
		var t transient
		if err == nil || !errors.As(err, &t) {
			return res, err
		}
	}
	return Result{}, fmt.Errorf("giving up after %d attempts: %w", p.attempts(), err)
}

func (p *Pinner) fixOnce(ctx context.Context, dryRun bool) (Result, error) {
	l, err := p.Load()
	if err != nil {
		return Result{}, fmt.Errorf("loading layout: %w", err)
	}
	state, err := p.Display.CurrentState(ctx)
	if err != nil {
		return Result{}, transient{fmt.Errorf("reading display state: %w", err)}
	}
	target, err := layout.Derive(l, state)
	if err != nil {
		return Result{State: state}, fmt.Errorf("deriving layout: %w", err)
	}
	res := Result{State: state, Target: target}
	if layout.Equal(target, state) {
		slog.DebugContext(ctx, "layout already pinned", slog.Uint64("serial", uint64(state.Serial)))
		return res, nil
	}
	res.Changed = true
	logDerivation(ctx, target)
	if err := p.Display.Verify(ctx, target); err != nil {
		return res, p.classifyVerify(ctx, target, err)
	}
	if dryRun {
		return res, nil
	}
	if err := p.Display.Apply(ctx, target); err != nil {
		return res, transient{fmt.Errorf("applying layout: %w", err)}
	}
	res.Applied = true
	slog.InfoContext(ctx, "layout applied", slog.Int("monitors", len(target.Cells)), slog.Uint64("serial", uint64(target.Serial)))
	return res, nil
}

// classifyVerify decides what a failed verify means: a canceled context
// is returned as is; a timeout, a state that changed underneath the
// verify, and a re-read that failed are transient; anything else is
// mutter rejecting the configuration, which no retry can change.
func (p *Pinner) classifyVerify(ctx context.Context, target layout.Target, err error) error {
	if ctx.Err() != nil {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return transient{fmt.Errorf("verifying layout: %w", err)}
	}
	fresh, rerr := p.Display.CurrentState(ctx)
	if rerr != nil {
		return transient{fmt.Errorf("re-reading display state after a failed verify: %w", errors.Join(err, rerr))}
	}
	if fresh.Serial != target.Serial {
		return transient{fmt.Errorf("display state changed during verify: %w", err)}
	}
	return fmt.Errorf("%w: %w", ErrRejected, err)
}

// logDerivation records what Derive left out or improvised, once per
// layout that is about to be verified.
func logDerivation(ctx context.Context, target layout.Target) {
	for _, id := range target.Missing {
		slog.InfoContext(ctx, "pinned monitor not connected", slog.String("product", id.Product), slog.String("serial", id.Serial))
	}
	for _, c := range target.Unknown {
		slog.WarnContext(ctx, "monitor not in layout, placed to the right", slog.String("connector", c))
	}
	for _, w := range target.Warnings {
		slog.WarnContext(ctx, "layout adjusted", slog.String("detail", w))
	}
}
```

- [ ] **Step 5: Run the suite to verify it passes**

Run: `go test ./internal/pin/...`
Expected: PASS.

- [ ] **Step 6: Run the gate and commit**

Run: `go fix ./... && make check`
Expected: exit 0, including `generate-check` since the fake is committed.

```bash
git add internal/pin
git commit -m "feat(pin): fix once with verify and retries" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 8: pin.Watch

**Files:**
- Create: `internal/pin/watch.go`
- Test: `internal/pin/watch_test.go`

**Interfaces:**
- Consumes: Task 7 `Pinner`, `Fix`, `Display.Subscribe`.
- Produces: `(*Pinner).Watch(ctx, debounce time.Duration) error`; `pin.ErrDisconnected`.

- [ ] **Step 1: Write the failing Watch specs**

`internal/pin/watch_test.go`. The fake returns the linear state with serial 8 on the first read and serial 9 afterwards, so the startup fix sees one hardware state and every later fix sees a changed one, unless a spec says otherwise. The debounce is 250 ms and the quiet windows are 120 ms, so a throttle that only starts the timer on the first signal fires inside the second window and fails, while a debounce that resets on every signal fires 130 ms after the last window. Each spec starts the loop from its own `SpecContext`, since Ginkgo cancels a setup node's context when that node returns.

```go
package pin_test

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
	"github.com/jhoblitt/gnome-monitor-pin/internal/layout/layouttest"
	"github.com/jhoblitt/gnome-monitor-pin/internal/pin"
	"github.com/jhoblitt/gnome-monitor-pin/internal/pin/pinfakes"
)

var _ = Describe("Pinner.Watch", func() {
	const (
		debounce = 250 * time.Millisecond
		quiet    = 120 * time.Millisecond
		poll     = time.Millisecond
		timeout  = 2 * time.Second
	)

	var (
		display  *pinfakes.FakeDisplay
		pinner   *pin.Pinner
		events   chan struct{}
		done     chan error
		finished chan struct{}
		cancel   context.CancelFunc
	)

	applies := func() int { return display.ApplyCallCount() }

	// start runs Watch until the spec's context ends or cancel is called.
	start := func(ctx context.Context) {
		var wctx context.Context
		wctx, cancel = context.WithCancel(ctx)
		go func() {
			defer close(finished)
			done <- pinner.Watch(wctx, debounce)
		}()
	}

	BeforeEach(func() {
		display = &pinfakes.FakeDisplay{}
		display.CurrentStateReturns(layouttest.WithSerial(layouttest.LinearState(), 9), nil)
		display.CurrentStateReturnsOnCall(0, layouttest.LinearState(), nil)
		events = make(chan struct{}, 8)
		display.SubscribeReturns(events, nil)
		pinner = &pin.Pinner{
			Display:    display,
			Load:       func() (layout.Layout, error) { return layouttest.GridLayout(), nil },
			RetryDelay: time.Millisecond,
		}
		done = make(chan error, 1)
		finished = make(chan struct{})
		cancel = nil
	})

	AfterEach(func() {
		if cancel != nil {
			cancel()
			Eventually(finished).WithTimeout(timeout).Should(BeClosed())
		}
	})

	It("fixes once at startup", func(ctx SpecContext) {
		start(ctx)
		Eventually(applies).WithTimeout(timeout).WithPolling(poll).Should(Equal(1))
		Consistently(applies).WithTimeout(2 * debounce).WithPolling(poll).Should(Equal(1))
	})

	It("resets the debounce on every signal and then fixes once", func(ctx SpecContext) {
		start(ctx)
		Eventually(applies).WithTimeout(timeout).WithPolling(poll).Should(Equal(1))
		for range 3 {
			events <- struct{}{}
			Consistently(applies).WithTimeout(quiet).WithPolling(poll).Should(Equal(1))
		}
		Eventually(applies).WithTimeout(timeout).WithPolling(poll).Should(Equal(2))
		Consistently(applies).WithTimeout(2 * debounce).WithPolling(poll).Should(Equal(2))
	})

	It("leaves a change with an unchanged serial alone", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.LinearState(), nil)
		start(ctx)
		Eventually(applies).WithTimeout(timeout).WithPolling(poll).Should(Equal(1))
		events <- struct{}{}
		Eventually(display.CurrentStateCallCount).WithTimeout(timeout).WithPolling(poll).Should(BeNumerically(">=", 2), "the state is read to compare serials")
		Consistently(applies).WithTimeout(2 * debounce).WithPolling(poll).Should(Equal(1))
	})

	It("does nothing when the hardware changed but mutter already restored the layout", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.WithSerial(layouttest.GridState(), 9), nil)
		start(ctx)
		Eventually(applies).WithTimeout(timeout).WithPolling(poll).Should(Equal(1))
		events <- struct{}{}
		Eventually(display.CurrentStateCallCount).WithTimeout(timeout).WithPolling(poll).Should(BeNumerically(">=", 3))
		Consistently(applies).WithTimeout(2 * debounce).WithPolling(poll).Should(Equal(1))
	})

	It("treats a failed gate read as a hardware change and lets the fix retry", func(ctx SpecContext) {
		display.CurrentStateReturnsOnCall(1, layout.State{}, errors.New("timed out"))
		start(ctx)
		Eventually(applies).WithTimeout(timeout).WithPolling(poll).Should(Equal(1))
		events <- struct{}{}
		Eventually(applies).WithTimeout(timeout).WithPolling(poll).Should(Equal(2))
	})

	It("keeps watching after mutter rejects a target", func(ctx SpecContext) {
		display.VerifyReturns(errors.New("Logical monitors not adjacent"))
		start(ctx)
		Eventually(display.VerifyCallCount).WithTimeout(timeout).WithPolling(poll).Should(Equal(2), "the startup fix verifies once per attempt")
		Consistently(display.VerifyCallCount).WithTimeout(quiet).WithPolling(poll).Should(Equal(2), "a rejection is not retried on its own")
		reads := display.CurrentStateCallCount()
		events <- struct{}{}
		Eventually(display.CurrentStateCallCount).WithTimeout(timeout).WithPolling(poll).Should(BeNumerically(">=", reads+1), "the signal reached the serial gate")
		Consistently(done).WithTimeout(2 * debounce).ShouldNot(Receive())
		Expect(applies()).To(BeZero())
	})

	It("remembers a rejected state so the same serial is not verified again", func(ctx SpecContext) {
		display.CurrentStateReturns(layouttest.WithSerial(layouttest.LinearState(), 9), nil)
		display.CurrentStateReturnsOnCall(0, layouttest.WithSerial(layouttest.LinearState(), 9), nil)
		display.VerifyReturns(errors.New("Logical monitors not adjacent"))
		start(ctx)
		Eventually(display.VerifyCallCount).WithTimeout(timeout).WithPolling(poll).Should(Equal(1))
		events <- struct{}{}
		Eventually(display.CurrentStateCallCount).WithTimeout(timeout).WithPolling(poll).Should(BeNumerically(">=", 3), "the gate reads the state")
		Consistently(display.VerifyCallCount).WithTimeout(2 * debounce).WithPolling(poll).Should(Equal(1), "same serial, no second verify")
	})

	It("keeps watching when the layout cannot be loaded", func(ctx SpecContext) {
		var loads atomic.Int32
		pinner.Load = func() (layout.Layout, error) {
			loads.Add(1)
			return layout.Layout{}, errors.New("no such file")
		}
		start(ctx)
		events <- struct{}{}
		Eventually(loads.Load).WithTimeout(timeout).WithPolling(poll).Should(BeNumerically(">=", 2), "once at startup, once after the signal")
		Consistently(done).WithTimeout(2 * debounce).ShouldNot(Receive())
		Expect(applies()).To(BeZero())
	})

	It("returns ErrDisconnected when the subscription closes", func(ctx SpecContext) {
		start(ctx)
		close(events)
		Eventually(done).WithTimeout(timeout).Should(Receive(MatchError(pin.ErrDisconnected)))
	})

	It("returns nil when canceled", func(ctx SpecContext) {
		start(ctx)
		cancel()
		Eventually(done).WithTimeout(timeout).Should(Receive(BeNil()))
	})

	It("returns nil when the subscription closes after cancellation", func(ctx SpecContext) {
		start(ctx)
		cancel()
		close(events)
		Eventually(done).WithTimeout(timeout).Should(Receive(BeNil()))
	})
})
```

In "keeps watching after mutter rejects a target" the startup fix first sees the serial move from 8 to 9 underneath the verify and retries, then sees the rejection and records that state; the later signal finds the serial unchanged and applies nothing. "remembers a rejected state" pins that recording: every read returns serial 9, so without it the signal would verify again.

- [ ] **Step 2: Run the suite to verify it fails**

Run: `go test ./internal/pin/...`
Expected: compile failure, `pinner.Watch undefined`.

- [ ] **Step 3: Write Watch**

`internal/pin/watch.go`:

```go
package pin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

// ErrDisconnected reports that the display subscription ended while
// watching, which happens when the bus connection closes.
var ErrDisconnected = errors.New("display connection closed")

// Watch applies the layout now and again after each burst of display
// signals that follows a hardware change, until ctx ends or the
// subscription closes. A burst is any run of signals less than debounce
// apart. Mutter bumps its configuration serial only when it re-reads the
// hardware, never when a configuration is applied, so a signal whose
// state carries the serial of the last fix is a deliberate change by
// Settings, gdctl, or this tool, and is left alone.
func (p *Pinner) Watch(ctx context.Context, debounce time.Duration) error {
	events, err := p.Display.Subscribe(ctx)
	if err != nil {
		return fmt.Errorf("watching for monitor changes: %w", err)
	}
	last := p.fixAndLog(ctx, nil, nil, nil)

	timer := time.NewTimer(debounce)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-events:
			if !ok {
				select {
				case <-ctx.Done():
					return nil
				default:
					return ErrDisconnected
				}
			}
			slog.DebugContext(ctx, "display signal received", slog.Duration("debounce", debounce))
			timer.Reset(debounce)
		case <-timer.C:
			if last == nil {
				last = p.fixAndLog(ctx, nil, nil, nil)
				continue
			}
			changed, added, removed := p.hardwareChanged(ctx, *last)
			if !changed {
				continue
			}
			last = p.fixAndLog(ctx, last, added, removed)
		}
	}
}

// hardwareChanged reads the current state and reports whether mutter has
// re-read the hardware since last, with the connectors that came and
// went. A read that fails counts as a change, so the fix's own retries
// take over rather than the event being dropped.
func (p *Pinner) hardwareChanged(ctx context.Context, last layout.State) (changed bool, added, removed []string) {
	state, err := p.Display.CurrentState(ctx)
	if err != nil {
		slog.WarnContext(ctx, "reading display state for the serial check", slog.Any("error", err))
		return true, nil, nil
	}
	if state.Serial == last.Serial {
		slog.DebugContext(ctx, "no hardware change, leaving layout alone", slog.Uint64("serial", uint64(state.Serial)))
		return false, nil, nil
	}
	added, removed = connectorDiff(last, state)
	slog.DebugContext(ctx, "hardware re-read",
		slog.Uint64("old_serial", uint64(last.Serial)), slog.Uint64("new_serial", uint64(state.Serial)),
		slog.Any("added", added), slog.Any("removed", removed))
	return true, added, removed
}

// fixAndLog runs Fix for the watch loop, where no failure is fatal, and
// returns the state it saw, or prev when it saw none. A repair after a
// hardware change logs the connectors that came and went; a repair with
// neither is the tell for a mutter that bumps its serial on every apply.
func (p *Pinner) fixAndLog(ctx context.Context, prev *layout.State, added, removed []string) *layout.State {
	res, err := p.Fix(ctx, false)
	if ctx.Err() != nil {
		return prev
	}
	switch {
	case err == nil && res.Applied:
		slog.InfoContext(ctx, "layout repaired", slog.Any("added", added), slog.Any("removed", removed))
	case errors.Is(err, ErrRejected):
		slog.WarnContext(ctx, "leaving layout as mutter set it", slog.Any("error", err))
	case err != nil:
		slog.ErrorContext(ctx, "fix failed", slog.Any("error", err))
	}
	if res.State.Serial != 0 {
		return &res.State
	}
	return prev
}

func connectorDiff(before, after layout.State) (added, removed []string) {
	was := make(map[string]bool, len(before.Monitors))
	for _, m := range before.Monitors {
		was[m.Connector] = true
	}
	is := make(map[string]bool, len(after.Monitors))
	for _, m := range after.Monitors {
		is[m.Connector] = true
		if !was[m.Connector] {
			added = append(added, m.Connector)
		}
	}
	for _, m := range before.Monitors {
		if !is[m.Connector] {
			removed = append(removed, m.Connector)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	return added, removed
}
```

`timer.Reset` without draining is correct under Go 1.23 timer semantics, which `go 1.27` selects. The closed-subscription branch uses a `select` rather than `ctx.Err()` so `nilerr` has no error value to object to. Any result that carries a serial updates `last`, rejections and derivation failures included: the failure is deterministic for that hardware state, and a later signal with the same serial must not be mistaken for hardware.

- [ ] **Step 4: Run the suite to verify it passes**

Run: `go test -race ./internal/pin/...`
Expected: PASS.

- [ ] **Step 5: Run the gate and commit**

Run: `go fix ./... && make check`
Expected: exit 0.

```bash
git add internal/pin
git commit -m "feat(pin): watch hardware changes with debounce" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 9: CLI

**Files:**
- Modify: `internal/cli/root.go` (replace the greeting)
- Delete: `internal/cli/root_test.go` (the scaffold's greeting spec)
- Create: `internal/cli/layoutfile.go`, `internal/cli/print.go`, `internal/cli/show.go`, `internal/cli/save.go`, `internal/cli/fix.go`, `internal/cli/watch.go`
- Create: `internal/cli/clifakes/fake_display.go` (generated, after the commands exist)
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: `displayconfig.Connect`, `pin.Pinner`, `pin.Display`, `layout.Snapshot`, `layout.Capture`.
- Produces: `cli.Run` (unchanged signature); `cli.RunWith(ctx, args, stdin, stdout, stderr, connect Connect) error`; `cli.Display` (`pin.Display` plus `Close() error`); `cli.Connect func(ctx) (Display, error)`; `cli.ErrLayoutExists`.

- [ ] **Step 1: Write the failing CLI specs**

Delete `internal/cli/root_test.go`. Write `internal/cli/cli_test.go`:

```go
package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"

	"github.com/jhoblitt/gnome-monitor-pin/internal/cli"
	"github.com/jhoblitt/gnome-monitor-pin/internal/cli/clifakes"
	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
	"github.com/jhoblitt/gnome-monitor-pin/internal/layout/layouttest"
)

var _ = Describe("gnome-monitor-pin", func() {
	var (
		display *clifakes.FakeDisplay
		connect cli.Connect
		dir     string
		path    string
		stdout  *gbytes.Buffer
		stderr  *gbytes.Buffer
	)

	run := func(ctx context.Context, args ...string) error {
		GinkgoHelper()
		return cli.RunWith(ctx, append([]string{"--log-format", "text", "--layout", path}, args...), nil, stdout, stderr, connect)
	}

	out := func() string { return string(stdout.Contents()) }

	BeforeEach(func() {
		display = &clifakes.FakeDisplay{}
		display.CurrentStateReturns(layouttest.LinearState(), nil)
		connect = func(context.Context) (cli.Display, error) { return display, nil }
		dir = GinkgoT().TempDir()
		path = filepath.Join(dir, "layout.json")
		stdout = gbytes.NewBuffer()
		stderr = gbytes.NewBuffer()
	})

	Describe("show", func() {
		It("prints one row per monitor with its connector and identity", func(ctx SpecContext) {
			Expect(run(ctx, "show")).To(Succeed())
			Expect(out()).To(ContainSubstring("CONNECTOR"))
			Expect(out()).To(MatchRegexp(`DP-9\s+DEL\s+DELL U2413\s+SER-F\s+9600,0`))
			Expect(display.CloseCallCount()).To(Equal(1))
		})

		It("lists a switched-off monitor with the position off", func(ctx SpecContext) {
			display.CurrentStateReturns(layouttest.Disabled(layouttest.GridState(), "DP-9"), nil)
			Expect(run(ctx, "show")).To(Succeed())
			Expect(out()).To(MatchRegexp(`DP-9\s+DEL\s+DELL U2413\s+SER-F\s+off`))
		})
	})

	Describe("save", func() {
		It("writes the current layout and shows what it captured", func(ctx SpecContext) {
			display.CurrentStateReturns(layouttest.GridState(), nil)
			Expect(run(ctx, "save")).To(Succeed())
			Expect(out()).To(ContainSubstring("saved 6 monitors to " + path))
			Expect(out()).To(MatchRegexp(`DP-9\s+DEL\s+DELL U2413\s+SER-F\s+3840,1200`))
			Expect(out()).To(ContainSubstring("Settings"))

			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			var l layout.Layout
			Expect(json.Unmarshal(data, &l)).To(Succeed())
			Expect(l).To(Equal(layouttest.GridLayout()))
		})

		It("leaves no temporary file behind", func(ctx SpecContext) {
			Expect(run(ctx, "save")).To(Succeed())
			entries, err := os.ReadDir(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1))
			Expect(entries[0].Name()).To(Equal("layout.json"))
		})

		It("refuses to overwrite without --force", func(ctx SpecContext) {
			Expect(os.WriteFile(path, []byte("{}"), 0o600)).To(Succeed())
			Expect(run(ctx, "save")).To(MatchError(cli.ErrLayoutExists))
			Expect(run(ctx, "save", "--force")).To(Succeed())
		})

		It("replaces the target of a symlinked layout file", func(ctx SpecContext) {
			real := filepath.Join(dir, "real.json")
			Expect(os.WriteFile(real, []byte("{}"), 0o600)).To(Succeed())
			Expect(os.Symlink(real, path)).To(Succeed())
			Expect(run(ctx, "save", "--force")).To(Succeed())
			info, err := os.Lstat(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode() & os.ModeSymlink).NotTo(BeZero(), "the symlink survives")
			data, err := os.ReadFile(real)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("SER-A"))
		})

		It("warns when two captured monitors share an identity", func(ctx SpecContext) {
			s := layouttest.GridState()
			s.Monitors[1].ID.Serial = "SER-A"
			display.CurrentStateReturns(s, nil)
			Expect(run(ctx, "save")).To(Succeed())
			Expect(string(stderr.Contents())).To(ContainSubstring("share identity"))
		})
	})

	Describe("fix", func() {
		It("names the layout path and does not connect when there is no file", func(ctx SpecContext) {
			connect = func(context.Context) (cli.Display, error) {
				Fail("connect called without a layout")
				return nil, nil
			}
			err := run(ctx, "fix")
			Expect(err).To(MatchError(ContainSubstring(path)))
			Expect(err).To(MatchError(ContainSubstring("save")))
		})

		It("applies the saved layout", func(ctx SpecContext) {
			writeGrid(path)
			Expect(run(ctx, "fix")).To(Succeed())
			Expect(display.ApplyCallCount()).To(Equal(1))
			Expect(out()).To(ContainSubstring("applied layout to 6 monitors"))
		})

		It("reports when nothing needs doing", func(ctx SpecContext) {
			writeGrid(path)
			display.CurrentStateReturns(layouttest.GridState(), nil)
			Expect(run(ctx, "fix")).To(Succeed())
			Expect(display.ApplyCallCount()).To(BeZero())
			Expect(out()).To(ContainSubstring("layout already pinned"))
		})

		It("prints current and target positions and the notes without applying on --dry-run", func(ctx SpecContext) {
			l := layouttest.GridLayout()
			l.Monitors = l.Monitors[:5]
			writeLayout(path, l)
			Expect(run(ctx, "fix", "--dry-run")).To(Succeed())
			Expect(display.VerifyCallCount()).To(Equal(1))
			Expect(display.ApplyCallCount()).To(BeZero())
			Expect(out()).To(ContainSubstring("would apply"))
			Expect(out()).To(MatchRegexp(`CONNECTOR\s+CURRENT\s+TARGET`))
			Expect(out()).To(MatchRegexp(`DP-20\s+7680,0\s+1920,1200`))
			Expect(out()).To(ContainSubstring("unknown: DP-9"))
		})

		It("prints the table on --dry-run even when already pinned", func(ctx SpecContext) {
			writeGrid(path)
			display.CurrentStateReturns(layouttest.GridState(), nil)
			Expect(run(ctx, "fix", "--dry-run")).To(Succeed())
			Expect(display.VerifyCallCount()).To(BeZero(), "nothing differs, so nothing is verified")
			Expect(display.ApplyCallCount()).To(BeZero())
			Expect(out()).To(ContainSubstring("layout already pinned; would apply:"))
			Expect(out()).To(MatchRegexp(`DP-9\s+3840,1200\s+3840,1200`))
		})
	})

	Describe("watch", func() {
		It("fixes at startup and returns when canceled", func(ctx SpecContext) {
			writeGrid(path)
			events := make(chan struct{})
			display.SubscribeReturns(events, nil)
			wctx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- run(wctx, "watch", "--debounce", "10ms") }()
			Eventually(display.ApplyCallCount).WithTimeout(time.Second).WithPolling(time.Millisecond).Should(Equal(1))
			cancel()
			Eventually(done).WithTimeout(time.Second).Should(Receive(BeNil()))
		})

		It("subscribes, tries a fix, and keeps running without a layout file", func(ctx SpecContext) {
			events := make(chan struct{})
			display.SubscribeReturns(events, nil)
			wctx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- run(wctx, "watch", "--debounce", "10ms") }()
			Eventually(display.SubscribeCallCount).WithTimeout(time.Second).WithPolling(time.Millisecond).Should(Equal(1))
			Eventually(stderr).WithTimeout(time.Second).WithPolling(time.Millisecond).Should(gbytes.Say("fix failed.*" + regexp.QuoteMeta(path)))
			Consistently(done).WithTimeout(50 * time.Millisecond).ShouldNot(Receive())
			cancel()
			Eventually(done).WithTimeout(time.Second).Should(Receive(BeNil()))
		})
	})
})

func writeGrid(path string) {
	GinkgoHelper()
	writeLayout(path, layouttest.GridLayout())
}

func writeLayout(path string, l layout.Layout) {
	GinkgoHelper()
	data, err := json.Marshal(l)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(path, data, 0o600)).To(Succeed())
}
```

`layouttest.LinearState` puts `DP-9` at x = 5 × 1920 = 9600 and `DP-20` at 4 × 1920 = 7680, which the regexps rely on. The output buffers are `gbytes.Buffer`, which is safe to read from the spec goroutine while the `watch` command writes to it from another. The dry-run spec pins a five-entry layout so the sixth monitor shows up as an unknown note.

- [ ] **Step 2: Run the suite to verify it fails**

Run: `go test ./internal/cli/...`
Expected: setup failure, `cannot find module providing package .../internal/cli/clifakes`, since the fake does not exist yet.

- [ ] **Step 3: Rewrite root.go**

`internal/cli/root.go`:

```go
// Package cli builds the gnome-monitor-pin command tree.
//
// Managed by go-conventions (references/cli.md owns the cobra and viper
// contract, references/logging.md the logger). Configuration precedence is
// flags, then GNOME_MONITOR_PIN_* environment variables, then the config file,
// then defaults, and every value is read back through viper.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jhoblitt/gnome-monitor-pin/internal/displayconfig"
	"github.com/jhoblitt/gnome-monitor-pin/internal/pin"
	"github.com/jhoblitt/gnome-monitor-pin/internal/version"
)

//go:generate go tool counterfeiter -generate

// Display is the display connection a command uses. displayconfig.Client
// is the real one.
//
//counterfeiter:generate . Display
type Display interface {
	pin.Display
	Close() error
}

// Connect opens a Display. Run uses displayconfig.Connect; specs substitute
// a fake through RunWith.
type Connect func(ctx context.Context) (Display, error)

// Run executes the command tree against args and returns the first error.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return RunWith(ctx, args, stdin, stdout, stderr, connectDisplay)
}

// RunWith is Run with the display connection supplied by the caller.
func RunWith(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, connect Connect) error {
	cmd := newRootCmd(stdin, stdout, stderr, connect)
	cmd.SetArgs(args)
	return cmd.ExecuteContext(ctx)
}

func connectDisplay(ctx context.Context) (Display, error) {
	c, err := displayconfig.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func newRootCmd(stdin io.Reader, stdout, stderr io.Writer, connect Connect) *cobra.Command {
	v := viper.New()
	cmd := &cobra.Command{
		Use:           "gnome-monitor-pin",
		Short:         "Pin a GNOME multi-monitor layout across monitor hotplugs",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return configure(cmd, v, stderr)
		},
	}
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	pf := cmd.PersistentFlags()
	pf.String("config", "", "config file to read after flags and environment")
	pf.String("log-level", "info", "log level: debug, info, warn, or error (GNOME_MONITOR_PIN_LOG_LEVEL)")
	pf.String("log-format", "json", "log format: json or text (GNOME_MONITOR_PIN_LOG_FORMAT)")
	pf.String("layout", defaultLayoutPath(), "layout file (GNOME_MONITOR_PIN_LAYOUT)")

	cmd.AddCommand(
		newShowCmd(connect),
		newSaveCmd(v, connect),
		newFixCmd(v, connect),
		newWatchCmd(v, connect),
	)
	return cmd
}

func defaultLayoutPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "layout.json"
	}
	return filepath.Join(dir, "gnome-monitor-pin", "layout.json")
}

// closeDisplay closes d at the end of a command; a failure to close has no
// recovery, so it is logged rather than returned.
func closeDisplay(ctx context.Context, d Display) {
	if err := d.Close(); err != nil {
		slog.WarnContext(ctx, "closing display connection", slog.Any("error", err))
	}
}

// configure binds the command's flags and the environment into v, reads the
// config file when one is named, and installs the default logger. It runs
// before every command, so a subcommand's own flags are bound as well.
func configure(cmd *cobra.Command, v *viper.Viper, stderr io.Writer) error {
	v.SetEnvPrefix("GNOME_MONITOR_PIN")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()
	if err := v.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("binding flags: %w", err)
	}
	if cfg := v.GetString("config"); cfg != "" {
		v.SetConfigFile(cfg)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("reading config %s: %w", cfg, err)
		}
	}
	return setupLogging(v, stderr)
}

func setupLogging(v *viper.Viper, w io.Writer) error {
	var level slog.Level
	if err := level.UnmarshalText([]byte(v.GetString("log-level"))); err != nil {
		return fmt.Errorf("parsing log level: %w", err)
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch format := v.GetString("log-format"); format {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	case "text":
		handler = slog.NewTextHandler(w, opts)
	default:
		return fmt.Errorf("unknown log format %q", format)
	}
	slog.SetDefault(slog.New(handler))
	return nil
}
```

- [ ] **Step 4: Write the layout file store and printers**

`internal/cli/layoutfile.go`:

```go
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
```

`os.CreateTemp` creates the file with mode 0600. sloglint's `context: scope` accepts `slog.Warn` in `removeQuietly` because no context is in scope there.

`internal/cli/print.go`:

```go
package cli

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

func printEntries(w io.Writer, entries []layout.Entry) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CONNECTOR\tVENDOR\tPRODUCT\tSERIAL\tPOSITION\tMODE\tSCALE\tTRANSFORM\tPRIMARY")
	for _, e := range entries {
		if e.Disabled {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\toff\t-\t-\t-\tno\n", e.Connector, e.Vendor, e.Product, e.Serial)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d,%d\t%s\t%g\t%s\t%s\n",
			e.Connector, e.Vendor, e.Product, e.Serial, e.X, e.Y, e.Mode, e.Scale, e.Transform, yesNo(e.Primary))
	}
	return tw.Flush()
}

// printPlan lists each target cell beside where mutter shows that
// connector now, so a dry run reads as a diff.
func printPlan(w io.Writer, state layout.State, target layout.Target) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CONNECTOR\tCURRENT\tTARGET\tMODE\tSCALE\tTRANSFORM\tPRIMARY")
	for _, c := range target.Cells {
		fmt.Fprintf(tw, "%s\t%s\t%d,%d\t%s\t%g\t%s\t%s\n",
			c.Connector, currentPosition(state, c.Connector), c.X, c.Y, c.Mode, c.Scale, c.Transform, yesNo(c.Primary))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, id := range target.Missing {
		fmt.Fprintf(w, "missing: %s %s %s is not connected\n", id.Vendor, id.Product, id.Serial)
	}
	for _, c := range target.Unknown {
		fmt.Fprintf(w, "unknown: %s is not in the layout and is placed to the right\n", c)
	}
	for _, note := range target.Warnings {
		fmt.Fprintf(w, "note: %s\n", note)
	}
	return nil
}

func currentPosition(state layout.State, connector string) string {
	for _, l := range state.Logical {
		for _, c := range l.Connectors {
			if c == connector {
				return strconv.Itoa(l.X) + "," + strconv.Itoa(l.Y)
			}
		}
	}
	return "off"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
```

- [ ] **Step 5: Write the four commands, then generate the fake**

`internal/cli/show.go`:

```go
package cli

import (
	"github.com/spf13/cobra"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

func newShowCmd(connect Connect) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the connected monitors and where mutter shows them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			d, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeDisplay(ctx, d)
			state, err := d.CurrentState(ctx)
			if err != nil {
				return err
			}
			entries, err := layout.Snapshot(state)
			if err != nil {
				return err
			}
			return printEntries(cmd.OutOrStdout(), entries)
		},
	}
}
```

`internal/cli/save.go`:

```go
package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

func newSaveCmd(v *viper.Viper, connect Connect) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "save",
		Short: "Write the current layout to the layout file",
		Long: `Write the current layout to the layout file.

Arrange the monitors in GNOME Settings first and click Keep Changes; save
records that arrangement keyed on each monitor's vendor, product, and
serial, so it survives the connector renames a DisplayPort MST hotplug
causes. A monitor switched off in Settings is recorded as disabled. Run
"show" first if unsure what mutter shows now.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			d, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeDisplay(ctx, d)
			state, err := d.CurrentState(ctx)
			if err != nil {
				return err
			}
			entries, err := layout.Snapshot(state)
			if err != nil {
				return err
			}
			l := layout.Layout{Monitors: make([]layout.Placement, 0, len(entries))}
			for _, e := range entries {
				l.Monitors = append(l.Monitors, e.Placement)
			}
			warnDuplicates(ctx, entries)
			path := v.GetString("layout")
			if err := writeLayout(path, l, v.GetBool("force")); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "saved %d monitors to %s\n", len(l.Monitors), path)
			if err := printEntries(out, entries); err != nil {
				return err
			}
			fmt.Fprintln(out, "If this is not the arrangement you want, arrange it in Settings, click Keep Changes, then run save --force.")
			return nil
		},
	}
	cmd.Flags().Bool("force", false, "overwrite an existing layout file (GNOME_MONITOR_PIN_FORCE)")
	return cmd
}

// warnDuplicates logs every identity two captured monitors share, since
// fix can only tell such monitors apart by order.
func warnDuplicates(ctx context.Context, entries []layout.Entry) {
	seen := make(map[layout.ID]string, len(entries))
	for _, e := range entries {
		if other, dup := seen[e.ID]; dup {
			slog.WarnContext(ctx, "monitors share identity and will be matched by order",
				slog.String("serial", e.Serial), slog.String("connector", e.Connector), slog.String("other", other))
			continue
		}
		seen[e.ID] = e.Connector
	}
}
```

`internal/cli/fix.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
	"github.com/jhoblitt/gnome-monitor-pin/internal/pin"
)

func newFixCmd(v *viper.Viper, connect Connect) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fix",
		Short: "Apply the pinned layout once",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			path := v.GetString("layout")
			if _, err := readLayout(path); err != nil {
				return err
			}
			d, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeDisplay(ctx, d)
			dryRun := v.GetBool("dry-run")
			p := &pin.Pinner{Display: d, Load: func() (layout.Layout, error) { return readLayout(path) }}
			res, err := p.Fix(ctx, dryRun)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case dryRun:
				if !res.Changed {
					fmt.Fprintln(out, "layout already pinned; would apply:")
				} else {
					fmt.Fprintln(out, "would apply:")
				}
				return printPlan(out, res.State, res.Target)
			case !res.Changed:
				fmt.Fprintln(out, "layout already pinned")
			default:
				fmt.Fprintf(out, "applied layout to %d monitors\n", len(res.Target.Cells))
			}
			return nil
		},
	}
	cmd.Flags().Bool("dry-run", false, "verify the derived layout with mutter and print it without applying (GNOME_MONITOR_PIN_DRY_RUN)")
	return cmd
}
```

`internal/cli/watch.go`:

```go
package cli

import (
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
	"github.com/jhoblitt/gnome-monitor-pin/internal/pin"
)

func newWatchCmd(v *viper.Viper, connect Connect) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Apply the pinned layout now and after every hardware change",
		Long: `Apply the pinned layout now and after every hardware change.

The layout file is read again for every apply, so "save --force" takes
effect without a restart. A change made in Settings or with gdctl is left
alone; only a hotplug, re-enumeration, or resume triggers an apply.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			path := v.GetString("layout")
			slog.InfoContext(ctx, "watching", slog.String("layout", path), slog.Duration("debounce", v.GetDuration("debounce")))
			d, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeDisplay(ctx, d)
			p := &pin.Pinner{Display: d, Load: func() (layout.Layout, error) { return readLayout(path) }}
			return p.Watch(ctx, v.GetDuration("debounce"))
		},
	}
	cmd.Flags().Duration("debounce", time.Second, "quiet time after a display signal before applying (GNOME_MONITOR_PIN_DEBOUNCE)")
	return cmd
}
```

Now generate the fake, which needs every function `root.go` references to exist:

Run: `go generate ./internal/cli/...`
Expected: `internal/cli/clifakes/fake_display.go` appears with all five methods.

- [ ] **Step 6: Run the suite to verify it passes**

Run: `go test -race ./internal/cli/...`
Expected: PASS. Build the binary and check the tree once: `go build -o "$TMPDIR/gmp" ./cmd/gnome-monitor-pin && "$TMPDIR/gmp" --help` lists `show`, `save`, `fix`, `watch`.

- [ ] **Step 7: Stage, gate, and commit**

Run: `git add -A internal/cli && go fix ./... && make check`
Expected: exit 0. Staging comes first because `generate-check` diffs tracked files, and `root.go` and the deleted `root_test.go` are tracked.

```bash
git add -A internal/cli
git commit -m "feat(cli): show, save, fix, and watch commands" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 10: systemd unit and README

**Files:**
- Create: `contrib/gnome-monitor-pin.service`
- Create: `README.md`

**Interfaces:**
- Consumes: the `watch` command from Task 9.
- Produces: the unit Task 11 packages.

- [ ] **Step 1: Write the unit**

`contrib/gnome-monitor-pin.service`. It names the binary where the RPM installs it; a `go install` user overrides `ExecStart` (README).

```ini
# The RPM installs this unit; from a checkout:
#   cp contrib/gnome-monitor-pin.service ~/.config/systemd/user/
#   systemctl --user daemon-reload
# Then: systemctl --user enable --now gnome-monitor-pin
[Unit]
Description=Pin the GNOME monitor layout across hotplugs
PartOf=graphical-session.target
After=graphical-session.target
# The shell's unit sets and unsets this in the user manager's environment.
ConditionEnvironment=WAYLAND_DISPLAY

[Service]
Type=exec
ExecStart=/usr/bin/gnome-monitor-pin watch
Environment=GNOME_MONITOR_PIN_LOG_FORMAT=text
Restart=on-failure
RestartSec=2

[Install]
WantedBy=graphical-session.target
```

- [ ] **Step 2: Write the README**

`README.md`. The Development and License sections that github-conventions' skeleton carries are added when the repository is created, because they describe a LICENSE file, pinned actions, and a commitlint workflow that do not exist yet.

````markdown
# gnome-monitor-pin

Pin a GNOME multi-monitor layout across monitor hotplugs.

## Why

GNOME remembers a monitor arrangement for the exact set of monitors it was
made for, and identifies each monitor by its connector name as well as its
EDID. Two things go wrong on a desk with DisplayPort MST daisy chains:

- When a monitor is powered off, no arrangement was ever saved for the
  monitors that remain, and mutter has no rule for keeping the survivors
  where they are, so it lays them out in one long row.
- When it is powered on again, its MST hub re-enumerates and the kernel
  gives it a new connector name, so the saved arrangement for the full set
  no longer matches and mutter lays everything out in a row again.

This tool records the arrangement keyed on vendor, product, and serial, and
puts it back for whatever subset is present whenever mutter reports a
hardware change. Upstream tracks the connector-name half as mutter issue
#4036 and merge request !984; nothing upstream addresses the survivor half.

## Install

From a release: download the RPM for your Fedora version from the release
page and install it.

```sh
sudo dnf install ./gnome-monitor-pin-*.fc43.x86_64.rpm
```

With Go: `go install github.com/jhoblitt/gnome-monitor-pin/cmd/gnome-monitor-pin@latest`,
then copy `contrib/gnome-monitor-pin.service` to `~/.config/systemd/user/`
and point it at the binary:

```sh
systemctl --user edit gnome-monitor-pin
# add:
# [Service]
# ExecStart=
# ExecStart=%h/go/bin/gnome-monitor-pin watch
```

## Usage

1. Arrange the monitors in GNOME Settings, apply, and click Keep Changes.
2. Check what mutter shows: `gnome-monitor-pin show`. A monitor you
   switched off in Settings is listed as `off`.
3. Record it: `gnome-monitor-pin save`. It lands in
   `~/.config/gnome-monitor-pin/layout.json` and prints what it captured.
4. Check the derived configuration against the live session without
   changing it: `gnome-monitor-pin fix --dry-run`. It prints each
   monitor's current and target position and every note the derivation
   made.
5. Hold it: `systemctl --user enable --now gnome-monitor-pin`.

`gnome-monitor-pin fix` applies the pinned layout once. Run commands in a
terminal with `--log-format text` for readable logs.

After a hotplug, a resume from suspend, or a login, the desktop shows
mutter's row for about a second, then the pinned layout returns; windows
may move twice. `--debounce` on `watch` trades that second against extra
applies during a slow MST re-enumeration.

When a pinned monitor is off, the others stay where they are; a column or
row left completely empty closes up, and survivors that would only touch
at corners (which mutter refuses) close up into a block. A few survivor
sets of a mixed-size layout cannot be compacted into a connected block;
the tool logs that and leaves mutter's layout alone. A monitor mutter
shows that the layout does not know is placed to the right of the pinned
ones, never disabled; a monitor recorded as disabled stays off even after
mutter's own fallback switches it on. Applies are temporary, so mutter's
own `monitors.xml` is left alone.

## Changing the layout

`watch` only reacts to hardware changes, so rearrange in Settings as usual,
answer the Keep Changes dialog, then run `gnome-monitor-pin save --force`.
The running service reads the file again at its next apply; no restart is
needed. Until you save, the next hardware change puts the pinned layout
back.

## Stopping

```sh
systemctl --user disable --now gnome-monitor-pin
```

## Troubleshooting

- Logs: `journalctl --user -u gnome-monitor-pin -f`. Add
  `--log-level debug` (or `GNOME_MONITOR_PIN_LOG_LEVEL=debug` in the unit)
  to see every signal and the serial comparison. Every repair logs which
  connectors appeared or disappeared; at startup that list is empty.
- `systemctl --user status` says `inactive` with a condition failed: the
  unit only runs inside a Wayland session, so enabling it from SSH does
  nothing until the next login.
- The unit is `failed` with `start-limit-hit`: the binary was missing when
  it started five times in ten seconds. Fix the cause, then
  `systemctl --user reset-failed gnome-monitor-pin` and
  `systemctl --user start gnome-monitor-pin`. A missing layout file is
  not fatal: the service logs it and waits.
- A change made in Settings snaps back within a second: that would mean
  this mutter bumps its configuration serial on applies, which this tool
  relies on it not doing. The repair line in the journal will name no
  added or removed connectors. Stop the service and report the mutter
  version.
- `fix` says mutter rejected the configuration: the message carries
  mutter's reason (for example a scale a mode does not support). `show`
  and `fix --dry-run` print what was derived.
- Two monitors report the same vendor, product, and serial: `save` warns,
  and `fix` tells them apart by the order mutter lists them, which can
  swap after a re-enumeration.
- Running `watch` in a terminal while the service runs is harmless: both
  see the same serial and the second one finds nothing to do.
- The GDM login screen has its own GNOME session and its own saved
  arrangement; this tool cannot reach it.

Every flag has an environment form under `GNOME_MONITOR_PIN_`, for
example `GNOME_MONITOR_PIN_LAYOUT` and `GNOME_MONITOR_PIN_LOG_LEVEL`.

The integration spec talks to the real session bus with a verify-only
apply: `go test -tags integration ./internal/displayconfig/...` from a
shell inside a GNOME session.
````

- [ ] **Step 3: Run the gate and commit**

Run: `make check`
Expected: exit 0 (nothing in Go changed).

```bash
git add contrib README.md
git commit -m "docs: README and systemd user unit" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 11: RPM packaging for Fedora 43 and 44

**Files:**
- Create: `packaging/gnome-monitor-pin.spec`
- Create: `packaging/build-rpm.sh`
- Modify: `.github/workflows/release.yml` (append the `rpm` and `rpm-check` jobs)

**Interfaces:**
- Consumes: the goreleaser job in `release.yml`, which publishes `gnome-monitor-pin_<version>_linux_amd64.tar.gz` (binary, README, LICENSE) to the GitHub release; the unit from Task 10.
- Produces: `gnome-monitor-pin-<version>-1.fc43.x86_64.rpm` and `...fc44...` as release assets.

Design: the RPM packages the binary goreleaser already built and stamped, so no Go toolchain runs inside the Fedora containers and the RPM's binary is byte-identical to the archive's. `rpmbuild` runs inside `fedora:43` and `fedora:44` images so `%{?dist}`, `%{_userunitdir}`, and the systemd macros are each Fedora's own. A pre-release tag such as `v1.2.3-rc1` becomes RPM version `1.2.3~rc1`.

- [ ] **Step 1: Write the spec**

`packaging/gnome-monitor-pin.spec`:

```spec
# Built by packaging/build-rpm.sh from a release binary; there is no
# %prep or %build because the binary is goreleaser's, already stamped
# with the tag.
#
# The binary is prebuilt and stripped, so there is nothing to extract.
%global debug_package %{nil}

Name:           gnome-monitor-pin
Version:        %{version}
Release:        1%{?dist}
Summary:        Pin a GNOME multi-monitor layout across monitor hotplugs
License:        Apache-2.0
URL:            https://github.com/jhoblitt/gnome-monitor-pin
Source0:        gnome-monitor-pin
Source1:        gnome-monitor-pin.service
Source2:        README.md
Source3:        LICENSE
BuildArch:      x86_64
BuildRequires:  systemd-rpm-macros
%{?systemd_requires}

%description
gnome-monitor-pin records a GNOME multi-monitor arrangement keyed on each
monitor's EDID identity and re-applies it whenever mutter re-reads the
hardware, so a DisplayPort MST hotplug no longer collapses the desktop
into a single row. It ships a systemd user unit; enable it with
systemctl --user enable --now gnome-monitor-pin.

%install
install -D -m 0755 %{SOURCE0} %{buildroot}%{_bindir}/gnome-monitor-pin
install -D -m 0644 %{SOURCE1} %{buildroot}%{_userunitdir}/gnome-monitor-pin.service
install -D -m 0644 %{SOURCE2} %{buildroot}%{_docdir}/%{name}/README.md
install -D -m 0644 %{SOURCE3} %{buildroot}%{_licensedir}/%{name}/LICENSE

%post
%systemd_user_post gnome-monitor-pin.service

%preun
%systemd_user_preun gnome-monitor-pin.service

%postun
%systemd_user_postun_with_restart gnome-monitor-pin.service

%files
%{_bindir}/gnome-monitor-pin
%{_userunitdir}/gnome-monitor-pin.service
%doc %{_docdir}/%{name}/
%license %{_licensedir}/%{name}/

%changelog
* Wed Sep 09 2026 Joshua Hoblitt <josh@hoblitt.com> - %{version}-1
- Built from the release archive.
```

The changelog is a static entry, not `%autochangelog`: rpmautospec has no
changelog file and no dist-git history to read here, so it would stamp every
released RPM with its `John Doe <packager@example.com> - local build`
placeholder.

- [ ] **Step 2: Write the build script**

`packaging/build-rpm.sh`:

```sh
#!/bin/sh
# Build the gnome-monitor-pin RPM inside a Fedora container.
#
#   packaging/build-rpm.sh <tag> <archive.tar.gz> <outdir>
#
# <archive.tar.gz> is goreleaser's linux/amd64 archive for <tag>, which
# carries the stamped binary, README.md, and LICENSE. The RPM lands in
# <outdir>. Needs rpm-build and systemd-rpm-macros.
set -eu

tag=$1
archive=$2
out=$3

version=$(printf '%s' "$tag" | sed -e 's/^v//' -e 's/-/~/g')
top=$(mktemp -d)
trap 'rm -rf "$top"' EXIT
mkdir -p "$top/SOURCES" "$top/SPECS" "$top/BUILD" "$top/RPMS"

tar -xzf "$archive" -C "$top/SOURCES" gnome-monitor-pin README.md LICENSE
cp contrib/gnome-monitor-pin.service "$top/SOURCES/"
cp packaging/gnome-monitor-pin.spec "$top/SPECS/"

rpmbuild --define "_topdir $top" --define "version $version" -bb "$top/SPECS/gnome-monitor-pin.spec"

mkdir -p "$out"
cp "$top"/RPMS/x86_64/*.rpm "$out/"
ls -1 "$out"
```

Make it executable: `chmod +x packaging/build-rpm.sh`.

- [ ] **Step 3: Append the rpm jobs to the release workflow**

Append to `.github/workflows/release.yml`, at the same indentation as the `goreleaser` job:

```yaml
  rpm:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    needs: goreleaser
    # Forks must not publish under the upstream name.
    if: github.repository == 'jhoblitt/gnome-monitor-pin'
    permissions:
      contents: read
    strategy:
      fail-fast: false
      matrix:
        fedora: ["43", "44"]
    container:
      image: registry.fedoraproject.org/fedora:${{ matrix.fedora }}
    steps:
      - name: Install packaging tools
        run: dnf -y install rpm-build systemd-rpm-macros git-core gh tar

      - name: Check out repository
        uses: actions/checkout@v7
        with:
          persist-credentials: false

      # goreleaser already built and stamped the binary; the RPM ships
      # exactly the bytes the archive does.
      - name: Download the release archive
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          GH_REPO: ${{ github.repository }}
          TAG: ${{ github.ref_name }}
        run: gh release download "$TAG" --pattern '*_linux_amd64.tar.gz' --output archive.tar.gz

      - name: Build the RPM
        env:
          TAG: ${{ github.ref_name }}
        run: packaging/build-rpm.sh "$TAG" archive.tar.gz out

      - name: Hand the RPM to the check job
        uses: actions/upload-artifact@v4
        with:
          name: rpm-fc${{ matrix.fedora }}
          path: out/*.rpm
          retention-days: 1
          if-no-files-found: error

  rpm-check:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    needs: rpm
    # Forks must not publish under the upstream name.
    if: github.repository == 'jhoblitt/gnome-monitor-pin'
    permissions:
      contents: write
    strategy:
      fail-fast: false
      matrix:
        fedora: ["43", "44"]
    # A container the build never touched: the RPM has to pull its own
    # dependencies, not inherit rpm-build's, gh's, and tar's closures.
    container:
      image: registry.fedoraproject.org/fedora:${{ matrix.fedora }}
    steps:
      - name: Download the RPM
        uses: actions/download-artifact@v4
        with:
          name: rpm-fc${{ matrix.fedora }}
          path: out

      - name: Install the RPM and check the version
        env:
          TAG: ${{ github.ref_name }}
        run: |
          dnf -y install ./out/*.rpm
          reported=$(gnome-monitor-pin --version)
          case "$reported" in
            *"$TAG"*) echo "reports $reported" ;;
            *) echo "binary reports '$reported', want '$TAG'"; exit 1 ;;
          esac
          test -f /usr/lib/systemd/user/gnome-monitor-pin.service

      - name: Upload the RPM to the release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          GH_REPO: ${{ github.repository }}
          TAG: ${{ github.ref_name }}
        run: |
          dnf -y install gh
          gh release upload "$TAG" out/*.rpm --clobber
```

`git-core` is in the tool install so `actions/checkout` produces a real clone;
without it the action falls back to the REST-API tarball, which has no `.git`,
and `gh` — which resolves its base repo from `--repo`, then `GH_REPO`, then a
git remote, and never from `GITHUB_REPOSITORY` — could not determine one. Every
step that runs `gh` also passes `GH_REPO` for that reason. The install and
upload run in `rpm-check`, a container the build job never touched, so the RPM
must pull its own dependencies rather than inherit the build tools' closures;
`gh` is installed there only after the check has passed.

The workflow's `uses:` lines stay unpinned like the rest of the file; the repository-creation hand-off runs `pinact` over every workflow.

- [ ] **Step 4: Lint the workflow and build one RPM locally when podman is available**

Run: `actionlint .github/workflows/release.yml`
Expected: no output.

Then, unsandboxed and only if `command -v podman` succeeds, build a throwaway archive and package it in the Fedora 43 image (the binary reports `(devel)` because there is no tag, so the version is passed by hand):

```sh
go build -o "$TMPDIR/gnome-monitor-pin" ./cmd/gnome-monitor-pin
tar -czf "$TMPDIR/archive.tar.gz" -C "$TMPDIR" gnome-monitor-pin -C "$PWD" README.md LICENSE
podman run --rm -v "$PWD:/src:Z" -v "$TMPDIR:/work:Z" -w /src registry.fedoraproject.org/fedora:43 \
  sh -c 'dnf -y -q install rpm-build systemd-rpm-macros && packaging/build-rpm.sh v0.0.1 /work/archive.tar.gz /work/out && rpm -qpl /work/out/*.rpm && dnf -y -q install /work/out/*.rpm && gnome-monitor-pin --help'
```

Expected: `gnome-monitor-pin-0.0.1-1.fc43.x86_64.rpm`, the file list showing `/usr/bin/gnome-monitor-pin` and `/usr/lib/systemd/user/gnome-monitor-pin.service`, and the help text. Without podman, this step is verified by the first tagged release instead; say so in the report.

- [ ] **Step 5: Commit**

```bash
git add packaging .github/workflows/release.yml
git commit -m "build: package RPMs for Fedora 43 and 44 on release" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 12: acceptance on the real desktop

**Files:** none.

- [ ] **Step 1: Build and inspect**

Run, unsandboxed so the session bus is reachable:

```sh
go build -o "$HOME/go/bin/gnome-monitor-pin" ./cmd/gnome-monitor-pin
gnome-monitor-pin --log-format text show
```

Expected: six rows, the 2x3 grid positions, `DP-4` primary at `0,0`, and no seventh row.

- [ ] **Step 2: Save and dry-run**

```sh
gnome-monitor-pin --log-format text save
gnome-monitor-pin --log-format text fix --dry-run
gnome-monitor-pin --log-format text fix
```

Expected: `saved 6 monitors to /home/jhoblitt/.config/gnome-monitor-pin/layout.json` followed by the table, then `layout already pinned` twice, no apply.

- [ ] **Step 3: Integration spec**

```sh
go test -tags integration -race ./internal/displayconfig/...
```

Expected: PASS.

- [ ] **Step 4: Hotplug, resume, and Settings acceptance**

Left to the user: start `watch` with `--log-level debug`, power one monitor off and on, and watch the grid come back with a `layout repaired` line naming the connector that went and the one that came. Suspend and resume: the row shows briefly, then a repair with no connectors added or removed. Then move a monitor in Settings and click Keep Changes: the daemon must log `no hardware change` and leave it; `save --force` then pins the new arrangement. Record all three results in the PR description when the repository exists.
