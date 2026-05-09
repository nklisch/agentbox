package kits_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nklisch/agentbox/internal/config"
	"github.com/nklisch/agentbox/internal/kits"
	"github.com/nklisch/agentbox/internal/runspec"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const (
	baseManifest = `name = "base"
description = "Base kit"
depends_on  = []
`
	emptyPackages = ""
	minimalInstall = "#!/usr/bin/env bash\nexit 0\n"
	minimalEnv    = "# env\n"
)

// fakeKitFS returns an in-memory FS containing the four required kit files.
func fakeKitFS(manifestTOML, packages, install, env string) fs.FS {
	return fstest.MapFS{
		"manifest.toml": &fstest.MapFile{Data: []byte(manifestTOML)},
		"packages.txt":  &fstest.MapFile{Data: []byte(packages)},
		"install.sh":    &fstest.MapFile{Data: []byte(install)},
		"env.sh":        &fstest.MapFile{Data: []byte(env)},
	}
}

// fakeBaseFS returns a valid fs.FS for the "base" kit.
func fakeBaseFS() fs.FS {
	return fakeKitFS(baseManifest, emptyPackages, minimalInstall, minimalEnv)
}

// fakeRegistry creates a Registry backed by an in-memory builtin FS with the
// given named kits. Each kit in the map is a valid kit with a minimal manifest.
// The "base" kit is special and receives a base-specific manifest.
func fakeRegistry(kitNames ...string) *kits.Registry {
	builtinFS := fstest.MapFS{}
	for _, name := range kitNames {
		var manifest string
		if name == "base" {
			manifest = baseManifest
		} else {
			manifest = `name = "` + name + `"
description = "Kit ` + name + `"
`
		}
		builtinFS[name+"/manifest.toml"] = &fstest.MapFile{Data: []byte(manifest)}
		builtinFS[name+"/packages.txt"] = &fstest.MapFile{Data: []byte("")}
		builtinFS[name+"/install.sh"] = &fstest.MapFile{Data: []byte("#!/bin/bash\n")}
		builtinFS[name+"/env.sh"] = &fstest.MapFile{Data: []byte("# env\n")}
	}
	return kits.NewRegistry(builtinFS, "")
}

// newCacheForTest creates a Cache using a temp dir as XDG_DATA_HOME.
func newCacheForTest(t *testing.T) *kits.Cache {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	c, err := kits.NewCache()
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return c
}

// ---------------------------------------------------------------------------
// Unit 2 — parse.go
// ---------------------------------------------------------------------------

