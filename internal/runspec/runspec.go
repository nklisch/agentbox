package runspec

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/project"
)

// Mount is a single bind mount.
type Mount struct {
	Source string
	Target string
	Mode   string // "rw" or "ro"
}

// PodmanCreateArgs is the structured form of a `podman create` invocation.
// Phase 3 will use this to actually call podman; Phase 1 only renders it
// for --dry-run.
type PodmanCreateArgs struct {
	Name     string
	Labels   []KV
	Mounts   []Mount
	Workdir  string
	EnvNames []string // -e NAME (no value, by name only — secrets policy)
	EnvVars  []KV     // -e NAME=VALUE (non-secret context: AGENTBOX_*, etc.)
	CPUs     int
	Memory   string
	PIDs     int
	CapDrop  []string
	CapAdd   []string // --cap-add <X>; populated when Containers.Enable
	SecOpt   []string
	Devices  []string // --device <X>; populated when Containers.Enable
	Network  string
	IP       string   // --ip <addr>; used by the CoreDNS sidecar for stable addressing
	DNS      []string // --dns <ip> entries; Phase 6 sets to [coredns-sidecar-ip] for safe/allowlist
	Image    string
	Argv     []string // typically [sleep, infinity]
}

// KV is a stable-ordered key/value pair (labels are an ordered list, not a
// map, so output is deterministic).
type KV struct {
	Key   string
	Value string
}

// BuildInput is everything BuildPodmanCreateArgs needs that isn't in Config.
type BuildInput struct {
	ProjectID   string
	ProjectAbs  string
	ProjectName string
	Agent       string
	Kits        []string
	HomeDir     string
	StateDir    string
	Created     time.Time
	SidecarDNS  []string // IPs to pass as --dns; set by lifecycle from network.Info.SidecarDNS
	SeccompPath string   // host path to bundled containers.json; populated by lifecycle when Containers.Enable
}

// KitImageTag returns the canonical image tag for a kit list.
//
//	tag = "agentbox/" + sha1(joined_resolved_list)[:12]
//
// Phase 1 uses this for the dry-run output even though the image won't
// be built until Phase 2.
func KitImageTag(kits []string) string {
	resolved := make([]string, len(kits))
	copy(resolved, kits)
	sort.Strings(resolved)
	h := sha1.Sum([]byte(strings.Join(resolved, "+")))
	return "agentbox/" + hex.EncodeToString(h[:])[:12]
}

// NetworkArg returns the value for `--network` per network.mode. Phase 1
// returns the placeholder name; Phase 6 will create the network.
func NetworkArg(cfg config.Config, projectID string) string {
	switch cfg.Network.Mode {
	case "off":
		return "none"
	case "open":
		return "bridge"
	default: // safe, allowlist
		return project.NetworkName(projectID)
	}
}

