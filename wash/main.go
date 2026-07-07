package main

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"dagger/wash/internal/dagger"

	"gopkg.in/yaml.v3"
)

const (
	workspaceDir = "/workspace"
	artifactDir  = "/wash-artifacts"

	cargoRegistryCache = "wash-cargo-registry"
	cargoGitCache      = "wash-cargo-git"
)

// Wash is a reusable Dagger module for wasmCloud wash workflows.
// It provides a wash/Rust toolchain plus helpers to build and publish wasmCloud components.
type Wash struct {
	// Source is the repository or workspace root containing component directories.
	Source *dagger.Directory

	// WashVersion is the wash CLI version installed in the toolchain.
	WashVersion string

	// RustImage is the Rust toolchain base image.
	RustImage string
}

type washConfig struct {
	Build struct {
		ComponentPath string `yaml:"component_path"`
	} `yaml:"build"`
	Wit struct {
		SkipFetch bool `yaml:"skip_fetch"`
	} `yaml:"wit"`
}

// New creates a wasmCloud wash toolchain module.
func New(
	// Source is the repository or workspace root containing component directories.
	source *dagger.Workspace,

	// RootDir selects the source subdirectory mounted as /workspace.
	// +optional
	// +default="/"
	rootDir string,

	// WashVersion pins the wash CLI version installed in the toolchain.
	// +optional
	// +default="2.5.1"
	washVersion string,

	// RustImage is the base Rust toolchain image.
	// +optional
	// +default="rust:latest"
	rustImage string,
) *Wash {
	return &Wash{
		Source: source.Directory(rootDir, dagger.WorkspaceDirectoryOpts{
			Exclude:   []string{".git", "target", "node_modules", ".dart_tool", "build", "dist"},
			Gitignore: true,
		}),
		WashVersion: washVersion,
		RustImage:   rustImage,
	}
}

// Container returns a reusable wash/Rust toolchain container.
func (m *Wash) Container() *dagger.Container {
	return dag.Container().
		From(m.RustImage).
		WithMountedCache("/usr/local/cargo/registry", dag.CacheVolume(cargoRegistryCache)).
		WithMountedCache("/usr/local/cargo/git", dag.CacheVolume(cargoGitCache)).
		WithExec([]string{"sh", "-c", "apt-get update && apt-get install -y --no-install-recommends curl ca-certificates pkg-config libssl-dev && rm -rf /var/lib/apt/lists/*"}).
		WithExec([]string{"rustup", "target", "add", "wasm32-wasip2"}).
		WithEnvVariable("WASH_VERSION", normalizedWashVersion(m.WashVersion)).
		WithExec([]string{"sh", "-c", "curl -fsSL https://wasmcloud.com/sh | INSTALL_DIR=/usr/local/bin WASH_VERSION=\"$WASH_VERSION\" bash -s -- --no-modify-path"}).
		WithExec([]string{"wash", "--version"})
}

func (m *Wash) componentContainer(ctx context.Context, componentDir string) (*dagger.Container, error) {
	componentDir = cleanComponentDir(componentDir)

	cfg, err := m.loadConfig(ctx, componentDir)
	if err != nil {
		return nil, err
	}

	targetDir := targetCacheMountPath(componentDir, cfg.Build.ComponentPath)

	return m.Container().
		WithDirectory(workspaceDir, m.Source).
		WithMountedCache(targetDir, dag.CacheVolume(targetCacheKey(componentDir))).
		WithWorkdir(path.Join(workspaceDir, componentDir)), nil
}

func (m *Wash) loadConfig(ctx context.Context, componentDir string) (washConfig, error) {
	componentDir = cleanComponentDir(componentDir)
	configPath := path.Join(componentDir, ".wash", "config.yaml")
	contents, err := m.Source.File(configPath).Contents(ctx)
	if err != nil {
		return washConfig{}, fmt.Errorf("read %s: %w", configPath, err)
	}

	var cfg washConfig
	if err := yaml.Unmarshal([]byte(contents), &cfg); err != nil {
		return washConfig{}, fmt.Errorf("parse %s: %w", configPath, err)
	}

	return cfg, nil
}

func resolveArtifactPath(componentDir string, componentPath string) string {
	componentDir = cleanComponentDir(componentDir)
	return path.Clean(path.Join(workspaceDir, componentDir, componentPath))
}

func targetCacheMountPath(componentDir string, componentPath string) string {
	componentDir = cleanComponentDir(componentDir)
	artifactPath := resolveArtifactPath(componentDir, componentPath)

	for dir := path.Dir(artifactPath); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if path.Base(dir) == "target" {
			return dir
		}
	}

	return path.Join(workspaceDir, componentDir, "target")
}

func (m *Wash) artifactPath(ctx context.Context, componentDir string) (string, error) {
	componentDir = cleanComponentDir(componentDir)
	configPath := path.Join(componentDir, ".wash", "config.yaml")
	cfg, err := m.loadConfig(ctx, componentDir)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(cfg.Build.ComponentPath) == "" {
		return "", fmt.Errorf("%s is missing build.component_path", configPath)
	}

	return resolveArtifactPath(componentDir, cfg.Build.ComponentPath), nil
}

