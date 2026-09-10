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
			X:         int32(c.X), //nolint:gosec // desktop coordinates never approach int32 range, and mutter validates the layout it is handed
			Y:         int32(c.Y), //nolint:gosec // desktop coordinates never approach int32 range, and mutter validates the layout it is handed
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
