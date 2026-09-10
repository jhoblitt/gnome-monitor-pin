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
		Consistently(display.VerifyCallCount).WithTimeout(2*debounce).WithPolling(poll).Should(Equal(1), "same serial, no second verify")
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
