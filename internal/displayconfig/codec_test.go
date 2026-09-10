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
