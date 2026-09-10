// Package pin holds a desktop to a pinned layout: it derives the target
// for mutter's current state and applies it when the two differ.
package pin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

//go:generate go tool counterfeiter -generate

// Display is what pinning needs from mutter's display configuration.
//
//counterfeiter:generate . Display
type Display interface {
	CurrentState(ctx context.Context) (layout.State, error)
	Verify(ctx context.Context, t layout.Target) error
	Apply(ctx context.Context, t layout.Target) error
	Subscribe(ctx context.Context) (<-chan struct{}, error)
}

// ErrRejected reports that mutter refused the derived configuration; the
// wrapped error carries mutter's reason.
var ErrRejected = errors.New("configuration rejected by mutter")

// transient marks a failure worth retrying from a fresh state.
type transient struct{ err error }

func (t transient) Error() string { return t.err.Error() }
func (t transient) Unwrap() error { return t.err }

// Pinner holds a desktop to the layout Load returns.
type Pinner struct {
	Display Display
	// Load returns the pinned layout; it runs on every Fix so a rewritten
	// layout file takes effect without a restart.
	Load func() (layout.Layout, error)
	// Retries is the number of attempts at a transient failure; zero means 3.
	Retries int
	// RetryDelay is the pause between attempts; zero means 500ms.
	RetryDelay time.Duration
}

// Result is what one Fix saw and did.
type Result struct {
	// State is what mutter showed when the target was derived; it is
	// filled whenever a state was read, even when the fix then failed.
	State  layout.State
	Target layout.Target
	// Changed reports that the target differed from what mutter showed.
	Changed bool
	// Applied reports that the target was applied.
	Applied bool
}

func (p *Pinner) attempts() int { return cmp.Or(p.Retries, 3) }

func (p *Pinner) delay() time.Duration { return cmp.Or(p.RetryDelay, 500*time.Millisecond) }

// Fix loads the layout, derives the target for the current state, and
// applies it when the two differ. With dryRun it stops after mutter
// verifies the target. A transient failure (reading the state, a call
// that timed out, a state that changed underneath the verify, a failed
// apply) is retried from a fresh state; a rejection by mutter, a layout
// that cannot be loaded, and a layout that fits no monitor are not.
func (p *Pinner) Fix(ctx context.Context, dryRun bool) (Result, error) {
	var err error
	for attempt := range p.attempts() {
		if attempt > 0 {
			slog.WarnContext(ctx, "retrying after transient failure", slog.Int("attempt", attempt+1), slog.Any("error", err))
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(p.delay()):
			}
		}
		var res Result
		res, err = p.fixOnce(ctx, dryRun)
		var t transient
		if err == nil || !errors.As(err, &t) {
			return res, err
		}
	}
	return Result{}, fmt.Errorf("giving up after %d attempts: %w", p.attempts(), err)
}

func (p *Pinner) fixOnce(ctx context.Context, dryRun bool) (Result, error) {
	l, err := p.Load()
	if err != nil {
		return Result{}, fmt.Errorf("loading layout: %w", err)
	}
	state, err := p.Display.CurrentState(ctx)
	if err != nil {
		return Result{}, transient{fmt.Errorf("reading display state: %w", err)}
	}
	target, err := layout.Derive(l, state)
	if err != nil {
		return Result{State: state}, fmt.Errorf("deriving layout: %w", err)
	}
	res := Result{State: state, Target: target}
	if layout.Equal(target, state) {
		slog.DebugContext(ctx, "layout already pinned", slog.Uint64("serial", uint64(state.Serial)))
		return res, nil
	}
	res.Changed = true
	logDerivation(ctx, target)
	if err := p.Display.Verify(ctx, target); err != nil {
		return res, p.classifyVerify(ctx, target, err)
	}
	if dryRun {
		return res, nil
	}
	if err := p.Display.Apply(ctx, target); err != nil {
		return res, transient{fmt.Errorf("applying layout: %w", err)}
	}
	res.Applied = true
	slog.InfoContext(ctx, "layout applied", slog.Int("monitors", len(target.Cells)), slog.Uint64("serial", uint64(target.Serial)))
	return res, nil
}

// classifyVerify decides what a failed verify means: a canceled context
// is returned as is; a timeout, a state that changed underneath the
// verify, and a re-read that failed are transient; anything else is
// mutter rejecting the configuration, which no retry can change.
func (p *Pinner) classifyVerify(ctx context.Context, target layout.Target, err error) error {
	if ctx.Err() != nil {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return transient{fmt.Errorf("verifying layout: %w", err)}
	}
	fresh, rerr := p.Display.CurrentState(ctx)
	if rerr != nil {
		return transient{fmt.Errorf("re-reading display state after a failed verify: %w", errors.Join(err, rerr))}
	}
	if fresh.Serial != target.Serial {
		return transient{fmt.Errorf("display state changed during verify: %w", err)}
	}
	return fmt.Errorf("%w: %w", ErrRejected, err)
}

// logDerivation records what Derive left out or improvised, once per
// layout that is about to be verified.
func logDerivation(ctx context.Context, target layout.Target) {
	for _, id := range target.Missing {
		slog.InfoContext(ctx, "pinned monitor not connected", slog.String("product", id.Product), slog.String("serial", id.Serial))
	}
	for _, c := range target.Unknown {
		slog.WarnContext(ctx, "monitor not in layout, placed to the right", slog.String("connector", c))
	}
	for _, w := range target.Warnings {
		slog.WarnContext(ctx, "layout adjusted", slog.String("detail", w))
	}
}
