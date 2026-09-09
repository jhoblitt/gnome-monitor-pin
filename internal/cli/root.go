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
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jhoblitt/gnome-monitor-pin/internal/version"
)

// Run executes the command tree against args and returns the first error.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := newRootCmd(stdin, stdout, stderr)
	cmd.SetArgs(args)
	return cmd.ExecuteContext(ctx)
}

func newRootCmd(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			slog.InfoContext(cmd.Context(), "greeting", slog.String("name", v.GetString("name")))
			fmt.Fprintf(cmd.OutOrStdout(), "hello, %s\n", v.GetString("name"))
			return nil
		},
	}
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	pf := cmd.PersistentFlags()
	pf.String("config", "", "config file to read after flags and environment")
	pf.String("log-level", "info", "log level: debug, info, warn, or error (GNOME_MONITOR_PIN_LOG_LEVEL)")
	pf.String("log-format", "json", "log format: json or text (GNOME_MONITOR_PIN_LOG_FORMAT)")
	cmd.Flags().String("name", "world", "who to greet (GNOME_MONITOR_PIN_NAME)")

	return cmd
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
