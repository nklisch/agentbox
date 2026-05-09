package runspec

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/paths"
	"github.com/nklisch/agentbox/internal/state"
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
	Sysctls  []KV     // --sysctl key=value
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

	// Trail wiring (auditor layout + claude agent only). Both are set together
	// by lifecycle when trailEnabled(layoutName, agent) is true; both are empty
	// otherwise. Lifecycle is responsible for the gate decision.
	TrailHostPath          string // <state>/trail.jsonl; bound rw to /etc/agentbox/trail.jsonl
	ClaudeSettingsHostPath string // <state>/claude-settings.json; shadow-mounted ro over $HOME/.claude/settings.json

	// ExtraSamePathMounts is a list of host paths to bind-mount at the SAME
	// path inside the container (the project's same-path-mount philosophy).
	// Used for live-resolving external symlink targets discovered inside
	// agent config dirs (e.g. ~/.claude/skills/<name> symlinked to
	// ~/.agents/skills/<name>): mounting the resolved target at its same
	// host path lets the preserved symlink resolve cleanly inside the box,
	// without copying and without losing host↔box live sync.
	//
	// Mounts are emitted rw and deduplicated. Lifecycle is responsible for
	// the discovery; runspec only emits the mount entries.
	ExtraSamePathMounts []string
}

// RemoteImageRef returns the canonical GHCR reference for a resolved kit
// list at a given agentbox version. Format:
//
//	<host>:<version>-<sha1[:12]>
//
// where the version is normalized to drop a leading "v" so v0.3.0 and 0.3.0
// produce the same tag. Returns "" if host is empty.
func RemoteImageRef(host, version string, kits []string) string {
	if host == "" {
		return ""
	}
	v := strings.TrimPrefix(version, "v")
	sha := strings.TrimPrefix(KitImageTag(kits), "agentbox/")
	return fmt.Sprintf("%s:%s-%s", host, v, sha)
}

// RemoteAliasRef returns the human-readable alias variant. The nickname
// is the resolved kit list without "base", joined with "-". Returns "" if
// host or version is empty.
func RemoteAliasRef(host, version string, kits []string) string {
	if host == "" || version == "" {
		return ""
	}
	parts := make([]string, 0, len(kits))
	for _, k := range kits {
		if k == "base" {
			continue
		}
		parts = append(parts, k)
	}
	if len(parts) == 0 {
		parts = []string{"base"}
	}
	v := strings.TrimPrefix(version, "v")
	return fmt.Sprintf("%s:%s-%s", host, v, strings.Join(parts, "-"))
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
		return "agentbox-net-" + projectID
	}
}