// BuildPodmanCreateArgs assembles the full args from config + input.
func BuildPodmanCreateArgs(cfg config.Config, in BuildInput) (PodmanCreateArgs, error) {
	args := PodmanCreateArgs{
		Name:    project.ContainerName(in.ProjectID),
		Workdir: in.ProjectAbs,
		CPUs:    cfg.Resources.CPUs,
		Memory:  cfg.Resources.Memory,
		PIDs:    cfg.Resources.PIDs,
		CapDrop: []string{"ALL"},
		SecOpt:  []string{"no-new-privileges"},
		Network: NetworkArg(cfg, in.ProjectID),
		DNS:     in.SidecarDNS,
		Image:   KitImageTag(in.Kits),
		Argv:    []string{"sleep", "infinity"},
	}

	args.Labels = []KV{
		{"agentbox", "1"},
		{"agentbox.role", "box"}, // identifies this as a user-facing box (not sidecar)
		{"agentbox.project", in.ProjectName},
		{"agentbox.project_id", in.ProjectID},
		{"agentbox.cwd", in.ProjectAbs},
		{"agentbox.agent", in.Agent},
		{"agentbox.kits", strings.Join(in.Kits, ",")},
		{"agentbox.kit_image", args.Image},
		{"agentbox.created", in.Created.UTC().Format(time.RFC3339)},
	}

	args.EnvVars = []KV{
		{Key: "AGENTBOX_PROJECT_ID", Value: in.ProjectID},
		{Key: "AGENTBOX_PROJECT", Value: in.ProjectName},
		{Key: "AGENTBOX_AGENT", Value: in.Agent},
		{Key: "AGENTBOX_KITS", Value: strings.Join(in.Kits, ",")},
		{Key: "AGENTBOX_KIT_IMAGE", Value: args.Image},
		{Key: "AGENTBOX_NETWORK", Value: cfg.Network.Mode},
		{Key: "AGENTBOX_CREATED", Value: in.Created.UTC().Format(time.RFC3339)},
		// In-container path; bind-mounts to <state-dir>/saved on the host.
		// Must match the saved/ Mount Target below.
		{Key: "AGENTBOX_SAVED_DIR", Value: "/root/.local/share/agentbox-saved"},
	}
	// Claude Code refuses --dangerously-skip-permissions when whoami==root
	// (anthropics/claude-code#9184). agentbox boxes run as root by design
	// (rootless podman maps container-root to the host user). IS_SANDBOX=1 is
	// the community-known undocumented bypass; verified against
	// @anthropic-ai/claude-code@2.x as of 2026-05-05. May break in future
	// releases — re-verify if claude exits with that error message.
	if in.Agent == "claude" {
		args.EnvVars = append(args.EnvVars, KV{Key: "IS_SANDBOX", Value: "1"})
	}

	// Same-path project mount (non-negotiable per CLAUDE.md).
	args.Mounts = []Mount{
		{Source: in.ProjectAbs, Target: in.ProjectAbs, Mode: "rw"},
	}
	if cfg.Mounts.Gitconfig && in.HomeDir != "" {
		args.Mounts = append(args.Mounts, Mount{
			Source: in.HomeDir + "/.gitconfig",
			Target: "/root/.gitconfig",
			Mode:   "rw",
		})
	}
	if cfg.Mounts.SSHReadonly && in.HomeDir != "" {
		args.Mounts = append(args.Mounts, Mount{
			Source: in.HomeDir + "/.ssh",
			Target: "/root/.ssh",
			Mode:   "ro",
		})
	}
	// Agent config dir for the resolved agent only.
	if src, ok := cfg.Mounts.AgentConfigs[in.Agent]; ok && src != "" {
		expanded := expandHome(src, in.HomeDir)
		args.Mounts = append(args.Mounts, Mount{
			Source: expanded,
			Target: "/root/." + in.Agent,
			Mode:   "rw",
		})
	}
	// Claude Code stores state in two places: the directory ~/.claude/
	// (settings, plugins) AND the sibling file ~/.claude.json (project
	// memories, MCP servers, machine ID). AgentConfigs above mounts the
	// directory; mount the sibling file too so first-run state persists.
	// Lifecycle touches the source path before podman create if it's missing
	// (otherwise podman would create it as a directory).
	if in.Agent == "claude" && in.HomeDir != "" {
		args.Mounts = append(args.Mounts, Mount{
			Source: in.HomeDir + "/.claude.json",
			Target: "/root/.claude.json",
			Mode:   "rw",
		})
	}
	// Session state dir mounts (shell history, layout, effective config, saved/).
	if in.StateDir != "" {
		args.Mounts = append(args.Mounts,
			Mount{Source: in.StateDir + "/history", Target: "/root/.local/share/agentbox-history", Mode: "rw"},
			Mount{Source: in.StateDir + "/layout.kdl", Target: "/etc/agentbox/layout.kdl", Mode: "ro"},
			Mount{Source: in.StateDir + "/effective-config.toml", Target: "/etc/agentbox/config.toml", Mode: "ro"},
			Mount{Source: in.StateDir + "/saved", Target: "/root/.local/share/agentbox-saved", Mode: "rw"},
		)
	}
	// Extra mounts ("<src>:<dst>:<mode>"). Validation deferred to a later phase.
	for _, e := range cfg.Mounts.Extra {
		m, err := parseExtraMount(e, in.HomeDir)
		if err != nil {
			return args, fmt.Errorf("mounts.extra: %w", err)
		}
		args.Mounts = append(args.Mounts, m)
	}

	args.EnvNames = append(args.EnvNames, cfg.Secrets.Passthrough...)

	if cfg.Containers.Enable {
		// Re-grant the small slice of caps + devices nested rootless podman needs.
		// CapDrop ALL stays; CapAdd layers specific caps back on top.
		args.CapAdd = append(args.CapAdd, cfg.Containers.ExtraCaps...)
		args.Devices = append(args.Devices, cfg.Containers.ExtraDevices...)
		args.SecOpt = append(args.SecOpt, "unmask=/proc/sys/net/ipv4")
		// `--security-opt seccomp=<path>` is read by podman from the HOST
		// filesystem at create time, before the container's filesystem
		// exists. The bundled profile has to be referenced by its host path
		// (lifecycle puts it under <state>/seccomp/containers.json and sets
		// SeccompPath). The in-container bind mount below is for in-box
		// introspection only; podman never reads it for seccomp.
		if in.SeccompPath != "" {
			args.SecOpt = append(args.SecOpt, "seccomp="+in.SeccompPath)
			args.Mounts = append(args.Mounts, Mount{
				Source: in.SeccompPath,
				Target: "/etc/agentbox/seccomp/containers.json",
				Mode:   "ro",
			})
		}
	}

	return args, nil
}

