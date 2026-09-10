package cli

import (
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jhoblitt/gnome-monitor-pin/internal/layout"
	"github.com/jhoblitt/gnome-monitor-pin/internal/pin"
)

func newWatchCmd(v *viper.Viper, connect Connect) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Apply the pinned layout now and after every hardware change",
		Long: `Apply the pinned layout now and after every hardware change.

The layout file is read again for every apply, so "save --force" takes
effect without a restart. A change made in Settings or with gdctl is left
alone; only a hotplug, re-enumeration, or resume triggers an apply.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			path := v.GetString("layout")
			slog.InfoContext(ctx, "watching", slog.String("layout", path), slog.Duration("debounce", v.GetDuration("debounce")))
			d, err := connect(ctx)
			if err != nil {
				return err
			}
			defer closeDisplay(ctx, d)
			p := &pin.Pinner{Display: d, Load: func() (layout.Layout, error) { return readLayout(path) }}
			return p.Watch(ctx, v.GetDuration("debounce"))
		},
	}
	cmd.Flags().Duration("debounce", time.Second, "quiet time after a display signal before applying (GNOME_MONITOR_PIN_DEBOUNCE)")
	return cmd
}