// BuildPodmanCreateArgs assembles the full args from config + input.
func BuildPodmanCreateArgs(cfg config.Config, in BuildInput) (PodmanCreateArgs, error) {
	args := PodmanCreateArgs{
		Name:    "agentbox-" + in.ProjectID,
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
		// Disable IPv6 inside the box. agentbox-managed Podman networks are
		// created v4-only (ipv6_enabled=false) and the safe/allowlist iptables
		// + ipset plumbing has no v6 equivalent. Without this, AAAA lookups
		// resolve via CoreDNS but TCP connect to the resulting v6 address
		// hangs (no v6 route), producing mysterious plugin/MCP failures.
		// `none` mode is unaffected (no v6 interface to disable);
		// `open` mode defaults to Podman's bridge which is also v4-only
		// in typical rootless setups, so this is the right default there too.
		Sysctls: []KV{
			{Key: "net.ipv6.conf.all.disable_ipv6", Value: "1"},
			{Key: "net.ipv6.conf.default.disable_ipv6", Value: "1"},
		},
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

	// boxHome is the in-container HOME path. We mirror the host's home dir
	// path (same-path principle) so that absolute host paths embedded in
	// config files (claude plugin JSON's installPath, gitconfig includeIf,
	// etc.) resolve inside the box. Falls back to "/root" when HomeDir is
	// empty (test paths and defensive default — production always sets it).
	boxHome := in.HomeDir
	if boxHome == "" {
		boxHome = "/root"
	}

	args.EnvVars = []KV{
		{Key: "AGENTBOX_PROJECT_ID", Value: in.ProjectID},
		{Key: "AGENTBOX_PROJECT", Value: in.ProjectName},
		{Key: "AGENTBOX_AGENT", Value: in.Agent},
		{Key: "AGENTBOX_KITS", Value: strings.Join(in.Kits, ",")},
		{Key: "AGENTBOX_KIT_IMAGE", Value: args.Image},
		{Key: "AGENTBOX_NETWORK", Value: cfg.Network.Mode},
		{Key: "AGENTBOX_CREATED", Value: in.Created.UTC().Format(time.RFC3339)},
		// HOME inside the box mirrors the host's home dir path (same-path).
		// Container still runs as uid 0; HOME is just an env var, not a uid.
		{Key: "HOME", Value: boxHome},
		// In-container path; bind-mounts to <state-dir>/saved on the host.
		// Must match the saved/ Mount Target below.
		{Key: "AGENTBOX_SAVED_DIR", Value: boxHome + "/.local/share/agentbox-saved"},
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
			Target: boxHome + "/.gitconfig",
			Mode:   "rw",
		})
	}
	if cfg.Mounts.SSHReadonly && in.HomeDir != "" {
		args.Mounts = append(args.Mounts, Mount{
			Source: in.HomeDir + "/.ssh",
			Target: boxHome + "/.ssh",
			Mode:   "ro",
		})
	}
	// Agent config dir for the resolved agent only. Same-path mount: the
	// expanded host path is also the in-container target. This is critical
	// for claude — installed_plugins.json and known_marketplaces.json embed
	// absolute host paths (installPath, installLocation) that claude opens
	// verbatim. Without same-path mounting they'd point at nothing inside
	// the box and plugins would silently fail to load. Same-path is harmless
	// for codex/opencode (their config dirs don't embed absolute paths).
	if src, ok := cfg.Mounts.AgentConfigs[in.Agent]; ok && src != "" {
		expanded := paths.ExpandHome(src, in.HomeDir)
		args.Mounts = append(args.Mounts, Mount{
			Source: expanded,
			Target: expanded,
			Mode:   "rw",
		})
	}
	// Extra same-path mounts for external symlink targets (e.g. global
	// skills/plugins symlinked into ~/.claude from outside). Lifecycle
	// discovers these; runspec just emits the bind-mount entries. Each
	// path is mounted at its own host path inside the container so that
	// preserved symlinks under ~/.claude resolve cleanly. Deduped.
	{
		seen := map[string]bool{}
		for _, p := range in.ExtraSamePathMounts {
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			args.Mounts = append(args.Mounts, Mount{
				Source: p,
				Target: p,
				Mode:   "rw",
			})
		}
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
			Target: in.HomeDir + "/.claude.json",
			Mode:   "rw",
		})
	}
	// Trail mount (auditor + claude only). Lifecycle sets in.TrailHostPath to
	// the host JSONL path when trail is wired; we bind-mount it rw to the
	// in-container path and set BOX_TRAIL_FILE so box-trail can find it.
	if in.TrailHostPath != "" {
		args.Mounts = append(args.Mounts, Mount{
			Source: in.TrailHostPath,
			Target: "/etc/agentbox/trail.jsonl",
			Mode:   "rw",
		})
		args.EnvVars = append(args.EnvVars, KV{
			Key:   "BOX_TRAIL_FILE",
			Value: "/etc/agentbox/trail.jsonl",
		})
	}

	// Settings shadow mount (auditor + claude only). Lifecycle has merged
	// agentbox's trail hooks into the user's settings.json and written the
	// result to in.ClaudeSettingsHostPath. We bind-mount it read-only on top
	// of the existing ~/.claude directory mount so $HOME/.claude/settings.json
	// inside the box is the agentbox-managed copy. Mount ORDER matters: this
	// MUST come after the ~/.claude same-path directory mount above so podman
	// layers the file on top of the directory mount correctly.
	// The host's actual ~/.claude/settings.json is never touched by agentbox.
	if in.ClaudeSettingsHostPath != "" {
		args.Mounts = append(args.Mounts, Mount{
			Source: in.ClaudeSettingsHostPath,
			Target: boxHome + "/.claude/settings.json",
			Mode:   "ro",
		})
	}

	// Session state dir mounts (shell history, layout, effective config, saved/).
	if in.StateDir != "" {
		args.Mounts = append(args.Mounts,
			Mount{Source: state.HistoryPath(in.StateDir), Target: boxHome + "/.local/share/agentbox-history", Mode: "rw"},
			Mount{Source: state.LayoutPath(in.StateDir), Target: "/etc/agentbox/layout.kdl", Mode: "ro"},
			Mount{Source: state.EffectiveConfigPath(in.StateDir), Target: "/etc/agentbox/config.toml", Mode: "ro"},
			Mount{Source: state.SavedDirPath(in.StateDir), Target: boxHome + "/.local/share/agentbox-saved", Mode: "rw"},
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
	for _, s := range p.Sysctls {
		fmt.Fprintf(&b, "  --sysctl %q \\\n", s.Key+"="+s.Value)
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
		Source: paths.ExpandHome(parts[0], homeDir),
		Target: parts[1],
		Mode:   mode,
	}, nil
}