func (m *Wash) buildArgs(ctx context.Context, componentDir string) ([]string, error) {
	cfg, err := m.loadConfig(ctx, componentDir)
	if err != nil {
		return nil, err
	}

	args := []string{"wash", "build"}
	if cfg.Wit.SkipFetch {
		args = append(args, "--skip-fetch")
	}
	return args, nil
}

func (m *Wash) buildContainer(ctx context.Context, componentDir string) (*dagger.Container, string, error) {
	componentDir = cleanComponentDir(componentDir)

	artifactPath, err := m.artifactPath(ctx, componentDir)
	if err != nil {
		return nil, "", err
	}

	buildArgs, err := m.buildArgs(ctx, componentDir)
	if err != nil {
		return nil, "", err
	}

	outputPath := path.Join(artifactDir, path.Base(artifactPath))
	copyArtifact := fmt.Sprintf("mkdir -p %q && cp %q %q", path.Dir(outputPath), artifactPath, outputPath)

	c, err := m.componentContainer(ctx, componentDir)
	if err != nil {
		return nil, "", err
	}

	c = c.
		WithExec(buildArgs).
		WithExec([]string{"sh", "-c", copyArtifact})

	return c, outputPath, nil
}

func (m *Wash) resolveComponentDirs(ctx context.Context, componentDirs []string) ([]string, error) {
	if len(componentDirs) > 0 {
		resolved := make([]string, 0, len(componentDirs))
		for _, componentDir := range componentDirs {
			resolved = append(resolved, cleanComponentDir(componentDir))
		}
		return resolved, nil
	}

	matches, err := m.Source.Glob(ctx, "**/.wash/config.yaml")
	if err != nil {
		return nil, err
	}
	rootMatches, err := m.Source.Glob(ctx, ".wash/config.yaml")
	if err != nil {
		return nil, err
	}
	matches = append(matches, rootMatches...)

	dirsByName := map[string]struct{}{}
	for _, match := range matches {
		dir := cleanComponentDir(path.Dir(path.Dir(match)))
		dirsByName[dir] = struct{}{}
	}
	if len(dirsByName) == 0 {
		return nil, fmt.Errorf("no wasmCloud components found; expected at least one **/.wash/config.yaml")
	}

	dirs := make([]string, 0, len(dirsByName))
	for dir := range dirsByName {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs, nil
}

// Build runs wash build for a component and returns the built wasm artifact.
func (m *Wash) Build(
	ctx context.Context,
	// ComponentDir is the component directory relative to the module source root.
	// +optional
	// +default="."
	componentDir string,
) (*dagger.File, error) {
	c, outputPath, err := m.buildContainer(ctx, componentDir)
	if err != nil {
		return nil, err
	}

	return c.File(outputPath), nil
}

// BuildComponents builds multiple component directories and returns all wasm artifacts.
// If componentDirs is empty, components are discovered by **/.wash/config.yaml.
func (m *Wash) BuildComponents(
	ctx context.Context,
	// ComponentDirs are component directories relative to the module source root.
	// If empty, components are auto-discovered from **/.wash/config.yaml.
	// +optional
	componentDirs []string,
) (*dagger.Directory, error) {
	resolvedDirs, err := m.resolveComponentDirs(ctx, componentDirs)
	if err != nil {
		return nil, err
	}

	out := dag.Directory()
	for _, componentDir := range resolvedDirs {
		artifact, err := m.Build(ctx, componentDir)
		if err != nil {
			return nil, err
		}
		out = out.WithFile(path.Base(componentDir)+".wasm", artifact)
	}

	return out, nil
}

// Publish builds and pushes one wasmCloud component OCI artifact.
func (m *Wash) Publish(
	ctx context.Context,
	// ComponentDir is the component directory relative to the module source root.
	// +optional
	// +default="."
	componentDir string,

	// Registry is the OCI registry host, e.g. ghcr.io or localhost:5000.
	// +optional
	registry *dagger.Service,

	// Registry hostname that the client should use
	// +optional
	registryHostname string,

	// Repository is an optional path below the registry, e.g. seamlezz/wasmcloud-smoke.
	// +optional
	repository string,

	// ComponentName is the image name. Defaults to the component directory basename.
	// +optional
	componentName string,

	// Tag is an optional additional tag. latest is always pushed.
	// +optional
	tag string,

	// Username is optional OCI basic auth username.
	// +optional
	username string,

	// Password is optional OCI basic auth password/token.
	// +optional
	password *dagger.Secret,

	// Insecure allows pushing to an HTTP/insecure registry.
	// +optional
	// +default=false
	insecure bool,
) (string, error) {
	componentDir = cleanComponentDir(componentDir)
	if strings.TrimSpace(componentName) == "" {
		componentName = path.Base(componentDir)
	}

	if registryHostname == "" {
		var err error
		registryHostname, err = registry.Endpoint(ctx)
		if err != nil {
			return "", err
		}
	}

	base, err := imageBase(ctx, registryHostname, repository, componentName)
	if err != nil {
		return "", err
	}
	if err := validateCredentials(username, password); err != nil {
		return "", err
	}

	refs := refsFor(base, tag)
	c, artifactPath, err := m.buildContainer(ctx, componentDir)
	if err != nil {
		return "", err
	}

	c = c.WithServiceBinding(registryHostname, registry)
	c = publishRefs(c, refs, artifactPath, username, password, insecure)

	if _, err := c.Sync(ctx); err != nil {
		return "", err
	}

	return strings.Join(refs, "\n"), nil
}

// PublishComponents builds and pushes multiple components using directory basenames as image names.
// If componentDirs is empty, components are discovered by **/.wash/config.yaml.
func (m *Wash) PublishComponents(
	ctx context.Context,
	// ComponentDirs are component directories relative to the module source root.
	// If empty, components are auto-discovered from **/.wash/config.yaml.
	// +optional
	componentDirs []string,

	// Registry is the OCI registry host, e.g. ghcr.io or localhost:5000.
	// +optional
	registry *dagger.Service,

	// Registry hostname that the client should use
	// +optional
	registryHostname string,

	// Repository is an optional path below the registry, e.g. seamlezz/wasmcloud-smoke.
	// +optional
	repository string,

	// Tag is an optional additional tag. latest is always pushed.
	// +optional
	tag string,

	// Username is optional OCI basic auth username.
	// +optional
	username string,

	// Password is optional OCI basic auth password/token.
	// +optional
	password *dagger.Secret,

	// Insecure allows pushing to an HTTP/insecure registry.
	// +optional
	// +default=false
	insecure bool,
) (string, error) {
	resolvedDirs, err := m.resolveComponentDirs(ctx, componentDirs)
	if err != nil {
		return "", err
	}

	var pushed []string
	for _, componentDir := range resolvedDirs {
		refs, err := m.Publish(ctx, componentDir, registry, registryHostname, repository, path.Base(componentDir), tag, username, password, insecure)
		if err != nil {
			return strings.Join(pushed, "\n"), err
		}
		pushed = append(pushed, refs)
	}

	return strings.Join(pushed, "\n"), nil
}

func normalizedWashVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || strings.HasPrefix(version, "v") || strings.HasPrefix(version, "wash-v") {
		return version
	}
	if version[0] >= '0' && version[0] <= '9' {
		return "v" + version
	}
	return version
}

