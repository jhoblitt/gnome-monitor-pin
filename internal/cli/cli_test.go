package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"

	"github.com/jhoblitt/gnome-monitor-pin/internal/cli"
	"github.com/jhoblitt/gnome-monitor-pin/internal/cli/clifakes"
	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
	"github.com/jhoblitt/gnome-monitor-pin/internal/layout/layouttest"
)

var _ = Describe("gnome-monitor-pin", func() {
	var (
		display *clifakes.FakeDisplay
		connect cli.Connect
		dir     string
		path    string
		stdout  *gbytes.Buffer
		stderr  *gbytes.Buffer
	)

	run := func(ctx context.Context, args ...string) error {
		GinkgoHelper()
		return cli.RunWith(ctx, append([]string{"--log-format", "text", "--layout", path}, args...), nil, stdout, stderr, connect)
	}

	out := func() string { return string(stdout.Contents()) }

	BeforeEach(func() {
		display = &clifakes.FakeDisplay{}
		display.CurrentStateReturns(layouttest.LinearState(), nil)
		connect = func(context.Context) (cli.Display, error) { return display, nil }
		dir = GinkgoT().TempDir()
		path = filepath.Join(dir, "layout.json")
		stdout = gbytes.NewBuffer()
		stderr = gbytes.NewBuffer()
	})

	Describe("show", func() {
		It("prints one row per monitor with its connector and identity", func(ctx SpecContext) {
			Expect(run(ctx, "show")).To(Succeed())
			Expect(out()).To(ContainSubstring("CONNECTOR"))
			Expect(out()).To(MatchRegexp(`DP-9\s+DEL\s+DELL U2413\s+SER-F\s+9600,0`))
			Expect(display.CloseCallCount()).To(Equal(1))
		})

		It("lists a switched-off monitor with the position off", func(ctx SpecContext) {
			display.CurrentStateReturns(layouttest.Disabled(layouttest.GridState(), "DP-9"), nil)
			Expect(run(ctx, "show")).To(Succeed())
			Expect(out()).To(MatchRegexp(`DP-9\s+DEL\s+DELL U2413\s+SER-F\s+off`))
		})
	})

	Describe("save", func() {
		It("writes the current layout and shows what it captured", func(ctx SpecContext) {
			display.CurrentStateReturns(layouttest.GridState(), nil)
			Expect(run(ctx, "save")).To(Succeed())
			Expect(out()).To(ContainSubstring("saved 6 monitors to " + path))
			Expect(out()).To(MatchRegexp(`DP-9\s+DEL\s+DELL U2413\s+SER-F\s+3840,1200`))
			Expect(out()).To(ContainSubstring("Settings"))

			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			var l layout.Layout
			Expect(json.Unmarshal(data, &l)).To(Succeed())
			Expect(l).To(Equal(layouttest.GridLayout()))
		})

		It("leaves no temporary file behind", func(ctx SpecContext) {
			Expect(run(ctx, "save")).To(Succeed())
			entries, err := os.ReadDir(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1))
			Expect(entries[0].Name()).To(Equal("layout.json"))
		})

		It("refuses to overwrite without --force", func(ctx SpecContext) {
			Expect(os.WriteFile(path, []byte("{}"), 0o600)).To(Succeed())
			Expect(run(ctx, "save")).To(MatchError(cli.ErrLayoutExists))
			Expect(run(ctx, "save", "--force")).To(Succeed())
		})

		It("replaces the target of a symlinked layout file", func(ctx SpecContext) {
			real := filepath.Join(dir, "real.json")
			Expect(os.WriteFile(real, []byte("{}"), 0o600)).To(Succeed())
			Expect(os.Symlink(real, path)).To(Succeed())
			Expect(run(ctx, "save", "--force")).To(Succeed())
			info, err := os.Lstat(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode()&os.ModeSymlink).NotTo(BeZero(), "the symlink survives")
			data, err := os.ReadFile(real)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("SER-A"))
		})

		It("warns when two captured monitors share an identity", func(ctx SpecContext) {
			s := layouttest.GridState()
			s.Monitors[1].ID.Serial = "SER-A"
			display.CurrentStateReturns(s, nil)
			Expect(run(ctx, "save")).To(Succeed())
			Expect(string(stderr.Contents())).To(ContainSubstring("share identity"))
		})
	})

	Describe("fix", func() {
		It("names the layout path and does not connect when there is no file", func(ctx SpecContext) {
			connect = func(context.Context) (cli.Display, error) {
				Fail("connect called without a layout")
				return nil, nil
			}
			err := run(ctx, "fix")
			Expect(err).To(MatchError(ContainSubstring(path)))
			Expect(err).To(MatchError(ContainSubstring("save")))
		})

		It("applies the saved layout", func(ctx SpecContext) {
			writeGrid(path)
			Expect(run(ctx, "fix")).To(Succeed())
			Expect(display.ApplyCallCount()).To(Equal(1))
			Expect(out()).To(ContainSubstring("applied layout to 6 monitors"))
		})

		It("reports when nothing needs doing", func(ctx SpecContext) {
			writeGrid(path)
			display.CurrentStateReturns(layouttest.GridState(), nil)
			Expect(run(ctx, "fix")).To(Succeed())
			Expect(display.ApplyCallCount()).To(BeZero())
			Expect(out()).To(ContainSubstring("layout already pinned"))
		})

		It("prints current and target positions and the notes without applying on --dry-run", func(ctx SpecContext) {
			l := layouttest.GridLayout()
			l.Monitors = l.Monitors[:5]
			writeLayout(path, l)
			Expect(run(ctx, "fix", "--dry-run")).To(Succeed())
			Expect(display.VerifyCallCount()).To(Equal(1))
			Expect(display.ApplyCallCount()).To(BeZero())
			Expect(out()).To(ContainSubstring("would apply"))
			Expect(out()).To(MatchRegexp(`CONNECTOR\s+CURRENT\s+TARGET`))
			Expect(out()).To(MatchRegexp(`DP-20\s+7680,0\s+1920,1200`))
			Expect(out()).To(ContainSubstring("unknown: DP-9"))
		})

		It("prints the table on --dry-run even when already pinned", func(ctx SpecContext) {
			writeGrid(path)
			display.CurrentStateReturns(layouttest.GridState(), nil)
			Expect(run(ctx, "fix", "--dry-run")).To(Succeed())
			Expect(display.VerifyCallCount()).To(BeZero(), "nothing differs, so nothing is verified")
			Expect(display.ApplyCallCount()).To(BeZero())
			Expect(out()).To(ContainSubstring("layout already pinned; would apply:"))
			Expect(out()).To(MatchRegexp(`DP-9\s+3840,1200\s+3840,1200`))
		})
	})

	Describe("watch", func() {
		It("fixes at startup and returns when canceled", func(ctx SpecContext) {
			writeGrid(path)
			events := make(chan struct{})
			display.SubscribeReturns(events, nil)
			wctx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- run(wctx, "watch", "--debounce", "10ms") }()
			Eventually(display.ApplyCallCount).WithTimeout(time.Second).WithPolling(time.Millisecond).Should(Equal(1))
			cancel()
			Eventually(done).WithTimeout(time.Second).Should(Receive(BeNil()))
		})

		It("subscribes, tries a fix, and keeps running without a layout file", func(ctx SpecContext) {
			events := make(chan struct{})
			display.SubscribeReturns(events, nil)
			wctx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- run(wctx, "watch", "--debounce", "10ms") }()
			Eventually(display.SubscribeCallCount).WithTimeout(time.Second).WithPolling(time.Millisecond).Should(Equal(1))
			Eventually(stderr).WithTimeout(time.Second).WithPolling(time.Millisecond).Should(gbytes.Say("fix failed.*" + regexp.QuoteMeta(path)))
			Consistently(done).WithTimeout(50 * time.Millisecond).ShouldNot(Receive())
			cancel()
			Eventually(done).WithTimeout(time.Second).Should(Receive(BeNil()))
		})
	})
})

func writeGrid(path string) {
	GinkgoHelper()
	writeLayout(path, layouttest.GridLayout())
}

func writeLayout(path string, l layout.Layout) {
	GinkgoHelper()
	data, err := json.Marshal(l)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(path, data, 0o600)).To(Succeed())
}
