package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// info prints an informational message to the command's stderr. Suppressed
// when --quiet is set. Use for status updates that are nice-to-have but not
// the command's primary output ("removed abc123", "no boxes found", etc.).
//
// info writes to stderr by convention: stdout is reserved for the command's
// structured output (table rows, JSON, dry-run shell). Tools piping
// `agentbox ls --json | jq` should not have to filter status chatter.
func info(cmd *cobra.Command, format string, args ...any) {
	if global.Quiet {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
}

// result prints the command's primary output to stdout. Always printed,
// regardless of --quiet. JSON, NDJSON, table rows, dry-run shell, and the
// output of `config show` all flow through result (or directly through
// cmd.OutOrStdout() — result is for one-line cases).
func result(cmd *cobra.Command, format string, args ...any) {
	fmt.Fprintf(cmd.OutOrStdout(), format+"\n", args...)
}

// fInfo is the io.Writer-style escape hatch for code paths that need a
// writer (e.g. passing to a builder). Returns either the command's stderr
// or io.Discard depending on --quiet. Caller is responsible for using
// stderr-appropriate framing.
func fInfo(cmd *cobra.Command) io.Writer {
	if global.Quiet {
		return io.Discard
	}
	return cmd.ErrOrStderr()
}
