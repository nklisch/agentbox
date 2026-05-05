package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/exitcode"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or edit configuration",
	}
	cmd.AddCommand(newConfigShowCmd(), newConfigEditCmd(), newConfigPathCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	var effective bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the merged configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := loadConfig()
			if err != nil {
				return err
			}
			cfg := res.Config
			// --effective applies the same global-flag overrides that
			// loadConfig already applied, so for P1 there's nothing extra
			// to do here. Future flags (network, kits) would apply here.
			_ = effective

			if global.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(cfg)
			}
			return toml.NewEncoder(cmd.OutOrStdout()).Encode(cfg)
		},
	}
	cmd.Flags().BoolVar(&effective, "effective", false, "include CLI-flag overrides as if a `run` were happening now")
	return cmd
}

func newConfigPathCmd() *cobra.Command {
	var (
		globalFlag  bool
		projectFlag bool
	)
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print the config file path and exit",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := config.DefaultPaths()
			if err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}
			if global.ConfigPath != "" {
				paths.Global = global.ConfigPath
			}
			target := paths.Global
			if projectFlag {
				target = paths.Project
			}
			fmt.Fprintln(cmd.OutOrStdout(), target)
			return nil
		},
	}
	cmd.Flags().BoolVar(&globalFlag, "global", false, "print global config path (default)")
	cmd.Flags().BoolVar(&projectFlag, "project", false, "print project config path")
	cmd.MarkFlagsMutuallyExclusive("global", "project")
	return cmd
}

func newConfigEditCmd() *cobra.Command {
	var (
		globalFlag  bool
		projectFlag bool
	)
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Open $EDITOR on the config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := config.DefaultPaths()
			if err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}
			if global.ConfigPath != "" {
				paths.Global = global.ConfigPath
			}
			target := paths.Global
			if projectFlag {
				target = paths.Project
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
			}
			if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
				if err := os.WriteFile(target, []byte(""), 0o600); err != nil {
					return exitcode.Wrap(exitcode.Generic, err)
				}
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			ec := exec.Command(editor, target)
			ec.Stdin = os.Stdin
			ec.Stdout = cmd.OutOrStdout()
			ec.Stderr = cmd.ErrOrStderr()
			if err := ec.Run(); err != nil {
				return exitcode.Wrap(exitcode.Generic, fmt.Errorf("editor %s: %w", editor, err))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&globalFlag, "global", false, "edit global config (default)")
	cmd.Flags().BoolVar(&projectFlag, "project", false, "edit project config")
	cmd.MarkFlagsMutuallyExclusive("global", "project")
	return cmd
}