// ToShell renders the args as a multi-line shell invocation suitable for
// `agentbox run --dry-run` output. The first line is the runtime + create
// verb; each flag is on its own indented line ending with ` \`.
func (p PodmanCreateArgs) ToShell(runtime string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s create \\\n", runtime)
	fmt.Fprintf(&b, "  --name %q \\\n", p.Name)
	for _, kv := range p.Labels {
		fmt.Fprintf(&b, "  --label %q \\\n", kv.Key+"="+kv.Value)
	}
	for _, m := range p.Mounts {
		fmt.Fprintf(&b, "  -v %q \\\n", m.Source+":"+m.Target+":"+m.Mode)
	}
	if p.Workdir != "" {
		fmt.Fprintf(&b, "  -w %q \\\n", p.Workdir)
	}
	if p.CPUs > 0 {
		fmt.Fprintf(&b, "  --cpus %d \\\n", p.CPUs)
	}
	if p.Memory != "" {
		fmt.Fprintf(&b, "  --memory %q \\\n", p.Memory)
	}
	if p.PIDs > 0 {
		fmt.Fprintf(&b, "  --pids-limit %d \\\n", p.PIDs)
	}
	for _, c := range p.CapDrop {
		fmt.Fprintf(&b, "  --cap-drop %s \\\n", c)
	}
	for _, c := range p.CapAdd {
		fmt.Fprintf(&b, "  --cap-add %s \\\n", c)
	}
	for _, d := range p.Devices {
		fmt.Fprintf(&b, "  --device %q \\\n", d)
	}
	for _, s := range p.SecOpt {
		fmt.Fprintf(&b, "  --security-opt %s \\\n", s)
	}
	if p.Network != "" {
		fmt.Fprintf(&b, "  --network %q \\\n", p.Network)
	}
	if p.IP != "" {
		fmt.Fprintf(&b, "  --ip %q \\\n", p.IP)
	}
	for _, d := range p.DNS {
		fmt.Fprintf(&b, "  --dns %q \\\n", d)
	}
	for _, e := range p.EnvNames {
		fmt.Fprintf(&b, "  -e %s \\\n", e)
	}
	for _, e := range p.EnvVars {
		fmt.Fprintf(&b, "  -e %q \\\n", e.Key+"="+e.Value)
	}
	fmt.Fprintf(&b, "  %q", p.Image)
	for _, a := range p.Argv {
		fmt.Fprintf(&b, " %q", a)
	}
	b.WriteByte('\n')
	return b.String()
}

// expandHome replaces a leading "~" with homeDir.
func expandHome(s, homeDir string) string {
	if strings.HasPrefix(s, "~/") && homeDir != "" {
		return homeDir + s[1:]
	}
	if s == "~" && homeDir != "" {
		return homeDir
	}
	return s
}

// parseExtraMount parses "<src>:<dst>:<mode>" with ~ expansion on src.
func parseExtraMount(spec, homeDir string) (Mount, error) {
	parts := strings.Split(spec, ":")
	if len(parts) != 3 {
		return Mount{}, fmt.Errorf("expected <src>:<dst>:<mode>, got %q", spec)
	}
	mode := parts[2]
	if mode != "rw" && mode != "ro" {
		return Mount{}, fmt.Errorf("mode must be rw or ro, got %q", mode)
	}
	return Mount{
		Source: expandHome(parts[0], homeDir),
		Target: parts[1],
		Mode:   mode,
	}, nil
}
