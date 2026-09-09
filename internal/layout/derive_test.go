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
			all := []string{"DP-4", "DP-22", "DP-16", "DP-6", "DP-20", "DP-9"}
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
