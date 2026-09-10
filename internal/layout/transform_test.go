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
