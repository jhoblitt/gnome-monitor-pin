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
