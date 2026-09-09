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
