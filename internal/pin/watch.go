package pin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
)

// ErrDisconnected reports that the display subscription ended while
// watching, which happens when the bus connection closes.
var ErrDisconnected = errors.New("display connection closed")

// Watch applies the layout now and again after each burst of display
// signals that follows a hardware change, until ctx ends or the
// subscription closes. A burst is any run of signals less than debounce
// apart. Mutter bumps its configuration serial only when it re-reads the
// hardware, never when a configuration is applied, so a signal whose
// state carries the serial of the last fix is a deliberate change by
// Settings, gdctl, or this tool, and is left alone.
func (p *Pinner) Watch(ctx context.Context, debounce time.Duration) error {
	events, err := p.Display.Subscribe(ctx)
	if err != nil {
		return fmt.Errorf("watching for monitor changes: %w", err)
	}
	last := p.fixAndLog(ctx, nil, nil, nil)

	timer := time.NewTimer(debounce)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-events:
			if !ok {
				select {
				case <-ctx.Done():
					return nil
				default:
					return ErrDisconnected
				}
			}
			slog.DebugContext(ctx, "display signal received", slog.Duration("debounce", debounce))
			timer.Reset(debounce)
		case <-timer.C:
			if last == nil {
				last = p.fixAndLog(ctx, nil, nil, nil)
				continue
			}
			changed, added, removed := p.hardwareChanged(ctx, *last)
			if !changed {
				continue
			}
			last = p.fixAndLog(ctx, last, added, removed)
		}
	}
}

// hardwareChanged reads the current state and reports whether mutter has
// re-read the hardware since last, with the connectors that came and
// went. A read that fails counts as a change, so the fix's own retries
// take over rather than the event being dropped.
func (p *Pinner) hardwareChanged(ctx context.Context, last layout.State) (changed bool, added, removed []string) {
	state, err := p.Display.CurrentState(ctx)
	if err != nil {
		slog.WarnContext(ctx, "reading display state for the serial check", slog.Any("error", err))
		return true, nil, nil
	}
	if state.Serial == last.Serial {
		slog.DebugContext(ctx, "no hardware change, leaving layout alone", slog.Uint64("serial", uint64(state.Serial)))
		return false, nil, nil
	}
	added, removed = connectorDiff(last, state)
	slog.DebugContext(ctx, "hardware re-read",
		slog.Uint64("old_serial", uint64(last.Serial)), slog.Uint64("new_serial", uint64(state.Serial)),
		slog.Any("added", added), slog.Any("removed", removed))
	return true, added, removed
}

// fixAndLog runs Fix for the watch loop, where no failure is fatal, and
// returns the state it saw, or prev when it saw none. A repair after a
// hardware change logs the connectors that came and went; a repair with
// neither is the tell for a mutter that bumps its serial on every apply.
func (p *Pinner) fixAndLog(ctx context.Context, prev *layout.State, added, removed []string) *layout.State {
	res, err := p.Fix(ctx, false)
	if ctx.Err() != nil {
		return prev
	}
	switch {
	case err == nil && res.Applied:
		slog.InfoContext(ctx, "layout repaired", slog.Any("added", added), slog.Any("removed", removed))
	case errors.Is(err, ErrRejected):
		slog.WarnContext(ctx, "leaving layout as mutter set it", slog.Any("error", err))
	case err != nil:
		slog.ErrorContext(ctx, "fix failed", slog.Any("error", err))
	}
	if res.State.Serial != 0 {
		return &res.State
	}
	return prev
}

func connectorDiff(before, after layout.State) (added, removed []string) {
	was := make(map[string]bool, len(before.Monitors))
	for _, m := range before.Monitors {
		was[m.Connector] = true
	}
	is := make(map[string]bool, len(after.Monitors))
	for _, m := range after.Monitors {
		is[m.Connector] = true
		if !was[m.Connector] {
			added = append(added, m.Connector)
		}
	}
	for _, m := range before.Monitors {
		if !is[m.Connector] {
			removed = append(removed, m.Connector)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	return added, removed
}
