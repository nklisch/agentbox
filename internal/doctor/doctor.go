package doctor

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/network"
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

// Run executes all checks and returns the aggregated result.
func Run(cfg config.Config) Result {
	checks := []Check{
		runtimeCheck(cfg.Runtime),
		stateDirCheck(),
		iptablesCheck(),
		ipsetCheck(),
		sudoCheck(),
		corednsImageCheck(cfg.Runtime),
	}
	return Result{Checks: checks}
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

// iptablesCheck verifies that iptables is installed and findable on PATH.
// On macOS this check is skipped (agentbox degrades to DNS-only mode there).
func iptablesCheck() Check {
	const name = "iptables"
	if runtime.GOOS == "darwin" {
		return Check{Name: name, Status: StatusOK, Message: "skipped on macOS (DNS-only mode)"}
	}
	path, err := exec.LookPath("iptables")
	if err != nil {
		return Check{Name: name, Status: StatusFail,
			Message: "iptables not found in PATH; install iptables (safe/allowlist mode requires it)"}
	}
	return Check{Name: name, Status: StatusOK, Message: "found at " + path}
}

// ipsetCheck verifies that ipset is installed and findable on PATH.
// On macOS this check is skipped.
func ipsetCheck() Check {
	const name = "ipset"
	if runtime.GOOS == "darwin" {
		return Check{Name: name, Status: StatusOK, Message: "skipped on macOS (DNS-only mode)"}
	}
	path, err := exec.LookPath("ipset")
	if err != nil {
		return Check{Name: name, Status: StatusFail,
			Message: "ipset not found in PATH; install ipset (safe/allowlist IP-level filtering requires it)"}
	}
	return Check{Name: name, Status: StatusOK, Message: "found at " + path}
}

// sudoCheck verifies that the current user has passwordless sudo for iptables.
// safe mode with block_direct_ip=true (default) and allowlist mode require this.
// On macOS this check is skipped.
func sudoCheck() Check {
	const name = "sudo-iptables"
	if runtime.GOOS == "darwin" {
		return Check{Name: name, Status: StatusOK, Message: "skipped on macOS (DNS-only mode)"}
	}

	// Check whether iptables is on PATH first; sudo check is moot without it.
	if _, err := exec.LookPath("iptables"); err != nil {
		return Check{Name: name, Status: StatusFail,
			Message: "iptables not found; install iptables before configuring sudo"}
	}

	// `sudo -n iptables -L` exits 0 when passwordless sudo is configured, non-zero otherwise.
	// Use -n (non-interactive) so it never blocks waiting for a password.
	cmd := exec.Command("sudo", "-n", "iptables", "-L")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return Check{Name: name, Status: StatusWarn,
			Message: fmt.Sprintf("passwordless sudo for iptables not configured: %s — "+
				"add 'ALL ALL=(root) NOPASSWD: /usr/sbin/iptables, /usr/sbin/ipset' to sudoers "+
				"(or use visudo). Required for safe mode with block_direct_ip=true and allowlist mode.", msg)}
	}
	return Check{Name: name, Status: StatusOK, Message: "passwordless sudo for iptables confirmed"}
}

// corednsImageCheck verifies that the pinned CoreDNS image is already pulled.
// If absent, it warns with the pull command (not a hard failure — the image
// will be pulled lazily on first `agentbox run` with safe/allowlist mode).
func corednsImageCheck(runtimeBin string) Check {
	const name = "coredns-image"
	if runtimeBin == "" {
		runtimeBin = "podman"
	}
	cmd := exec.Command(runtimeBin, "image", "exists", network.CoreDNSImage)
	if err := cmd.Run(); err != nil {
		return Check{Name: name, Status: StatusWarn,
			Message: fmt.Sprintf("%s not pulled yet; run: %s pull %s",
				network.CoreDNSImage, runtimeBin, network.CoreDNSImage)}
	}
	return Check{Name: name, Status: StatusOK, Message: network.CoreDNSImage + " present"}
}
