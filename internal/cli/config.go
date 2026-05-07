package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/state"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or edit configuration",
	}
	cmd.AddCommand(
		newConfigShowCmd(),
		newConfigEditCmd(),
		newConfigPathCmd(),
		newConfigSetCmd(),
		newConfigUnsetCmd(),
		newConfigNetworkCmd(),
		newConfigContainersCmd(),
		newConfigKitsCmd(),
	)
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the merged configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := loadConfig()
			if err != nil {
				return err
			}
			cfg := res.Config
			if global.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(cfg)
			}
			return toml.NewEncoder(cmd.OutOrStdout()).Encode(cfg)
		},
	}
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
			if err := state.EnsureFile(target); err != nil {
				return exitcode.Wrap(exitcode.Generic, err)
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

// ── Write commands ────────────────────────────────────────────────────────────

// editTarget resolves the target file (global or project), runs the
// read→mutate→encode→validate pipeline, and writes atomically. In --dry-run
// mode it prints the proposed body and exits without writing.
func editTarget(cmd *cobra.Command, useProject bool, mutate func(map[string]any) error) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	if global.ConfigPath != "" {
		paths.Global = global.ConfigPath
	}
	target := paths.Global
	if useProject {
		if paths.Project == "" {
			return exitcode.New(exitcode.InvalidArgs, "no project config path (run from a project directory)")
		}
		target = paths.Project
	}

	body, err := config.EditFile(target, mutate)
	if err != nil {
		return exitcode.Wrap(exitcode.InvalidArgs, err)
	}

	// Validate the merged config with the proposed body before touching disk.
	proposed, err := config.LoadProposed(paths, target, body)
	if err != nil {
		return exitcode.Wrap(exitcode.InvalidArgs, fmt.Errorf("config invalid after mutation: %w", err))
	}
	if err := proposed.Validate(); err != nil {
		return exitcode.Wrap(exitcode.InvalidArgs, fmt.Errorf("config invalid after mutation: %w", err))
	}

	if global.DryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "# would write to %s:\n%s", target, string(body))
		return nil
	}

	if err := config.WriteAtomic(target, body); err != nil {
		return exitcode.Wrap(exitcode.Generic, err)
	}
	return nil
}

// globalProjectFlags attaches --global / --project (mutually exclusive) to cmd
// and returns a pointer to the project flag's value.
func globalProjectFlags(cmd *cobra.Command) *bool {
	var globalFlag bool
	var projectFlag bool
	cmd.Flags().BoolVar(&globalFlag, "global", false, "edit global config (default)")
	cmd.Flags().BoolVar(&projectFlag, "project", false, "edit project config (.agentbox.toml)")
	cmd.MarkFlagsMutuallyExclusive("global", "project")
	return &projectFlag
}

// parseBoolFriendly accepts on/off/true/false/enable/disable/yes/no
// (case-insensitive). Used by the `containers` convenience command.
func parseBoolFriendly(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true", "on", "enable", "enabled", "yes", "1":
		return true, nil
	case "false", "off", "disable", "disabled", "no", "0":
		return false, nil
	default:
		return false, fmt.Errorf("invalid value %q: expected on|off|true|false|enable|disable", s)
	}
}

// ── agentbox config set <key> <value> ────────────────────────────────────────

func newConfigSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config key by dotted path",
		Long: `Set a config key by dotted path. Scalar types (string, bool, int, float)
are detected automatically. List fields accept a comma-separated value
that overwrites the whole array (e.g. "polyglot,claude").

Examples:
  agentbox config set network.mode open
  agentbox config set network.safe.block_direct_ip false
  agentbox config set default_kits polyglot,claude
  agentbox config set resources.cpus 8
  agentbox config set --project network.mode allowlist`,
		Args: cobra.ExactArgs(2),
	}
	projectFlag := globalProjectFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		key, rawValue := args[0], args[1]
		return editTarget(cmd, *projectFlag, func(doc map[string]any) error {
			path, err := config.ParsePath(key)
			if err != nil {
				return err
			}
			kind := config.LookupKind(path)
			v, err := config.ParseValue(rawValue, kind)
			if err != nil {
				return fmt.Errorf("parse value: %w", err)
			}
			return config.Set(doc, path, v)
		})
	}
	return cmd
}

// ── agentbox config unset <key> ──────────────────────────────────────────────

func newConfigUnsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove a key from the config file by dotted path",
		Long: `Remove a key from the config file. The merged config falls back to the
schema default for the removed key. Idempotent if the key is already absent.

Empty ancestor sections are pruned automatically.

Example:
  agentbox config unset network.safe.block_direct_ip`,
		Args: cobra.ExactArgs(1),
	}
	projectFlag := globalProjectFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		key := args[0]
		return editTarget(cmd, *projectFlag, func(doc map[string]any) error {
			path, err := config.ParsePath(key)
			if err != nil {
				return err
			}
			config.Unset(doc, path) // idempotent; ignore "not found"
			return nil
		})
	}
	return cmd
}

// ── agentbox config network <mode> ───────────────────────────────────────────

func newConfigNetworkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network <off|safe|allowlist|open>",
		Short: "Set the network mode",
		Long: `Set network.mode. Equivalent to:
  agentbox config set network.mode <mode>

Modes:
  off       No network (loopback only)
  safe      Threat-feed DNS + direct-IP egress block (default)
  allowlist Explicit hostname allowlist only
  open      Default bridge, no filtering`,
		Args: cobra.ExactArgs(1),
	}
	projectFlag := globalProjectFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		mode := args[0]
		return editTarget(cmd, *projectFlag, func(doc map[string]any) error {
			// Let cfg.Validate() catch invalid modes — single source of truth.
			return config.Set(doc, []string{"network", "mode"}, mode)
		})
	}
	return cmd
}

// ── agentbox config containers <on|off> ──────────────────────────────────────

func newConfigContainersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "containers <on|off>",
		Short: "Toggle nested container support (containers.enable)",
		Long: `Enable or disable nested rootless podman inside boxes.
Accepts on|off|true|false|enable|disable|yes|no (case-insensitive).

Requires the 'containers' kit to be in your kit list. Equivalent to:
  agentbox config set containers.enable true|false`,
		Args: cobra.ExactArgs(1),
	}
	projectFlag := globalProjectFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		enable, err := parseBoolFriendly(args[0])
		if err != nil {
			return exitcode.Wrap(exitcode.InvalidArgs, err)
		}
		return editTarget(cmd, *projectFlag, func(doc map[string]any) error {
			return config.Set(doc, []string{"containers", "enable"}, enable)
		})
	}
	return cmd
}

// ── agentbox config kits add|remove <name> ───────────────────────────────────

func newConfigKitsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kits",
		Short: "Modify default_kits incrementally",
	}
	cmd.AddCommand(newConfigKitsAddCmd(), newConfigKitsRemoveCmd())
	return cmd
}

func newConfigKitsAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Append a kit to default_kits (idempotent)",
		Long: `Append a kit name to default_kits. Idempotent: adding a kit that's
already present is a no-op. If default_kits isn't in the target file yet,
it is seeded from the current merged value before appending.`,
		Args: cobra.ExactArgs(1),
	}
	projectFlag := globalProjectFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		// Seed from the merged config so `kits add` on a fresh file
		// doesn't silently discard the defaults from the other layer.
		res, err := loadConfig()
		if err != nil {
			return err
		}
		mergedKits := res.Config.DefaultKits

		return editTarget(cmd, *projectFlag, func(doc map[string]any) error {
			path := []string{"default_kits"}
			if _, ok := doc["default_kits"]; !ok {
				seed := make([]any, len(mergedKits))
				for i, k := range mergedKits {
					seed[i] = k
				}
				doc["default_kits"] = seed
			}
			return config.AppendUnique(doc, path, name)
		})
	}
	return cmd
}

func newConfigKitsRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a kit from default_kits (idempotent)",
		Long: `Remove the first occurrence of a kit name from default_kits.
Idempotent: removing a kit that isn't present is a no-op. If default_kits
isn't in the target file yet, it is seeded from the current merged value
before removing.`,
		Args: cobra.ExactArgs(1),
	}
	projectFlag := globalProjectFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[0]
		res, err := loadConfig()
		if err != nil {
			return err
		}
		mergedKits := res.Config.DefaultKits

		return editTarget(cmd, *projectFlag, func(doc map[string]any) error {
			path := []string{"default_kits"}
			if _, ok := doc["default_kits"]; !ok {
				seed := make([]any, len(mergedKits))
				for i, k := range mergedKits {
					seed[i] = k
				}
				doc["default_kits"] = seed
			}
			_, err := config.RemoveValue(doc, path, name)
			return err
		})
	}
	return cmd
}
