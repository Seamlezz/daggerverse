package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

func TestResolveDirsNormalizesSortsAndDeduplicates(t *testing.T) {
	got, err := resolveDirs([]string{"backend/z", "/backend/a/", "backend/z"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"backend/a", "backend/z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolveDirsPermitsRootAndRejectsTraversal(t *testing.T) {
	got, err := resolveDirs([]string{"."})
	if err != nil || !reflect.DeepEqual(got, []string{"."}) {
		t.Fatalf("root resolved as %v, %v", got, err)
	}
	if _, err := resolveDirs([]string{"../component"}); err == nil {
		t.Fatal("accepted traversal")
	}
}

func TestWorkspaceMemberExactAndGlob(t *testing.T) {
	members := []string{"components/exact", "crates/*"}
	for _, component := range []string{"components/exact", "crates/globbed"} {
		if !workspaceMember(".", component, members) {
			t.Errorf("%q was not a member", component)
		}
	}
	if workspaceMember(".", "other/component", members) {
		t.Fatal("accepted non-member")
	}
}

func TestFallbackTargetDir(t *testing.T) {
	if got := fallbackTargetDir(componentPlan{Dir: "components/a", ArtifactPath: "target/custom/a.wasm"}); got != "target" {
		t.Fatalf("nearest target = %q", got)
	}
	if got := fallbackTargetDir(componentPlan{Dir: "components/a", ArtifactPath: "components/a/dist/a.wasm"}); got != "components/a/target" {
		t.Fatalf("local target = %q", got)
	}
}

func TestWashRelease(t *testing.T) {
	if url, checksum, err := washRelease("linux/arm64/v8"); err != nil || url != washArm64URL || checksum != washArm64Checksum {
		t.Fatalf("arm64: %q %q %v", url, checksum, err)
	}
	if _, _, err := washRelease("linux/riscv64"); err == nil {
		t.Fatal("accepted unsupported architecture")
	}
}

func TestRelativeContained(t *testing.T) {
	if got, err := relativeContained("components/a", "../../target/a.wasm"); err != nil || got != "target/a.wasm" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := relativeContained("a", "../../secret"); err == nil {
		t.Fatal("accepted escaping path")
	}
}

func TestRefsForVersionThenLatest(t *testing.T) {
	want := []string{"example.test/repo/a:v1", "example.test/repo/a:latest"}
	if got := refsFor("example.test/repo/a", "v1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if got := refsFor("example.test/repo/a", "latest"); !reflect.DeepEqual(got, want[1:]) {
		t.Fatalf("got %v", got)
	}
}

func TestPublishValidation(t *testing.T) {
	for _, registry := range []string{"", "https://example.test", "bad/path"} {
		if err := validatePublishInput(registry, "", "", "", nil, 1); err == nil {
			t.Errorf("accepted registry %q", registry)
		}
	}
	if err := validatePublishInput("example.test:5000", "team/components", "v1", "", nil, 8); err != nil {
		t.Fatal(err)
	}
	if err := validatePublishInput("example.test", "", "", "user", nil, 8); err == nil {
		t.Fatal("accepted incomplete credentials")
	}
	if err := validatePublishInput("example.test", "", "", "", nil, 0); err == nil {
		t.Fatal("accepted zero parallelism")
	}
}

func TestTypewriterWorkspaceRustVersionIsInheritedAndGrouped(t *testing.T) {
	var workspace, member cargoManifest
	if _, err := toml.Decode(`[workspace]
members = ["standard"]
[workspace.package]
rust-version = "1.95.0"`, &workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := toml.Decode(`[package]
name = "standard"
rust-version = { workspace = true }`, &member); err != nil {
		t.Fatal(err)
	}
	var cfg washConfig
	if err := yaml.Unmarshal([]byte("build:\n  command: cargo build --target wasm32-wasip2 --release\n  component_path: ../../target/wasm32-wasip2/release/standard.wasm\nwit:\n  skip_fetch: true\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	version := member.Package.RustVersion.Value
	if member.Package.RustVersion.Workspace {
		version = workspace.Workspace.Package.RustVersion
	}
	artifact, err := relativeContained("components/standard", cfg.Build.ComponentPath)
	if err != nil {
		t.Fatal(err)
	}
	plan := componentPlan{ID: member.Package.Name, PackageName: member.Package.Name, WorkspaceRoot: ".", RustVersion: version, Config: cfg, ArtifactPath: artifact}
	plan.FastPath = cfg.Wit.SkipFetch && strings.Join(strings.Fields(cfg.Build.Command), " ") == "cargo build --target wasm32-wasip2 --release" && strings.HasPrefix(artifact, "target/wasm32-wasip2/release/")
	groups := groupBuilds([]componentPlan{plan}, "")
	if version != "1.95.0" || !plan.FastPath || len(groups) != 1 || groups[0].RustImage != "rust:1.95.0-bookworm" {
		t.Fatalf("version=%q fast=%v groups=%v", version, plan.FastPath, fmt.Sprint(groups))
	}
}

func TestCargoBuildArgsSelectsEveryPackageOnce(t *testing.T) {
	group := buildGroup{Components: []componentPlan{{PackageName: "a"}, {PackageName: "b"}}}
	got := strings.Join(cargoBuildArgs(group), " ")
	if got != "cargo build --locked --target wasm32-wasip2 --release --package a --package b" {
		t.Fatalf("unexpected command %q", got)
	}
}

func TestRunPublishJobsIsBoundedAndAttemptsAll(t *testing.T) {
	var active, maximum, attempted atomic.Int32
	jobs := make([]publishJob, 6)
	for i := range jobs {
		jobs[i] = publishJob{Ref: string(rune('a' + i)), Run: func(context.Context) error {
			attempted.Add(1)
			current := active.Add(1)
			defer active.Add(-1)
			for {
				old := maximum.Load()
				if current <= old || maximum.CompareAndSwap(old, current) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			return errors.New("failed")
		}}
	}
	results := runPublishJobs(context.Background(), jobs, 2)
	if attempted.Load() != 6 || len(results) != 6 {
		t.Fatalf("attempted %d jobs", attempted.Load())
	}
	if maximum.Load() != 2 {
		t.Fatalf("maximum concurrency %d", maximum.Load())
	}
}

func TestFormatPublishResultsAggregatesInOrder(t *testing.T) {
	output, err := formatPublishResults([]publishResult{{Ref: "a"}, {Ref: "b", Err: errors.New("one")}, {Ref: "c", Err: errors.New("two")}})
	if output != "a" {
		t.Fatalf("output %q", output)
	}
	if err == nil || !strings.Contains(err.Error(), "b: one\nc: two") || !strings.Contains(err.Error(), "succeeded:\na") {
		t.Fatalf("error %v", err)
	}
}

func TestRunPublishJobsRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	var mu sync.Mutex
	results := runPublishJobs(ctx, []publishJob{{Ref: "a", Run: func(context.Context) error { mu.Lock(); calls++; mu.Unlock(); return nil }}}, 1)
	if calls != 0 || !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("result %#v, calls %d", results[0], calls)
	}
}
