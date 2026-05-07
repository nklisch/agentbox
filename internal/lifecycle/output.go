package lifecycle

import (
	"fmt"
	"io"
)

// info writes an informational message to l.Stderr (not Stdout — primary
// output goes there). Suppressed when l.Quiet is true. Mirrors cli.info but
// for code paths inside lifecycle that don't have a *cobra.Command.
func (l *Lifecycle) info(format string, args ...any) {
	if l.Quiet {
		return
	}
	fmt.Fprintf(l.Stderr, format+"\n", args...)
}

// warn writes a warning to l.Stderr regardless of l.Quiet. --quiet suppresses
// status chatter, not warnings about partial failures.
func (l *Lifecycle) warn(format string, args ...any) {
	fmt.Fprintf(l.Stderr, format+"\n", args...)
}

// stderrOf returns l.Stderr if non-nil, else io.Discard. Used for handing a
// writer to lower-level helpers that always need a non-nil writer.
func (l *Lifecycle) stderrOf() io.Writer {
	if l.Stderr == nil {
		return io.Discard
	}
	return l.Stderr
}
