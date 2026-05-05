package doctor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/kits"
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
	Name    string       `json:"name"`
	Status  Status       `json:"status"`
	Message string       `json:"message"`
	Fix     func() error `json:"-"` // optional remediation; called when --fix is set
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

// ApplyFixes calls Fix() on every check that has one and is currently FAIL or
// WARN. Returns the list of check names whose Fix was attempted, plus the first
// error encountered (continues after errors so all fixes get a chance to run).
func (r Result) ApplyFixes() (attempted []string, firstErr error) {
	for _, c := range r.Checks {
		if c.Fix == nil {
			continue
		}
		if c.Status != StatusFail && c.Status != StatusWarn {
			continue
		}
		attempted = append(attempted, c.Name)
		if err := c.Fix(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return attempted, firstErr
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
		containersConfigCheck(cfg),
		podmanMachineCheck(),
		kitCacheHealthCheck(cfg.Runtime),
		mountSourcesCheck(cfg.Runtime),
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

// containersConfigCheck warns when the containers kit is in DefaultKits but
// Containers.Enable is false — the most common misconfiguration for nested
// rootless podman support.
func containersConfigCheck(cfg config.Config) Check {
	const name = "containers-config"
	hasKit := false
	for _, k := range cfg.DefaultKits {
		if k == "containers" {
			hasKit = true
			break
		}
	}
	if !hasKit {
		return Check{Name: name, Status: StatusOK,
			Message: "containers kit not in default_kits; nested-container support disabled by default"}
	}
	if cfg.Containers.Enable {
		return Check{Name: name, Status: StatusOK,
			Message: "containers kit + runtime.containers.enable=true; nested rootless ready"}
	}
	return Check{Name: name, Status: StatusWarn,
		Message: "containers kit is in default_kits but runtime.containers.enable=false; " +
			"nested docker/podman commands inside boxes will fail. Set [containers] enable=true " +
			"in ~/.config/agentbox/config.toml to grant the runtime privileges (requires understanding " +
			"the security trade-offs documented in SPEC.md)."}
}

// corednsImageCheck verifies that the pinned CoreDNS image is already pulled.
// If absent, it warns with the pull command (not a hard failure — the image
// will be pulled lazily on first `agentbox run` with safe/allowlist mode).
// When --fix is set, the Fix closure pulls the image automatically.
func corednsImageCheck(runtimeBin string) Check {
	const name = "coredns-image"
	if runtimeBin == "" {
		runtimeBin = "podman"
	}
	bin := runtimeBin // capture for closure
	cmd := exec.Command(runtimeBin, "image", "exists", network.CoreDNSImage)
	if err := cmd.Run(); err != nil {
		return Check{
			Name:    name,
			Status:  StatusWarn,
			Message: fmt.Sprintf("%s not pulled yet; run: %s pull %s", network.CoreDNSImage, runtimeBin, network.CoreDNSImage),
			Fix: func() error {
				return exec.Command(bin, "pull", network.CoreDNSImage).Run()
			},
		}
	}
	return Check{Name: name, Status: StatusOK, Message: network.CoreDNSImage + " present"}
}

// podmanMachineCheck verifies that a podman machine is running on macOS.
// On Linux, podman runs natively so no machine is needed — returns OK with
// a "skipped on Linux" message. Never returns FAIL on the wrong platform.
func podmanMachineCheck() Check {
	const name = "podman-machine"
	if runtime.GOOS != "darwin" {
		return Check{Name: name, Status: StatusOK, Message: "skipped on Linux"}
	}
	cmd := exec.Command("podman", "machine", "list", "--format", "{{.Running}}")
	out, err := cmd.Output()
	if err != nil {
		return Check{
			Name:    name,
			Status:  StatusFail,
			Message: "podman not installed or unable to list machines: " + err.Error(),
			Fix: func() error {
				// Try start first; if it fails because there's no machine, init first.
				if err := exec.Command("podman", "machine", "start").Run(); err == nil {
					return nil
				}
				// Ignore "already exists" errors from init.
				_ = exec.Command("podman", "machine", "init").Run()
				return exec.Command("podman", "machine", "start").Run()
			},
		}
	}
	// out contains one line per machine: "true" if running, "false" if stopped.
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "true" {
			return Check{Name: name, Status: StatusOK, Message: "podman machine running"}
		}
	}
	return Check{
		Name:    name,
		Status:  StatusWarn,
		Message: "no running podman machine; agentbox containers will fail to start. Run `podman machine start` (or `agentbox doctor --fix`).",
		Fix: func() error {
			return exec.Command("podman", "machine", "start").Run()
		},
	}
}

// kitCacheHealthCheck compares cache JSON entries against actual container
// images. Warns if the cache references images that no longer exist (user
// pruned externally) — `agentbox build` will rebuild on next run.
func kitCacheHealthCheck(runtimeBin string) Check {
	const name = "kit-cache"
	if runtimeBin == "" {
		runtimeBin = "podman"
	}
	cache, err := kits.NewCache()
	if err != nil {
		return Check{Name: name, Status: StatusFail,
			Message: "kit cache unreachable: " + err.Error()}
	}
	entries, err := cache.ListEntries()
	if err != nil {
		return Check{Name: name, Status: StatusWarn,
			Message: "kit cache list error: " + err.Error()}
	}
	if len(entries) == 0 {
		return Check{Name: name, Status: StatusOK,
			Message: "kit cache empty (no kits built yet)"}
	}
	var stale []string
	for _, e := range entries {
		cmd := exec.Command(runtimeBin, "image", "inspect", e.Tag)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if cmd.Run() != nil {
			stale = append(stale, e.Tag)
		}
	}
	if len(stale) == 0 {
		return Check{Name: name, Status: StatusOK,
			Message: fmt.Sprintf("%d cached kits, all images present", len(entries))}
	}
	return Check{Name: name, Status: StatusWarn,
		Message: fmt.Sprintf("%d cached kits reference %d missing images: %s. Run `agentbox build --no-cache <kit>` to rebuild.",
			len(entries), len(stale), strings.Join(stale, ", "))}
}

// mountSourcesCheck verifies that bind-mount sources for all agentbox-labeled
// containers still exist on the host. Warns if any source directory was deleted
// after the box was created.
func mountSourcesCheck(runtimeBin string) Check {
	const name = "mount-sources"
	if runtimeBin == "" {
		runtimeBin = "podman"
	}
	cmd := exec.Command(runtimeBin, "ps", "-a",
		"--filter", "label=agentbox.role=box",
		"--format", "{{.Names}}")
	out, err := cmd.Output()
	if err != nil {
		return Check{Name: name, Status: StatusOK,
			Message: "no running boxes (or runtime unreachable)"}
	}
	names := strings.Fields(strings.TrimSpace(string(out)))
	if len(names) == 0 {
		return Check{Name: name, Status: StatusOK, Message: "no running boxes"}
	}

	var missing []string
	for _, cname := range names {
		icmd := exec.Command(runtimeBin, "inspect", cname,
			"--format", "{{range .Mounts}}{{.Source}}\n{{end}}")
		iout, err := icmd.Output()
		if err != nil {
			continue
		}
		for _, src := range strings.Split(strings.TrimSpace(string(iout)), "\n") {
			if src == "" {
				continue
			}
			if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
				missing = append(missing, fmt.Sprintf("%s: %s", cname, src))
			}
		}
	}
	if len(missing) == 0 {
		return Check{Name: name, Status: StatusOK,
			Message: fmt.Sprintf("all bind-mount sources present (%d boxes)", len(names))}
	}
	return Check{Name: name, Status: StatusWarn,
		Message: fmt.Sprintf("%d missing mount sources: %s", len(missing), strings.Join(missing, "; "))}
}