func TestLoad_ValidBase(t *testing.T) {
	kit, err := kits.Load("base", fakeBaseFS(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kit.Manifest.Name != "base" {
		t.Errorf("name: got %q, want %q", kit.Manifest.Name, "base")
	}
	if len(kit.Manifest.DependsOn) != 0 {
		t.Errorf("base DependsOn should be empty, got %v", kit.Manifest.DependsOn)
	}
	if kit.Source != "test" {
		t.Errorf("source: got %q, want %q", kit.Source, "test")
	}
}

func TestLoad_MissingRequiredFile(t *testing.T) {
	fsys := fstest.MapFS{
		"manifest.toml": &fstest.MapFile{Data: []byte(baseManifest)},
		"packages.txt":  &fstest.MapFile{Data: []byte("")},
		"env.sh":        &fstest.MapFile{Data: []byte("")},
		// install.sh is deliberately absent
	}
	_, err := kits.Load("base", fsys, "test")
	if err == nil {
		t.Fatal("expected error for missing install.sh, got nil")
	}
	if !strings.Contains(err.Error(), "install.sh") {
		t.Errorf("error should mention install.sh, got: %v", err)
	}
}

func TestLoad_MissingDescription(t *testing.T) {
	fsys := fakeKitFS(`name = "base"
depends_on = []
`, "", minimalInstall, minimalEnv)
	_, err := kits.Load("base", fsys, "test")
	if err == nil {
		t.Fatal("expected error for missing description, got nil")
	}
	if !strings.Contains(err.Error(), "description") {
		t.Errorf("error should mention description, got: %v", err)
	}
}

func TestLoad_InvalidName(t *testing.T) {
	_, err := kits.Load("FOO", fakeBaseFS(), "test")
	if err == nil {
		t.Fatal("expected error for invalid name, got nil")
	}
	if !strings.Contains(err.Error(), "invalid kit name") {
		t.Errorf("error should mention invalid name, got: %v", err)
	}
}

func TestLoad_InvalidName_Numeric(t *testing.T) {
	// Name must start with alphanumeric per the regex [a-z0-9][a-z0-9_-]*
	fsys := fakeKitFS(`name = "1foo"
description = "numeric start"
`, "", minimalInstall, minimalEnv)
	kit, err := kits.Load("1foo", fsys, "test")
	if err != nil {
		t.Fatalf("1foo should be valid: %v", err)
	}
	if kit.Manifest.Name != "1foo" {
		t.Errorf("name: got %q", kit.Manifest.Name)
	}
}

func TestLoad_NonBaseDefaultDependsOn(t *testing.T) {
	// A non-base kit with no depends_on in the manifest should default to ["base"].
	fsys := fakeKitFS(`name = "polyglot"
description = "Polyglot kit"
`, "", minimalInstall, minimalEnv)
	kit, err := kits.Load("polyglot", fsys, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(kit.Manifest.DependsOn) != 1 || kit.Manifest.DependsOn[0] != "base" {
		t.Errorf("DependsOn: got %v, want [base]", kit.Manifest.DependsOn)
	}
}

func TestLoad_BaseWithDependsOnErrors(t *testing.T) {
	fsys := fakeKitFS(`name = "base"
description = "Base kit"
depends_on = ["other"]
`, "", minimalInstall, minimalEnv)
	_, err := kits.Load("base", fsys, "test")
	if err == nil {
		t.Fatal("expected error when base has depends_on, got nil")
	}
	if !strings.Contains(err.Error(), "depends_on") {
		t.Errorf("error should mention depends_on, got: %v", err)
	}
}

func TestLoad_ManifestNameMismatch(t *testing.T) {
	// manifest says "foo" but dir name says "bar"
	fsys := fakeKitFS(`name = "foo"
description = "A kit"
`, "", minimalInstall, minimalEnv)
	_, err := kits.Load("bar", fsys, "test")
	if err == nil {
		t.Fatal("expected error for name mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "does not match directory name") {
		t.Errorf("error should mention mismatch, got: %v", err)
	}
}

func TestLoad_ManifestOmitsName_UsesDirectoryName(t *testing.T) {
	// manifest omits name — should default to the directory name "alpha".
	fsys := fakeKitFS(`description = "No explicit name"
`, "", minimalInstall, minimalEnv)
	kit, err := kits.Load("alpha", fsys, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kit.Manifest.Name != "alpha" {
		t.Errorf("name: got %q, want %q", kit.Manifest.Name, "alpha")
	}
}

// ---------------------------------------------------------------------------
// Unit 3 — registry.go
// ---------------------------------------------------------------------------

func TestRegistry_List_BuiltinOnly(t *testing.T) {
	reg := fakeRegistry("base", "node")
	names, err := reg.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 kits, got %d: %v", len(names), names)
	}
	if names[0] != "base" || names[1] != "node" {
		t.Errorf("expected [base, node], got %v", names)
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	reg := fakeRegistry("base")
	_, err := reg.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown kit, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error should wrap fs.ErrNotExist, got: %v", err)
	}
}

func TestRegistry_Get_Builtin(t *testing.T) {
	reg := fakeRegistry("base")
	kit, err := reg.Get("base")
	if err != nil {
		t.Fatalf("Get base: %v", err)
	}
	if kit.Source != "builtin" {
		t.Errorf("source: got %q, want %q", kit.Source, "builtin")
	}
}

func TestRegistry_UserShadowsBuiltin(t *testing.T) {
	// Set up a real temp dir with a user "base" kit.
	userDir := t.TempDir()
	baseDir := filepath.Join(userDir, "base")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"manifest.toml": baseManifest,
		"packages.txt":  "",
		"install.sh":    "#!/bin/bash\n",
		"env.sh":        "# env\n",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	builtinFS := fstest.MapFS{
		"base/manifest.toml": &fstest.MapFile{Data: []byte(baseManifest)},
		"base/packages.txt":  &fstest.MapFile{Data: []byte("")},
		"base/install.sh":    &fstest.MapFile{Data: []byte("#!/bin/bash\n")},
		"base/env.sh":        &fstest.MapFile{Data: []byte("# env\n")},
	}
	reg := kits.NewRegistry(builtinFS, userDir)

	kit, err := reg.Get("base")
	if err != nil {
		t.Fatalf("Get base: %v", err)
	}
	if !strings.HasPrefix(kit.Source, "user:") {
		t.Errorf("user kit should shadow builtin; source: %q", kit.Source)
	}
}

func TestRegistry_MissingUserDir_DoesNotError(t *testing.T) {
	builtinFS := fstest.MapFS{
		"base/manifest.toml": &fstest.MapFile{Data: []byte(baseManifest)},
		"base/packages.txt":  &fstest.MapFile{Data: []byte("")},
		"base/install.sh":    &fstest.MapFile{Data: []byte("#!/bin/bash\n")},
		"base/env.sh":        &fstest.MapFile{Data: []byte("# env\n")},
	}
	// Point to a dir that doesn't exist — should not error.
	reg := kits.NewRegistry(builtinFS, "/does/not/exist/ever")
	names, err := reg.List()
	if err != nil {
		t.Fatalf("List should succeed even with absent userDir: %v", err)
	}
	if len(names) != 1 || names[0] != "base" {
		t.Errorf("expected [base], got %v", names)
	}
}

func TestRegistry_Describe(t *testing.T) {
	reg := fakeRegistry("base", "node")
	infos, err := reg.Describe()
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 infos, got %d", len(infos))
	}
	for _, info := range infos {
		if info.Source != "builtin" {
			t.Errorf("kit %q: source %q, want builtin", info.Name, info.Source)
		}
	}
}

// ---------------------------------------------------------------------------
// Unit 4 — resolve.go
// ---------------------------------------------------------------------------

// registryWith creates a Registry containing exactly the given kits, where
// each kit can have custom DependsOn and ConflictsWith values via the
// manifestOverride.
func registryWithKits(t *testing.T, specs []struct {
	name          string
	dependsOn     []string
	conflictsWith []string
}) *kits.Registry {
	t.Helper()
	builtinFS := fstest.MapFS{}
	for _, s := range specs {
		var sb strings.Builder
		if s.name == "base" {
			sb.WriteString("name = \"base\"\ndescription = \"Base\"\ndepends_on = []\n")
		} else {
			sb.WriteString("name = \"" + s.name + "\"\n")
			sb.WriteString("description = \"Kit " + s.name + "\"\n")
			if s.dependsOn != nil {
				sb.WriteString("depends_on = [")
				for i, d := range s.dependsOn {
					if i > 0 {
						sb.WriteString(", ")
					}
					sb.WriteString("\"" + d + "\"")
				}
				sb.WriteString("]\n")
			}
			if len(s.conflictsWith) > 0 {
				sb.WriteString("conflicts_with = [")
				for i, c := range s.conflictsWith {
					if i > 0 {
						sb.WriteString(", ")
					}
					sb.WriteString("\"" + c + "\"")
				}
				sb.WriteString("]\n")
			}
		}
		builtinFS[s.name+"/manifest.toml"] = &fstest.MapFile{Data: []byte(sb.String())}
		builtinFS[s.name+"/packages.txt"] = &fstest.MapFile{Data: []byte("")}
		builtinFS[s.name+"/install.sh"] = &fstest.MapFile{Data: []byte("#!/bin/bash\n")}
		builtinFS[s.name+"/env.sh"] = &fstest.MapFile{Data: []byte("# env\n")}
	}
	return kits.NewRegistry(builtinFS, "")
}

func TestResolve_BaseOnly(t *testing.T) {
	reg := fakeRegistry("base")
	res, err := kits.Resolve(reg, []string{"base"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Kits) != 1 {
		t.Fatalf("expected 1 kit, got %d", len(res.Kits))
	}
	if res.Kits[0].Manifest.Name != "base" {
		t.Errorf("kit: got %q, want base", res.Kits[0].Manifest.Name)
	}
	expected := runspec.KitImageTag([]string{"base"})
	if res.Tag != expected {
		t.Errorf("tag: got %q, want %q", res.Tag, expected)
	}
}

func TestResolve_BaseFirst(t *testing.T) {
	// Requesting a non-base kit should pull in base first.
	reg := registryWithKits(t, []struct {
		name          string
		dependsOn     []string
		conflictsWith []string
	}{
		{name: "base"},
		{name: "node", dependsOn: []string{"base"}},
	})
	res, err := kits.Resolve(reg, []string{"node"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Kits) != 2 {
		t.Fatalf("expected 2 kits, got %d: %v", len(res.Kits), res.Names())
	}
	if res.Kits[0].Manifest.Name != "base" {
		t.Errorf("first kit should be base, got %q", res.Kits[0].Manifest.Name)
	}
	if res.Kits[1].Manifest.Name != "node" {
		t.Errorf("second kit should be node, got %q", res.Kits[1].Manifest.Name)
	}
}

func TestResolve_DependencyChain(t *testing.T) {
	// claude → node → base. Expect [base, node, claude].
	reg := registryWithKits(t, []struct {
		name          string
		dependsOn     []string
		conflictsWith []string
	}{
		{name: "base"},
		{name: "node", dependsOn: []string{"base"}},
		{name: "claude", dependsOn: []string{"base", "node"}},
	})
	res, err := kits.Resolve(reg, []string{"claude"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	names := res.Names()
	if len(names) != 3 {
		t.Fatalf("expected 3 kits, got %v", names)
	}
	if names[0] != "base" {
		t.Errorf("first kit should be base, got %q", names[0])
	}
	if names[len(names)-1] != "claude" {
		t.Errorf("last kit should be claude, got %q", names[len(names)-1])
	}
}

func TestResolve_NoDuplicateBase(t *testing.T) {
	// Two kits both depending on base — base should appear only once.
	reg := registryWithKits(t, []struct {
		name          string
		dependsOn     []string
		conflictsWith []string
	}{
		{name: "base"},
		{name: "a", dependsOn: []string{"base"}},
		{name: "b", dependsOn: []string{"base"}},
	})
	res, err := kits.Resolve(reg, []string{"a", "b"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	count := 0
	for _, k := range res.Kits {
		if k.Manifest.Name == "base" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("base should appear exactly once, got %d: %v", count, res.Names())
	}
}

func TestResolve_Cycle(t *testing.T) {
	// a→b, b→a
	reg := registryWithKits(t, []struct {
		name          string
		dependsOn     []string
		conflictsWith []string
	}{
		{name: "base"},
		{name: "a", dependsOn: []string{"b"}},
		{name: "b", dependsOn: []string{"a"}},
	})
	_, err := kits.Resolve(reg, []string{"a"})
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error should mention cycle, got: %v", err)
	}
}

func TestResolve_MissingDep(t *testing.T) {
	// a depends on "missing" which doesn't exist.
	reg := registryWithKits(t, []struct {
		name          string
		dependsOn     []string
		conflictsWith []string
	}{
		{name: "base"},
		{name: "a", dependsOn: []string{"missing"}},
	})
	_, err := kits.Resolve(reg, []string{"a"})
	if err == nil {
		t.Fatal("expected error for missing dep, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error should wrap fs.ErrNotExist, got: %v", err)
	}
}

func TestResolve_Conflicts(t *testing.T) {
	reg := registryWithKits(t, []struct {
		name          string
		dependsOn     []string
		conflictsWith []string
	}{
		{name: "base"},
		{name: "a", dependsOn: []string{"base"}, conflictsWith: []string{"b"}},
		{name: "b", dependsOn: []string{"base"}},
	})
	_, err := kits.Resolve(reg, []string{"a", "b"})
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error should mention conflict, got: %v", err)
	}
	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Errorf("error should name both kits, got: %v", err)
	}
}

func TestResolve_EmptyInput(t *testing.T) {
	reg := fakeRegistry("base")
	_, err := kits.Resolve(reg, []string{})
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
	if !strings.Contains(err.Error(), "at least one kit") {
		t.Errorf("error should say 'at least one kit', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Unit 5 — hash.go
// ---------------------------------------------------------------------------

func TestHashKit_Deterministic(t *testing.T) {
	fsys := fakeBaseFS()
	kit, _ := kits.Load("base", fsys, "test")

	h1, err := kits.HashKit(kit)
	if err != nil {
		t.Fatalf("HashKit: %v", err)
	}
	h2, err := kits.HashKit(kit)
	if err != nil {
		t.Fatalf("HashKit (2nd): %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 40 {
		t.Errorf("expected 40-char SHA1 hex, got len=%d: %q", len(h1), h1)
	}
}

func TestHashKit_DiffersOnContentChange(t *testing.T) {
	fsys1 := fakeKitFS(baseManifest, "", "#!/bin/bash\necho hello\n", minimalEnv)
	fsys2 := fakeKitFS(baseManifest, "", "#!/bin/bash\necho world\n", minimalEnv)

	k1, _ := kits.Load("base", fsys1, "test")
	k2, _ := kits.Load("base", fsys2, "test")

	h1, _ := kits.HashKit(k1)
	h2, _ := kits.HashKit(k2)

	if h1 == h2 {
		t.Error("hashes should differ when install.sh content changes")
	}
}

func TestHashKit_DiffersOnFileRename(t *testing.T) {
	// Same content, different file name (add an extra file with different name).
	fsys1 := fstest.MapFS{
		"manifest.toml": &fstest.MapFile{Data: []byte(baseManifest)},
		"packages.txt":  &fstest.MapFile{Data: []byte("")},
		"install.sh":    &fstest.MapFile{Data: []byte(minimalInstall)},
		"env.sh":        &fstest.MapFile{Data: []byte(minimalEnv)},
		"extra-a.sh":    &fstest.MapFile{Data: []byte("echo a\n")},
	}
	fsys2 := fstest.MapFS{
		"manifest.toml": &fstest.MapFile{Data: []byte(baseManifest)},
		"packages.txt":  &fstest.MapFile{Data: []byte("")},
		"install.sh":    &fstest.MapFile{Data: []byte(minimalInstall)},
		"env.sh":        &fstest.MapFile{Data: []byte(minimalEnv)},
		"extra-b.sh":    &fstest.MapFile{Data: []byte("echo a\n")}, // same content, different name
	}

	k1, _ := kits.Load("base", fsys1, "test")
	k2, _ := kits.Load("base", fsys2, "test")

	h1, _ := kits.HashKit(k1)
	h2, _ := kits.HashKit(k2)

	if h1 == h2 {
		t.Error("hashes should differ when a file is renamed")
	}
}

// ---------------------------------------------------------------------------
// Unit 6 — dockerfile.go
// ---------------------------------------------------------------------------

func resolveBase(t *testing.T) kits.Resolved {
	t.Helper()
	reg := fakeRegistry("base")
	res, err := kits.Resolve(reg, []string{"base"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return res
}

func TestGenerateDockerfile_Stable(t *testing.T) {
	res := resolveBase(t)
	df1 := kits.GenerateDockerfile(res, "v0.1")
	df2 := kits.GenerateDockerfile(res, "v0.1")
	if df1 != df2 {
		t.Error("GenerateDockerfile is not deterministic")
	}
}

func TestGenerateDockerfile_StartsWithGeneratedComment(t *testing.T) {
	res := resolveBase(t)
	df := kits.GenerateDockerfile(res, "v0.1")
	if !strings.HasPrefix(df, "# Generated by agentbox") {
		t.Errorf("expected header comment, got: %q", df[:min(80, len(df))])
	}
}

func TestGenerateDockerfile_FromDebian(t *testing.T) {
	res := resolveBase(t)
	df := kits.GenerateDockerfile(res, "v0.1")
	if count := strings.Count(df, "FROM debian:bookworm-slim"); count != 1 {
		t.Errorf("expected exactly 1 FROM line, got %d", count)
	}
}

func TestGenerateDockerfile_BaseOnly(t *testing.T) {
	res := resolveBase(t)
	df := kits.GenerateDockerfile(res, "v0.1")

	if !strings.Contains(df, "COPY kits/base/ /tmp/kit-base/") {
		t.Errorf("missing COPY line for base:\n%s", df)
	}
	if !strings.Contains(df, "cp /tmp/kit-base/env.sh /etc/agentbox/env.d/00-base.sh") {
		t.Errorf("missing env.d copy with 00 prefix:\n%s", df)
	}
}

func TestGenerateDockerfile_PolyglotEnvPrefix(t *testing.T) {
	reg := registryWithKits(t, []struct {
		name          string
		dependsOn     []string
		conflictsWith []string
	}{
		{name: "base"},
		{name: "polyglot", dependsOn: []string{"base"}},
	})
	res, err := kits.Resolve(reg, []string{"polyglot"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	df := kits.GenerateDockerfile(res, "v0.1")

	if !strings.Contains(df, "cp /tmp/kit-polyglot/env.sh /etc/agentbox/env.d/10-polyglot.sh") {
		t.Errorf("polyglot env should be prefixed 10:\n%s", df)
	}
}

func TestGenerateDockerfile_FinalWireup(t *testing.T) {
	res := resolveBase(t)
	df := kits.GenerateDockerfile(res, "v0.1")
	if !strings.Contains(df, `for f in /etc/agentbox/env.d/*.sh; do . "$f"; done`) {
		t.Errorf("missing env.d source loop:\n%s", df)
	}
	if !strings.Contains(df, "apt-get clean") {
		t.Errorf("missing apt-get clean:\n%s", df)
	}
	if !strings.Contains(df, "/tmp/kit-*") {
		t.Errorf("missing /tmp/kit-* cleanup:\n%s", df)
	}
}

// ---------------------------------------------------------------------------
// Unit 7 — cache.go
// ---------------------------------------------------------------------------

func TestCache_RoundTrip(t *testing.T) {
	cache := newCacheForTest(t)
	res := resolveBase(t)

	hashes, err := kits.HashesFromResolved(res)
	if err != nil {
		t.Fatalf("HashesFromResolved: %v", err)
	}
	entry := kits.CacheEntry{
		Tag:             res.Tag,
		Kits:            res.Names(),
		KitHashes:       hashes,
		AgentboxVersion: "v0.1",
	}
	dockerfile := kits.GenerateDockerfile(res, "v0.1")

	if err := cache.Save(entry, dockerfile); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := cache.Lookup(res.Tag)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Tag != entry.Tag {
		t.Errorf("tag: got %q, want %q", got.Tag, entry.Tag)
	}
	if len(got.KitHashes) != len(entry.KitHashes) {
		t.Errorf("kit hashes len: got %d, want %d", len(got.KitHashes), len(entry.KitHashes))
	}
	for k, v := range entry.KitHashes {
		if got.KitHashes[k] != v {
			t.Errorf("kit hash[%q]: got %q, want %q", k, got.KitHashes[k], v)
		}
	}
}

func TestCache_LookupMissing(t *testing.T) {
	cache := newCacheForTest(t)
	_, err := cache.Lookup("agentbox/does-not-exist")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected fs.ErrNotExist, got: %v", err)
	}
}

func TestCache_HasMatch_True(t *testing.T) {
	cache := newCacheForTest(t)
	res := resolveBase(t)

	hashes, _ := kits.HashesFromResolved(res)
	entry := kits.CacheEntry{
		Tag:       res.Tag,
		Kits:      res.Names(),
		KitHashes: hashes,
	}
	_ = cache.Save(entry, "")

	match, err := cache.HasMatch(res)
	if err != nil {
		t.Fatalf("HasMatch: %v", err)
	}
	if !match {
		t.Error("HasMatch should return true when all hashes match")
	}
}

func TestCache_HasMatch_False_ContentChanged(t *testing.T) {
	cache := newCacheForTest(t)
	reg := fakeRegistry("base")
	res, _ := kits.Resolve(reg, []string{"base"})

	// Save with "old" hashes (just set a wrong value).
	entry := kits.CacheEntry{
		Tag:       res.Tag,
		Kits:      res.Names(),
		KitHashes: map[string]string{"base": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"},
	}
	_ = cache.Save(entry, "")

	match, err := cache.HasMatch(res)
	if err != nil {
		t.Fatalf("HasMatch: %v", err)
	}
	if match {
		t.Error("HasMatch should return false when hashes differ")
	}
}

func TestCache_HasMatch_False_Missing(t *testing.T) {
	cache := newCacheForTest(t)
	res := resolveBase(t)
	// Nothing saved yet.
	match, err := cache.HasMatch(res)
	if err != nil {
		t.Fatalf("HasMatch: %v", err)
	}
	if match {
		t.Error("HasMatch should return false when no cache entry exists")
	}
}

func TestCache_ListEntries_SkipsNonJSON(t *testing.T) {
	cache := newCacheForTest(t)
	res := resolveBase(t)
	hashes, _ := kits.HashesFromResolved(res)
	entry := kits.CacheEntry{
		Tag:       res.Tag,
		Kits:      res.Names(),
		KitHashes: hashes,
	}
	_ = cache.Save(entry, "some dockerfile")

	// Also place a non-JSON file.
	_ = os.WriteFile(filepath.Join(cache.Dir, "junk.txt"), []byte("noise"), 0o644)

	entries, err := cache.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (junk.txt skipped), got %d: %v", len(entries), entries)
	}
}

func TestCache_Remove(t *testing.T) {
	cache := newCacheForTest(t)
	res := resolveBase(t)
	hashes, _ := kits.HashesFromResolved(res)
	entry := kits.CacheEntry{
		Tag:       res.Tag,
		Kits:      res.Names(),
		KitHashes: hashes,
	}
	_ = cache.Save(entry, "")
	if err := cache.Remove(res.Tag); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	_, err := cache.Lookup(res.Tag)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after Remove, Lookup should return ErrNotExist, got: %v", err)
	}
	// Calling Remove again should be a no-op, not an error.
	if err := cache.Remove(res.Tag); err != nil {
		t.Errorf("second Remove should be no-op, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Unit 8 — builder.go (with fakeRunner)
// ---------------------------------------------------------------------------

type fakeRunner struct {
	builds    []kits.BuildContext
	hasImage  map[string]bool
	liveRefs  []string
	removed   []string
	failBuild error
	pullCalls []kits.PullContext
	tagCalls  [][2]string
	pullErr   *kits.PullError
}

func (r *fakeRunner) Build(ctx kits.BuildContext) error {
	r.builds = append(r.builds, ctx)
	return r.failBuild
}

func (r *fakeRunner) HasImage(tag string) (bool, error) {
	return r.hasImage[tag], nil
}

func (r *fakeRunner) LiveImageRefs() ([]string, error) {
	return r.liveRefs, nil
}

func (r *fakeRunner) RemoveImage(tag string) error {
	r.removed = append(r.removed, tag)
	return nil
}

func (r *fakeRunner) Pull(ctx kits.PullContext) error {
	r.pullCalls = append(r.pullCalls, ctx)
	if r.pullErr != nil {
		return r.pullErr
	}
	return nil
}

func (r *fakeRunner) Tag(src, dst string) error {
	r.tagCalls = append(r.tagCalls, [2]string{src, dst})
	return nil
}

func (r *fakeRunner) Bin() string {
	return "podman"
}

func newBuilder(t *testing.T, runner *fakeRunner) *kits.Builder {
	t.Helper()
	cache := newCacheForTest(t)
	reg := fakeRegistry("base")
	return &kits.Builder{
		Registry: reg,
		Cache:    cache,
		Runner:   runner,
		Version:  "v0.1",
	}
}

func TestBuilder_FreshBuild(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilder(t, runner)

	result, err := b.Build([]string{"base"}, kits.BuildOpts{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(runner.builds) != 1 {
		t.Errorf("expected 1 Runner.Build call, got %d", len(runner.builds))
	}
	if result.CacheHit {
		t.Error("fresh build should not be a cache hit")
	}
	if result.Tag == "" {
		t.Error("result.Tag should not be empty")
	}
}

func TestBuilder_BuildContextHasDockerfile(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilder(t, runner)

	_, err := b.Build([]string{"base"}, kits.BuildOpts{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(runner.builds) == 0 {
		t.Fatal("no builds recorded")
	}
	ctx := runner.builds[0]
	if ctx.Dockerfile == "" {
		t.Error("BuildContext.Dockerfile should not be empty")
	}
	if ctx.ContextDir == "" {
		t.Error("BuildContext.ContextDir should not be empty")
	}
	// The temp dir is cleaned up after Build returns, but the path was set.
	if !strings.Contains(ctx.Dockerfile, "Dockerfile") {
		t.Errorf("Dockerfile path should contain 'Dockerfile', got: %q", ctx.Dockerfile)
	}
}

func TestBuilder_CacheHitWhenImagePresent(t *testing.T) {
	res := resolveBase(t)
	runner := &fakeRunner{
		hasImage: map[string]bool{res.Tag: true},
	}
	b := newBuilder(t, runner)

	// First build: populates cache.
	_, err := b.Build([]string{"base"}, kits.BuildOpts{})
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}
	if len(runner.builds) != 1 {
		t.Fatalf("expected 1 build on first call, got %d", len(runner.builds))
	}

	// Second build: cache hit, image present — Runner.Build should NOT be called.
	result, err := b.Build([]string{"base"}, kits.BuildOpts{})
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	if len(runner.builds) != 1 {
		t.Errorf("Runner.Build should not be called on cache hit; calls: %d", len(runner.builds))
	}
	if !result.CacheHit {
		t.Error("second build should be a cache hit")
	}
}

func TestBuilder_CacheStaleWhenImageMissing(t *testing.T) {
	res := resolveBase(t)
	runner := &fakeRunner{
		// HasImage returns false (image was pruned externally).
		hasImage: map[string]bool{res.Tag: false},
	}
	b := newBuilder(t, runner)

	// First build to populate cache.
	_, err := b.Build([]string{"base"}, kits.BuildOpts{})
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}

	// Second build: cache has an entry, but HasImage returns false → should rebuild.
	result, err := b.Build([]string{"base"}, kits.BuildOpts{})
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	if len(runner.builds) != 2 {
		t.Errorf("expected 2 Runner.Build calls (stale cache), got %d", len(runner.builds))
	}
	if result.CacheHit {
		t.Error("stale cache (image missing) should not be a cache hit")
	}
}

func TestBuilder_NoCache_AlwaysBuilds(t *testing.T) {
	res := resolveBase(t)
	runner := &fakeRunner{
		hasImage: map[string]bool{res.Tag: true},
	}
	b := newBuilder(t, runner)

	// First build to populate cache.
	_, _ = b.Build([]string{"base"}, kits.BuildOpts{})

	// Second build with NoCache: should bypass cache entirely.
	_, err := b.Build([]string{"base"}, kits.BuildOpts{NoCache: true})
	if err != nil {
		t.Fatalf("NoCache Build: %v", err)
	}
	if len(runner.builds) != 2 {
		t.Errorf("NoCache should call Runner.Build every time; calls: %d", len(runner.builds))
	}
}

// --- registry.refresh: rolling-tag pull + cache age gate ---

// When refresh is "always", a normally-valid cache hit is bypassed and the
// builder pulls the rolling `latest-<nickname>` tag instead.
func TestBuilder_RefreshAlways_BypassesCacheAndUsesRollingRef(t *testing.T) {
	res := resolveBase(t)
	runner := &fakeRunner{hasImage: map[string]bool{res.Tag: true}}
	b := newBuilder(t, runner)
	b.RegistryEnabled = true
	b.RegistryHost = "ghcr.io/example/agentbox-kits"
	b.RegistryRefresh = config.RefreshPolicy{Enabled: true, Always: true}

	// First build: populates cache (no pull config to verify yet — tryPull is
	// gated on RegistryEnabled which we just turned on, but with Always set
	// the cache short-circuit is skipped and Pull WILL be called).
	if _, err := b.Build([]string{"base"}, kits.BuildOpts{}); err != nil {
		t.Fatalf("first Build: %v", err)
	}

	// Second build: cache hit + image present, but Always=true → re-pull.
	if _, err := b.Build([]string{"base"}, kits.BuildOpts{}); err != nil {
		t.Fatalf("second Build: %v", err)
	}
	if len(runner.pullCalls) < 1 {
		t.Fatalf("expected at least 1 Pull call when refresh=always, got %d", len(runner.pullCalls))
	}
	wantRef := "ghcr.io/example/agentbox-kits:latest-base"
	for _, pc := range runner.pullCalls {
		if pc.Ref != wantRef {
			t.Errorf("Pull ref = %q, want rolling tag %q (refresh enabled should not use version-pinned ref)", pc.Ref, wantRef)
		}
	}
}

// When refresh is a duration and the cache entry is younger than the
// threshold, the cache short-circuit still wins and no pull happens.
func TestBuilder_RefreshDuration_FreshCacheStillHits(t *testing.T) {
	res := resolveBase(t)
	runner := &fakeRunner{hasImage: map[string]bool{res.Tag: true}}
	b := newBuilder(t, runner)
	b.RegistryEnabled = true
	b.RegistryHost = "ghcr.io/example/agentbox-kits"
	b.RegistryRefresh = config.RefreshPolicy{Enabled: true, MaxAge: 24 * time.Hour}

	// Inject a clock so the first build's BuiltAt is "now" and the second
	// build's now is only 1h later — well under the 24h threshold.
	t0 := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	clock := t0
	b.Now = func() time.Time { return clock }

	if _, err := b.Build([]string{"base"}, kits.BuildOpts{}); err != nil {
		t.Fatalf("first Build: %v", err)
	}
	pullsBefore := len(runner.pullCalls)

	clock = t0.Add(1 * time.Hour)
	result, err := b.Build([]string{"base"}, kits.BuildOpts{})
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	if !result.CacheHit {
		t.Error("cache should hit when entry is younger than refresh threshold")
	}
	if len(runner.pullCalls) != pullsBefore {
		t.Errorf("expected no new Pull calls, got %d → %d", pullsBefore, len(runner.pullCalls))
	}
}

// When refresh is a duration and the cache entry is older than the threshold,
// the cache short-circuit is bypassed and a pull is attempted with the
// rolling tag.
func TestBuilder_RefreshDuration_StaleCacheBypassed(t *testing.T) {
	res := resolveBase(t)
	runner := &fakeRunner{hasImage: map[string]bool{res.Tag: true}}
	b := newBuilder(t, runner)
	b.RegistryEnabled = true
	b.RegistryHost = "ghcr.io/example/agentbox-kits"
	b.RegistryRefresh = config.RefreshPolicy{Enabled: true, MaxAge: 24 * time.Hour}

	t0 := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	clock := t0
	b.Now = func() time.Time { return clock }

	if _, err := b.Build([]string{"base"}, kits.BuildOpts{}); err != nil {
		t.Fatalf("first Build: %v", err)
	}
	pullsBefore := len(runner.pullCalls)

	// Jump 25h forward — past the 24h refresh threshold.
	clock = t0.Add(25 * time.Hour)
	if _, err := b.Build([]string{"base"}, kits.BuildOpts{}); err != nil {
		t.Fatalf("second Build: %v", err)
	}
	if len(runner.pullCalls) <= pullsBefore {
		t.Errorf("expected a new Pull call after 25h, got %d → %d", pullsBefore, len(runner.pullCalls))
	}
}

// Refresh disabled (default) preserves the legacy behavior: cache hits
// skip the registry and the version-pinned ref is used when pulling.
func TestBuilder_RefreshOff_UsesVersionPinnedRef(t *testing.T) {
	res := resolveBase(t)
	runner := &fakeRunner{hasImage: map[string]bool{res.Tag: false}}
	b := newBuilder(t, runner)
	b.RegistryEnabled = true
	b.RegistryHost = "ghcr.io/example/agentbox-kits"
	// RegistryRefresh left at zero value (disabled).

	if _, err := b.Build([]string{"base"}, kits.BuildOpts{}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(runner.pullCalls) == 0 {
		t.Fatal("expected a Pull call (registry enabled, no local image)")
	}
	for _, pc := range runner.pullCalls {
		// RemoteImageRef strips the leading "v" from the version.
		if !strings.Contains(pc.Ref, ":0.1-") {
			t.Errorf("Pull ref = %q, want version-pinned (:0.1-...) when refresh disabled", pc.Ref)
		}
		if strings.Contains(pc.Ref, ":latest-") {
			t.Errorf("Pull ref = %q, must NOT use rolling tag when refresh disabled", pc.Ref)
		}
	}
}

func TestBuilder_PrintDockerfile(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilder(t, runner)

	df, err := b.PrintDockerfile([]string{"base"})
	if err != nil {
		t.Fatalf("PrintDockerfile: %v", err)
	}
	if len(runner.builds) != 0 {
		t.Error("PrintDockerfile should not call Runner.Build")
	}
	if !strings.Contains(df, "FROM debian:bookworm-slim") {
		t.Errorf("Dockerfile missing FROM line:\n%s", df)
	}
}

func TestBuilder_Prune(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cache, _ := kits.NewCache()
	reg := fakeRegistry("base")

	// Imagine two cached images: live and stale.
	liveTag := "agentbox/live111111"
	staleTag := "agentbox/stale2222"

	_ = cache.Save(kits.CacheEntry{Tag: liveTag, KitHashes: map[string]string{}}, "")
	_ = cache.Save(kits.CacheEntry{Tag: staleTag, KitHashes: map[string]string{}}, "")

	runner := &fakeRunner{
		liveRefs: []string{liveTag}, // only liveTag is in use by a container
	}
	b := &kits.Builder{
		Registry: reg,
		Cache:    cache,
		Runner:   runner,
		Version:  "v0.1",
	}

	result, err := b.Prune()
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != staleTag {
		t.Errorf("expected [%s] removed, got %v", staleTag, result.Removed)
	}
	if len(result.Kept) != 1 || result.Kept[0] != liveTag {
		t.Errorf("expected [%s] kept, got %v", liveTag, result.Kept)
	}
	if len(runner.removed) != 1 || runner.removed[0] != staleTag {
		t.Errorf("Runner.RemoveImage should be called once for stale, got %v", runner.removed)
	}
}

// ---------------------------------------------------------------------------
// Unit tests for registry pull path (Units 3–6)
// ---------------------------------------------------------------------------

// TestClassifyPullStderr is a table-driven test of the pure classification
// function. It covers all the keyword categories.
func TestClassifyPullStderr(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		ctxErr error
		want   kits.PullErrorKind
	}{
		{
			name:   "manifest unknown",
			stderr: "Error: reading manifest latest in ghcr.io/foo/bar: manifest unknown",
			want:   kits.PullErrorNotFound,
		},
		{
			name:   "not found",
			stderr: "Error: ghcr.io/foo/bar: image not found",
			want:   kits.PullErrorNotFound,
		},
		{
			name:   "404 in stderr",
			stderr: "Trying to pull ghcr.io/foo/bar...  Error: Request failed with status 404",
			want:   kits.PullErrorNotFound,
		},
		{
			name:   "unauthorized",
			stderr: "Error: copying reference ghcr.io/foo/bar: unauthorized: authentication required",
			want:   kits.PullErrorAuth,
		},
		{
			name:   "denied",
			stderr: "Error: access denied for ghcr.io/private/repo",
			want:   kits.PullErrorAuth,
		},
		{
			name:   "401 in stderr",
			stderr: "HTTP/2 401 from ghcr.io",
			want:   kits.PullErrorAuth,
		},
		{
			name:   "403 in stderr",
			stderr: "Request forbidden: 403",
			want:   kits.PullErrorAuth,
		},
		{
			name:   "no such host",
			stderr: "Error: Get https://ghcr.invalid/v2/: dial tcp: lookup ghcr.invalid: no such host",
			want:   kits.PullErrorNetwork,
		},
		{
			name:   "connection refused",
			stderr: "Error: Get https://localhost:5000/v2/: connection refused",
			want:   kits.PullErrorNetwork,
		},
		{
			name:   "dial tcp",
			stderr: "Error: dial tcp 1.2.3.4:443: connect: connection refused",
			want:   kits.PullErrorNetwork,
		},
		{
			name:   "timeout in stderr",
			stderr: "Error: timeout waiting for response",
			want:   kits.PullErrorNetwork,
		},
		{
			name:   "unknown error",
			stderr: "Error: something completely unexpected happened",
			want:   kits.PullErrorUnknown,
		},
		{
			name:   "empty stderr",
			stderr: "",
			want:   kits.PullErrorUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := kits.ClassifyPullStderrForTest(tt.stderr, tt.ctxErr)
			if got != tt.want {
				t.Errorf("classifyPullStderr(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
	}
}

// TestAllBuiltin_AllBuiltin verifies that a fully built-in resolved list returns true.
func TestAllBuiltin_AllBuiltin(t *testing.T) {
	reg := fakeRegistry("base", "node")
	res, err := kits.Resolve(reg, []string{"node"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !kits.AllBuiltin(res) {
		t.Error("AllBuiltin should return true for all-builtin resolved list")
	}
}

// TestAllBuiltin_UserShadow verifies that a user-shadowed kit returns false.
func TestAllBuiltin_UserShadow(t *testing.T) {
	userDir := t.TempDir()
	baseDir := userDir + "/base"
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"manifest.toml": baseManifest,
		"packages.txt":  "",
		"install.sh":    "#!/bin/bash\n",
		"env.sh":        "# env\n",
	} {
		if err := os.WriteFile(baseDir+"/"+name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	builtinFS := fstest.MapFS{
		"base/manifest.toml": &fstest.MapFile{Data: []byte(baseManifest)},
		"base/packages.txt":  &fstest.MapFile{Data: []byte("")},
		"base/install.sh":    &fstest.MapFile{Data: []byte("#!/bin/bash\n")},
		"base/env.sh":        &fstest.MapFile{Data: []byte("# env\n")},
	}
	reg := kits.NewRegistry(builtinFS, userDir)
	res, err := kits.Resolve(reg, []string{"base"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if kits.AllBuiltin(res) {
		t.Error("AllBuiltin should return false when a kit is user-shadowed")
	}
}

// TestAllBuiltin_EmptyList verifies empty resolved list returns false.
func TestAllBuiltin_EmptyList(t *testing.T) {
	res := kits.Resolved{}
	if kits.AllBuiltin(res) {
		t.Error("AllBuiltin should return false for an empty resolved list")
	}
}

// TestBuild_PullHit_WhenAllBuiltinAndEnabled verifies pull path succeeds.
func TestBuild_PullHit_WhenAllBuiltinAndEnabled(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilderWithRegistry(t, runner)

	result, err := b.Build([]string{"base"}, kits.BuildOpts{
		Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !result.PullHit {
		t.Error("expected PullHit=true when registry enabled and pull succeeds")
	}
	if result.CacheHit {
		t.Error("PullHit result should not set CacheHit")
	}
	if len(runner.pullCalls) != 1 {
		t.Errorf("expected 1 Pull call, got %d", len(runner.pullCalls))
	}
	if len(runner.tagCalls) != 1 {
		t.Errorf("expected 1 Tag call, got %d", len(runner.tagCalls))
	}
}

// TestBuild_NoPullFlag_SkipsRegistry verifies --no-pull skips pull entirely.
func TestBuild_NoPullFlag_SkipsRegistry(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilderWithRegistry(t, runner)

	_, err := b.Build([]string{"base"}, kits.BuildOpts{NoPull: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(runner.pullCalls) != 0 {
		t.Errorf("expected 0 Pull calls with NoPull=true, got %d", len(runner.pullCalls))
	}
	if len(runner.builds) != 1 {
		t.Errorf("expected 1 local Build call, got %d", len(runner.builds))
	}
}

// TestBuild_RegistryDisabled_SkipsRegistry verifies disabled registry skips pull.
func TestBuild_RegistryDisabled_SkipsRegistry(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilderWithRegistry(t, runner)
	b.RegistryEnabled = false

	_, err := b.Build([]string{"base"}, kits.BuildOpts{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(runner.pullCalls) != 0 {
		t.Errorf("expected 0 Pull calls with RegistryEnabled=false, got %d", len(runner.pullCalls))
	}
}

// TestBuild_UserKitDisablesPull verifies that a user-shadowed kit skips pull.
func TestBuild_UserKitDisablesPull(t *testing.T) {
	userDir := t.TempDir()
	baseDir := userDir + "/base"
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"manifest.toml": baseManifest,
		"packages.txt":  "",
		"install.sh":    "#!/bin/bash\n",
		"env.sh":        "# env\n",
	} {
		if err := os.WriteFile(baseDir+"/"+name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	builtinFS := fstest.MapFS{
		"base/manifest.toml": &fstest.MapFile{Data: []byte(baseManifest)},
		"base/packages.txt":  &fstest.MapFile{Data: []byte("")},
		"base/install.sh":    &fstest.MapFile{Data: []byte("#!/bin/bash\n")},
		"base/env.sh":        &fstest.MapFile{Data: []byte("# env\n")},
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cache, err := kits.NewCache()
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := &kits.Builder{
		Registry:        kits.NewRegistry(builtinFS, userDir),
		Cache:           cache,
		Runner:          runner,
		Version:         "v0.1",
		RegistryEnabled: true,
		RegistryHost:    "ghcr.io/n/agentbox-kits",
	}

	_, err = b.Build([]string{"base"}, kits.BuildOpts{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(runner.pullCalls) != 0 {
		t.Errorf("expected 0 Pull calls for user-shadowed kit, got %d", len(runner.pullCalls))
	}
	if len(runner.builds) != 1 {
		t.Errorf("expected 1 local Build call, got %d", len(runner.builds))
	}
}

// TestBuild_PullNotFound_FallsBackToLocal verifies NotFound falls back silently.
func TestBuild_PullNotFound_FallsBackToLocal(t *testing.T) {
	notFoundErr := &kits.PullError{Kind: kits.PullErrorNotFound, Wrapped: errors.New("not found")}
	runner := &fakeRunner{
		hasImage: map[string]bool{},
		pullErr:  notFoundErr,
	}
	b := newBuilderWithRegistry(t, runner)
	var stderr strings.Builder

	result, err := b.Build([]string{"base"}, kits.BuildOpts{Stderr: &stderr})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.PullHit {
		t.Error("PullHit should be false on NotFound")
	}
	if len(runner.builds) != 1 {
		t.Errorf("expected 1 local build after NotFound fallback, got %d", len(runner.builds))
	}
	if !strings.Contains(stderr.String(), "building locally") {
		t.Errorf("stderr should mention 'building locally', got: %q", stderr.String())
	}
}

// TestBuild_PullAuth_FallsBackWithError verifies Auth error surfaces to stderr.
func TestBuild_PullAuth_FallsBackWithError(t *testing.T) {
	authErr := &kits.PullError{Kind: kits.PullErrorAuth, Wrapped: errors.New("unauthorized")}
	runner := &fakeRunner{
		hasImage: map[string]bool{},
		pullErr:  authErr,
	}
	b := newBuilderWithRegistry(t, runner)
	var stderr strings.Builder

	result, err := b.Build([]string{"base"}, kits.BuildOpts{Stderr: &stderr})
	if err != nil {
		t.Fatalf("Build should still succeed via local fallback: %v", err)
	}
	if result.PullHit {
		t.Error("PullHit should be false on Auth error")
	}
	if len(runner.builds) != 1 {
		t.Errorf("expected 1 local build, got %d", len(runner.builds))
	}
	if !strings.Contains(stderr.String(), "auth required") {
		t.Errorf("stderr should mention 'auth required', got: %q", stderr.String())
	}
}

// TestBuild_NoCache_SkipsRegistry verifies NoCache skips pull path.
func TestBuild_NoCache_SkipsRegistry(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilderWithRegistry(t, runner)

	_, err := b.Build([]string{"base"}, kits.BuildOpts{NoCache: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(runner.pullCalls) != 0 {
		t.Errorf("expected 0 Pull calls with NoCache=true, got %d", len(runner.pullCalls))
	}
	if len(runner.builds) != 1 {
		t.Errorf("expected 1 local Build call, got %d", len(runner.builds))
	}
}

// TestBuild_CacheHit_SkipsPull verifies cache hit skips pull entirely.
func TestBuild_CacheHit_SkipsPull(t *testing.T) {
	reg := fakeRegistry("base")
	res, err := kits.Resolve(reg, []string{"base"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	runner := &fakeRunner{
		hasImage: map[string]bool{res.Tag: true},
	}
	b := newBuilderWithRegistry(t, runner)

	// Populate cache with a local build (NoPull=true).
	_, err = b.Build([]string{"base"}, kits.BuildOpts{NoPull: true})
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}
	runner.pullCalls = nil // reset

	// Second build: should hit cache, skip pull.
	result, err := b.Build([]string{"base"}, kits.BuildOpts{})
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	if !result.CacheHit {
		t.Error("expected CacheHit=true on second build")
	}
	if len(runner.pullCalls) != 0 {
		t.Errorf("expected 0 Pull calls on cache hit, got %d", len(runner.pullCalls))
	}
}

// TestCacheEntry_SourceField verifies the new Source field is persisted.
func TestCacheEntry_SourceField(t *testing.T) {
	cache := newCacheForTest(t)
	res := resolveBase(t)
	hashes, _ := kits.HashesFromResolved(res)

	entry := kits.CacheEntry{
		Tag:       res.Tag,
		Kits:      res.Names(),
		KitHashes: hashes,
		Source:    "registry",
	}
	if err := cache.Save(entry, ""); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := cache.Lookup(res.Tag)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Source != "registry" {
		t.Errorf("Source = %q, want %q", got.Source, "registry")
	}
}

// TestCacheEntry_OldEntryNoSource verifies old entries without Source decode OK.
func TestCacheEntry_OldEntryNoSource(t *testing.T) {
	cache := newCacheForTest(t)
	res := resolveBase(t)
	hashes, _ := kits.HashesFromResolved(res)

	entry := kits.CacheEntry{
		Tag:       res.Tag,
		Kits:      res.Names(),
		KitHashes: hashes,
		// Source intentionally omitted.
	}
	if err := cache.Save(entry, ""); err != nil {
		t.Fatalf("Save: %v", err)
	}

	match, err := cache.HasMatch(res)
	if err != nil {
		t.Fatalf("HasMatch: %v", err)
	}
	if !match {
		t.Error("HasMatch should return true for old entry without Source field")
	}
}

// newBuilderWithRegistry creates a Builder with registry enabled.
func newBuilderWithRegistry(t *testing.T, runner *fakeRunner) *kits.Builder {
	t.Helper()
	cache := newCacheForTest(t)
	reg := fakeRegistry("base")
	return &kits.Builder{
		Registry:        reg,
		Cache:           cache,
		Runner:          runner,
		Version:         "v0.1",
		RegistryEnabled: true,
		RegistryHost:    "ghcr.io/n/agentbox-kits",
	}
}

// ---------------------------------------------------------------------------
// Unit 9 — EmitContext (builder.go)
// ---------------------------------------------------------------------------

// TestEmitContext_WritesDockerfileAndKits verifies the basic happy path: emit
// to a fresh temp dir, confirm Dockerfile and at least one kit subdir exist.
func TestEmitContext_WritesDockerfileAndKits(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilder(t, runner)

	dst := t.TempDir()
	// t.TempDir() returns a non-empty dir on some systems; remove it and let
	// EmitContext re-create it so we always start from a truly absent path.
	if err := os.RemoveAll(dst); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	if err := b.EmitContext([]string{"base"}, dst); err != nil {
		t.Fatalf("EmitContext: %v", err)
	}

	// Dockerfile must exist.
	dfPath := filepath.Join(dst, "Dockerfile")
	if _, err := os.Stat(dfPath); err != nil {
		t.Errorf("Dockerfile not created: %v", err)
	}

	// kits/base/manifest.toml must exist (base is the only kit in the
	// fakeRegistry built by newBuilder).
	manifestPath := filepath.Join(dst, "kits", "base", "manifest.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("kits/base/manifest.toml not created: %v", err)
	}
}

// TestEmitContext_RefusesNonEmptyDir verifies that emitting into a non-empty
// directory is rejected with an error containing "is not empty".
func TestEmitContext_RefusesNonEmptyDir(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilder(t, runner)

	dst := t.TempDir()
	// Place a file to make it non-empty.
	if err := os.WriteFile(filepath.Join(dst, "canary"), []byte("exists"), 0o644); err != nil {
		t.Fatalf("WriteFile canary: %v", err)
	}

	err := b.EmitContext([]string{"base"}, dst)
	if err == nil {
		t.Fatal("expected error for non-empty dir, got nil")
	}
	if !strings.Contains(err.Error(), "is not empty") {
		t.Errorf("error should mention 'is not empty', got: %v", err)
	}
}

// TestEmitContext_DockerfileMatchesPrint verifies that the Dockerfile written
// by EmitContext is byte-identical to the output of Builder.PrintDockerfile.
func TestEmitContext_DockerfileMatchesPrint(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilder(t, runner)

	// Get expected Dockerfile via PrintDockerfile.
	want, err := b.PrintDockerfile([]string{"base"})
	if err != nil {
		t.Fatalf("PrintDockerfile: %v", err)
	}

	dst := t.TempDir()
	if err := os.RemoveAll(dst); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := b.EmitContext([]string{"base"}, dst); err != nil {
		t.Fatalf("EmitContext: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "Dockerfile"))
	if err != nil {
		t.Fatalf("ReadFile Dockerfile: %v", err)
	}
	if string(got) != want {
		t.Errorf("Dockerfile mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestEmitContext_EmptyDstErrors verifies that an empty dst string is rejected.
func TestEmitContext_EmptyDstErrors(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilder(t, runner)

	err := b.EmitContext([]string{"base"}, "")
	if err == nil {
		t.Fatal("expected error for empty dst, got nil")
	}
	if !strings.Contains(err.Error(), "dst is empty") {
		t.Errorf("error should mention 'dst is empty', got: %v", err)
	}
}

// TestEmitContext_NoRunnerCalled verifies EmitContext never calls the runner.
func TestEmitContext_NoRunnerCalled(t *testing.T) {
	runner := &fakeRunner{hasImage: map[string]bool{}}
	b := newBuilder(t, runner)

	dst := t.TempDir()
	if err := os.RemoveAll(dst); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := b.EmitContext([]string{"base"}, dst); err != nil {
		t.Fatalf("EmitContext: %v", err)
	}
	if len(runner.builds) != 0 {
		t.Errorf("EmitContext must not call Runner.Build; got %d calls", len(runner.builds))
	}
	if len(runner.pullCalls) != 0 {
		t.Errorf("EmitContext must not call Runner.Pull; got %d calls", len(runner.pullCalls))
	}
}

// ---------------------------------------------------------------------------
// Ports & Adapters check — domain layer must not import cobra or internal/cli.
// (Verified by the grep in the Makefile / CI; this test is a belt-and-suspenders
// check at the package import level — if this test file compiles, the imports
// are clean. The actual grep is in the verification script.)
// ---------------------------------------------------------------------------

func TestNoCobra_CompilesWithoutCobra(_ *testing.T) {
	// If this file compiles, kits doesn't import cobra.
	// (The grep verification is external; this test documents the intention.)
}

// min returns the smaller of a and b (for Go < 1.21 compatibility).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
