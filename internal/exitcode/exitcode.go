package exitcode

import "fmt"

// Exit codes per docs/CLI.md "Exit codes".
const (
	OK           = 0
	Generic      = 1
	InvalidArgs  = 2
	Runtime      = 3
	NotFound     = 4
	KitBuild     = 5
	Network      = 6
	MountMissing = 7
	Interrupted  = 130
)

// Err carries an exit code with an optional underlying error. Returned from
// cobra RunE; main.go unwraps it to set the process exit code.
type Err struct {
	Code int
	Wrap error
}

func (e *Err) Error() string {
	if e.Wrap == nil {
		return fmt.Sprintf("exit code %d", e.Code)
	}
	return e.Wrap.Error()
}

func (e *Err) Unwrap() error { return e.Wrap }

// Wrap returns an *Err with the given code and underlying error. Returns nil
// if err is nil so callers can chain.
func Wrap(code int, err error) error {
	if err == nil {
		return nil
	}
	return &Err{Code: code, Wrap: err}
}

// New creates an *Err with a fresh fmt.Errorf message.
func New(code int, format string, a ...any) error {
	return &Err{Code: code, Wrap: fmt.Errorf(format, a...)}
}
