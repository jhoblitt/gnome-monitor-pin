// Package cli builds the gnome-monitor-pin command tree.
//
// Managed by go-conventions (references/cli.md owns the cobra and viper
// contract, references/logging.md the logger). Configuration precedence is
// flags, then GNOME_MONITOR_PIN_* environment variables, then the config file,
// then defaults, and every value is read back through viper.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jhoblitt/gnome-monitor-pin/internal/displayconfig"
	"github.com/jhoblitt/gnome-monitor-pin/internal/pin"
	"github.com/jhoblitt/gnome-monitor-pin/internal/version"
)

//go:generate go tool counterfeiter -generate

// Display is the display connection a command uses. displayconfig.Client
// is the real one.
//
//counterfeiter:generate . Display
type Display interface {
	pin.Display
	Close() error
}

// Connect opens a Display. Run uses displayconfig.Connect; specs substitute
// a fake through RunWith.
type Connect func(ctx context.Context) (Display, error)

// Run executes the command tree against args and returns the first error.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return RunWith(ctx, args, stdin, stdout, stderr, connectDisplay)
}

// RunWith is Run with the display connection supplied by the caller.
func RunWith(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, connect Connect) error {
	cmd := newRootCmd(stdin, stdout, stderr, connect)
	cmd.SetArgs(args)
	return cmd.ExecuteContext(ctx)
}

func connectDisplay(ctx context.Context) (Display, error) {
	c, err := displayconfig.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func newRootCmd(stdin io.Reader, stdout, stderr io.Writer, connect Connect) *cobra.Command {
	v := viper.New()
	cmd := &cobra.Command{
		Use:           "gnome-monitor-pin",
		Short:         "Pin a GNOME multi-monitor layout across monitor hotplugs",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return configure(cmd, v, stderr)
		},
	}
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	pf := cmd.PersistentFlags()
	pf.String("config", "", "config file to read after flags and environment")
	pf.String("log-level", "info", "log level: debug, info, warn, or error (GNOME_MONITOR_PIN_LOG_LEVEL)")
	pf.String("log-format", "json", "log format: json or text (GNOME_MONITOR_PIN_LOG_FORMAT)")
	pf.String("layout", defaultLayoutPath(), "layout file (GNOME_MONITOR_PIN_LAYOUT)")

	cmd.AddCommand(
		newShowCmd(connect),
		newSaveCmd(v, connect),
		newFixCmd(v, connect),
		newWatchCmd(v, connect),
	)
	return cmd
}

func defaultLayoutPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "layout.json"
	}
	return filepath.Join(dir, "gnome-monitor-pin", "layout.json")
}

// closeDisplay closes d at the end of a command; a failure to close has no
// recovery, so it is logged rather than returned.
func closeDisplay(ctx context.Context, d Display) {
	if err := d.Close(); err != nil {
		slog.WarnContext(ctx, "closing display connection", slog.Any("error", err))
	}
}

// configure binds the command's flags and the environment into v, reads the
// config file when one is named, and installs the default logger. It runs
// before every command, so a subcommand's own flags are bound as well.
func configure(cmd *cobra.Command, v *viper.Viper, stderr io.Writer) error {
	v.SetEnvPrefix("GNOME_MONITOR_PIN")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()
	if err := v.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("binding flags: %w", err)
	}
	if cfg := v.GetString("config"); cfg != "" {
		v.SetConfigFile(cfg)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("reading config %s: %w", cfg, err)
		}
	}
	return setupLogging(v, stderr)
}

func setupLogging(v *viper.Viper, w io.Writer) error {
	var level slog.Level
	if err := level.UnmarshalText([]byte(v.GetString("log-level"))); err != nil {
		return fmt.Errorf("parsing log level: %w", err)
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch format := v.GetString("log-format"); format {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	case "text":
		handler = slog.NewTextHandler(w, opts)
	default:
		return fmt.Errorf("unknown log format %q", format)
	}
	slog.SetDefault(slog.New(handler))
	return nil
}
