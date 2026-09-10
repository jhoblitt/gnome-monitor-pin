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
