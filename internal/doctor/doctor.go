package doctor

import (
	"fmt"
	"os/exec"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/state"
)

// Status is the result of a single check.
type Status string

const (
	StatusOK   Status = "OK"
	StatusWarn Status = "WARN"
	StatusFail Status = "FAIL"
)

// Check is a single doctor check.
type Check struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message"`
}

// Result aggregates all checks and a summary.
type Result struct {
	Checks []Check `json:"checks"`
}

// AnyFail reports whether any check has Status == FAIL.
func (r Result) AnyFail() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

// Run executes all P1 checks and returns the aggregated result.
func Run(cfg config.Config) Result {
	return Result{
		Checks: []Check{
			runtimeCheck(cfg.Runtime),
			stateDirCheck(),
		},
	}
}

func runtimeCheck(bin string) Check {
	if bin == "" {
		return Check{Name: "runtime", Status: StatusFail, Message: "config.runtime is empty"}
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return Check{
			Name:    "runtime",
			Status:  StatusFail,
			Message: fmt.Sprintf("%s not found in PATH", bin),
		}
	}
	return Check{
		Name:    "runtime",
		Status:  StatusOK,
		Message: fmt.Sprintf("%s found at %s", bin, path),
	}
}

func stateDirCheck() Check {
	dir, err := state.Dir()
	if err != nil {
		return Check{Name: "state-dir", Status: StatusFail, Message: err.Error()}
	}
	if err := state.EnsureDir(dir); err != nil {
		return Check{Name: "state-dir", Status: StatusFail, Message: fmt.Sprintf("create %s: %v", dir, err)}
	}
	if !state.IsWritable(dir) {
		return Check{Name: "state-dir", Status: StatusFail, Message: fmt.Sprintf("%s not writable", dir)}
	}
	return Check{Name: "state-dir", Status: StatusOK, Message: dir + " writable"}
}
