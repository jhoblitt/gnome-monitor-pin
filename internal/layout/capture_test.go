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

	It("rejects a state showing the same connector twice", func() {
		s := layouttest.GridState()
		s.Logical[1].Connectors = []string{"DP-4"}
		_, err := layout.Snapshot(s)
		Expect(err).To(MatchError(ContainSubstring("DP-4")))
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
