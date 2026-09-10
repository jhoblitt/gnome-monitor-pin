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