func cleanComponentDir(componentDir string) string {
	componentDir = strings.TrimSpace(componentDir)
	if componentDir == "" || componentDir == "/" {
		return "."
	}
	return strings.TrimPrefix(path.Clean(componentDir), "/")
}

func targetCacheKey(componentDir string) string {
	componentDir = cleanComponentDir(componentDir)
	if componentDir == "." {
		return "wash-target-root"
	}

	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "_")
	return "wash-target-" + replacer.Replace(componentDir)
}

func imageBase(
	ctx context.Context,
	registry string,
	repository, componentName string,
) (string, error) {
	registry = strings.Trim(strings.TrimSpace(registry), "/")
	repository = strings.Trim(strings.TrimSpace(repository), "/")
	componentName = strings.Trim(strings.TrimSpace(componentName), "/")

	if registry == "" {
		return "", fmt.Errorf("registry is required")
	}
	if componentName == "" || componentName == "." {
		return "", fmt.Errorf("componentName is required when componentDir has no basename")
	}

	url := registry
	if repository != "" {
		url += "/" + repository
	}
	url += "/" + componentName
	return url, nil
}

func refsFor(base, tag string) []string {
	refs := []string{base + ":latest"}
	tag = strings.TrimSpace(tag)
	if tag != "" && tag != "latest" {
		refs = append([]string{base + ":" + tag}, refs...)
	}
	return refs
}

func validateCredentials(username string, password *dagger.Secret) error {
	if strings.TrimSpace(username) == "" && password != nil {
		return fmt.Errorf("username is required when password is provided")
	}
	if strings.TrimSpace(username) != "" && password == nil {
		return fmt.Errorf("password secret is required when username is provided")
	}
	return nil
}

func publishRefs(
	c *dagger.Container,
	refs []string,
	artifactPath string,
	username string,
	password *dagger.Secret,
	insecure bool,
) *dagger.Container {
	for _, ref := range refs {
		args := []string{"wash", "oci", "push"}
		if insecure {
			args = append(args, "--insecure")
		}

		if username != "" && password != nil {
			c = c.
				WithEnvVariable("WASH_REG_USER", username).
				WithSecretVariable("WASH_REG_PASSWORD", password)
			script := fmt.Sprintf(
				"%s -u \"$WASH_REG_USER\" -p \"$WASH_REG_PASSWORD\" %q %q",
				strings.Join(args, " "),
				ref,
				artifactPath,
			)
			c = c.WithExec([]string{"sh", "-c", script})
			continue
		}

		args = append(args, ref, artifactPath)
		c = c.WithExec(args)
	}
	return c
}
