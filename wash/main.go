package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"dagger/wash/internal/dagger"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

const (
	workspaceDir      = "/workspace"
	outputDir         = "/out"
	washVersion       = "v2.5.2"
	cacheSchema       = "wash-v2"
	fallbackRustImage = "rust:1.95-bookworm"

	washArm64URL      = "https://github.com/wasmCloud/wasmCloud/releases/download/v2.5.2/wash-aarch64-unknown-linux-gnu"
	washArm64Checksum = "sha256:2c29cc651b87062d60c145942abce022c431710e398c47213f04d59062db4683"
	washAMD64URL      = "https://github.com/wasmCloud/wasmCloud/releases/download/v2.5.2/wash-x86_64-unknown-linux-gnu"
	washAMD64Checksum = "sha256:fef1e14a645144c84b4518ff5c907510b28dcd050576b80bd2c1d9d0dba6f02a"
)

// Wash builds and publishes wasmCloud components.
type Wash struct {
	Source         *dagger.Directory
	RootDir        string
	CacheNamespace string
	RustImage      string
}

// New creates a workspace-first wasmCloud component publisher.
func New(
	source *dagger.Workspace,
	// CacheNamespace is a stable caller-owned cache identity.
	cacheNamespace string,
	// +optional
	// +default="/"
	rootDir string,
	// +optional
	rustImage string,
) *Wash {
	return &Wash{
		Source:         source.Directory(rootDir, dagger.WorkspaceDirectoryOpts{Exclude: []string{".git", "target", "node_modules", ".dart_tool", "build", "dist"}, Gitignore: true}),
		RootDir:        rootDir,
		CacheNamespace: strings.TrimSpace(cacheNamespace),
		RustImage:      strings.TrimSpace(rustImage),
	}
}

type washConfig struct {
	Build struct {
		Command       string            `yaml:"command"`
		Env           map[string]string `yaml:"env"`
		ComponentPath string            `yaml:"component_path"`
	} `yaml:"build"`
	Wit struct {
		SkipFetch bool `yaml:"skip_fetch"`
	} `yaml:"wit"`
}

type rustVersion struct {
	Value     string
	Workspace bool
}

func (v *rustVersion) UnmarshalTOML(value any) error {
	switch value := value.(type) {
	case string:
		v.Value = value
		return nil
	case map[string]any:
		workspace, ok := value["workspace"].(bool)
		if !ok || !workspace {
			return fmt.Errorf("rust-version table must set workspace = true")
		}
		v.Workspace = true
		return nil
	default:
		return fmt.Errorf("rust-version must be a string or workspace table")
	}
}

type cargoManifest struct {
	Package struct {
		Name        string      `toml:"name"`
		RustVersion rustVersion `toml:"rust-version"`
	} `toml:"package"`
	Workspace struct {
		Members []string `toml:"members"`
		Package struct {
			RustVersion string `toml:"rust-version"`
		} `toml:"package"`
	} `toml:"workspace"`
}

type componentPlan struct {
	Dir, ID, ArtifactPath, WorkspaceRoot, PackageName, RustVersion string
	Config                                                         washConfig
	FastPath                                                       bool
}

type buildGroup struct {
	WorkspaceRoot, RustVersion, RustImage string
	Components                            []componentPlan
}

func resolveDirs(dirs []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || dir == "/" {
			dir = "."
		}
		if strings.HasPrefix(dir, "/") {
			dir = strings.TrimPrefix(path.Clean(dir), "/")
		} else {
			dir = path.Clean(dir)
		}
		if dir == ".." || strings.HasPrefix(dir, "../") {
			return nil, fmt.Errorf("component directory %q must be within the source root", dir)
		}
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *Wash) componentDirs(ctx context.Context, dirs []string) ([]string, error) {
	if len(dirs) > 0 {
		return resolveDirs(dirs)
	}
	matches, err := m.Source.Glob(ctx, "**/.wash/config.yaml")
	if err != nil {
		return nil, fmt.Errorf("discover components: %w", err)
	}
	root, err := m.Source.Glob(ctx, ".wash/config.yaml")
	if err != nil {
		return nil, fmt.Errorf("discover root component: %w", err)
	}
	matches = append(matches, root...)
	for i := range matches {
		matches[i] = path.Dir(path.Dir(matches[i]))
	}
	dirs, err = resolveDirs(matches)
	if err != nil {
		return nil, err
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no wasmCloud components found")
	}
	return dirs, nil
}

func (m *Wash) readManifest(ctx context.Context, manifestPath string) (cargoManifest, error) {
	contents, err := m.Source.File(manifestPath).Contents(ctx)
	if err != nil {
		return cargoManifest{}, err
	}
	var manifest cargoManifest
	if _, err := toml.Decode(contents, &manifest); err != nil {
		return manifest, fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	return manifest, nil
}

func (m *Wash) workspaceFor(ctx context.Context, componentDir string) (string, cargoManifest, cargoManifest, error) {
	packageManifest, err := m.readManifest(ctx, path.Join(componentDir, "Cargo.toml"))
	if err != nil {
		return "", cargoManifest{}, cargoManifest{}, err
	}
	for dir := componentDir; ; dir = path.Dir(dir) {
		manifestPath := path.Join(dir, "Cargo.toml")
		exists, existsErr := m.Source.Exists(ctx, manifestPath)
		if existsErr != nil {
			return "", cargoManifest{}, packageManifest, fmt.Errorf("check %s: %w", manifestPath, existsErr)
		}
		if exists {
			manifest, readErr := m.readManifest(ctx, manifestPath)
			if readErr != nil {
				return "", cargoManifest{}, packageManifest, readErr
			}
			if len(manifest.Workspace.Members) > 0 || dir == "." {
				return dir, manifest, packageManifest, nil
			}
		}
		if dir == "." {
			break
		}
	}
	return "", cargoManifest{}, packageManifest, fmt.Errorf("no Cargo workspace contains %s", componentDir)
}

func workspaceMember(workspaceRoot, componentDir string, members []string) bool {
	relative := strings.TrimPrefix(componentDir, workspaceRoot+"/")
	if workspaceRoot == "." {
		relative = strings.TrimPrefix(componentDir, "./")
	}
	if relative == "." {
		return true
	}
	for _, member := range members {
		matched, matchErr := path.Match(path.Clean(member), relative)
		if matchErr == nil && matched {
			return true
		}
	}
	return false
}

func relativeContained(base, value string) (string, error) {
	resolved := path.Clean(path.Join(base, value))
	if resolved == ".." || strings.HasPrefix(resolved, "../") || path.IsAbs(value) {
		return "", fmt.Errorf("path %q escapes source root", value)
	}
	return resolved, nil
}

func (m *Wash) resolveComponents(ctx context.Context, dirs []string) ([]componentPlan, error) {
	if m.CacheNamespace == "" {
		return nil, fmt.Errorf("cacheNamespace is required")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`).MatchString(m.CacheNamespace) {
		return nil, fmt.Errorf("invalid cacheNamespace %q", m.CacheNamespace)
	}
	resolved, err := m.componentDirs(ctx, dirs)
	if err != nil {
		return nil, err
	}
	plans := make([]componentPlan, 0, len(resolved))
	ids, artifacts := map[string]string{}, map[string]string{}
	for _, dir := range resolved {
		configPath := path.Join(dir, ".wash/config.yaml")
		contents, err := m.Source.File(configPath).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", configPath, err)
		}
		var cfg washConfig
		if err := yaml.Unmarshal([]byte(contents), &cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", configPath, err)
		}
		if strings.TrimSpace(cfg.Build.ComponentPath) == "" {
			return nil, fmt.Errorf("%s is missing build.component_path", configPath)
		}
		artifact, err := relativeContained(dir, cfg.Build.ComponentPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", configPath, err)
		}
		fallbackID := path.Base(dir)
		if dir == "." {
			fallbackID = strings.TrimSuffix(path.Base(artifact), path.Ext(artifact))
		}
		plan := componentPlan{Dir: dir, ID: fallbackID, ArtifactPath: artifact, Config: cfg}
		workspace, workspaceManifest, packageManifest, cargoErr := m.workspaceFor(ctx, dir)
		if cargoErr == nil && packageManifest.Package.Name != "" && workspaceMember(workspace, dir, workspaceManifest.Workspace.Members) {
			plan.WorkspaceRoot, plan.PackageName = workspace, packageManifest.Package.Name
			plan.RustVersion = packageManifest.Package.RustVersion.Value
			if packageManifest.Package.RustVersion.Workspace || plan.RustVersion == "" {
				plan.RustVersion = workspaceManifest.Workspace.Package.RustVersion
			}
			command := strings.Join(strings.Fields(cfg.Build.Command), " ")
			expectedArtifact := path.Join(workspace, "target/wasm32-wasip2/release", strings.ReplaceAll(plan.PackageName, "-", "_")+".wasm")
			plan.FastPath = cfg.Wit.SkipFetch && len(cfg.Build.Env) == 0 && command == "cargo build --target wasm32-wasip2 --release" && plan.RustVersion != "" && artifact == expectedArtifact
			if plan.FastPath {
				plan.ID = plan.PackageName
			}
		}
		if !namePattern.MatchString(plan.ID) || strings.Contains(plan.ID, "/") {
			return nil, fmt.Errorf("component %q has invalid OCI path segment ID %q", dir, plan.ID)
		}
		if old := ids[plan.ID]; old != "" {
			return nil, fmt.Errorf("components %q and %q have duplicate ID %q", old, dir, plan.ID)
		}
		if old := artifacts[path.Base(artifact)]; old != "" {
			return nil, fmt.Errorf("components %q and %q have duplicate artifact %q", old, dir, path.Base(artifact))
		}
		ids[plan.ID], artifacts[path.Base(artifact)] = dir, dir
		plans = append(plans, plan)
	}
	return plans, nil
}

func groupBuilds(plans []componentPlan, override string) []buildGroup {
	groups := map[string]*buildGroup{}
	for _, plan := range plans {
		if !plan.FastPath {
			continue
		}
		image := override
		if image == "" {
			image = "rust:" + plan.RustVersion + "-bookworm"
		}
		key := plan.WorkspaceRoot + "\x00" + plan.RustVersion + "\x00" + image
		if groups[key] == nil {
			groups[key] = &buildGroup{WorkspaceRoot: plan.WorkspaceRoot, RustVersion: plan.RustVersion, RustImage: image}
		}
		groups[key].Components = append(groups[key].Components, plan)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]buildGroup, 0, len(keys))
	for _, key := range keys {
		sort.Slice(groups[key].Components, func(i, j int) bool { return groups[key].Components[i].ID < groups[key].Components[j].ID })
		out = append(out, *groups[key])
	}
	return out
}

func cachePart(value string) string {
	return regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(value, "-")
}

func washRelease(platform string) (string, string, error) {
	parts := strings.Split(platform, "/")
	if len(parts) < 2 || parts[0] != "linux" {
		return "", "", fmt.Errorf("unsupported wash platform %q", platform)
	}
	switch parts[1] {
	case "arm64":
		return washArm64URL, washArm64Checksum, nil
	case "amd64":
		return washAMD64URL, washAMD64Checksum, nil
	default:
		return "", "", fmt.Errorf("unsupported wash platform %q", platform)
	}
}

func withWash(ctx context.Context, container *dagger.Container) (*dagger.Container, error) {
	platform, err := dag.DefaultPlatform(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve default platform: %w", err)
	}
	url, checksum, err := washRelease(string(platform))
	if err != nil {
		return nil, err
	}
	binary := dag.HTTP(url, dagger.HTTPOpts{Checksum: checksum, Permissions: 0o755})
	return container.WithFile("/usr/local/bin/wash", binary), nil
}

func rustBase(image string) *dagger.Container {
	return dag.Container().From(image).
		WithExec([]string{"sh", "-c", "apt-get update && apt-get install -y --no-install-recommends pkg-config libssl-dev && rm -rf /var/lib/apt/lists/*"}).
		WithExec([]string{"rustup", "target", "add", "wasm32-wasip2"})
}

func withCargoCaches(container *dagger.Container, rustVersion string) *dagger.Container {
	// Keep mutable Cargo caches after reproducible toolchain layers. Mounting
	// them earlier would force tool installation to execute on every call.
	return container.
		WithMountedCache("/usr/local/cargo/registry", dag.CacheVolume(cacheSchema+"/cargo-registry/"+cachePart(rustVersion)), dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModeShared}).
		WithMountedCache("/usr/local/cargo/git", dag.CacheVolume(cacheSchema+"/cargo-git/"+cachePart(rustVersion)), dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModeShared})
}

func cargoToolchain(image, rustVersion string) *dagger.Container {
	return withCargoCaches(rustBase(image), rustVersion)
}

func washToolchain(ctx context.Context, image, rustVersion string) (*dagger.Container, error) {
	container, err := withWash(ctx, rustBase(image))
	if err != nil {
		return nil, err
	}
	return withCargoCaches(container, rustVersion), nil
}

func publisherContainer(ctx context.Context) (*dagger.Container, error) {
	base := dag.Container().From("debian:bookworm-slim").WithExec([]string{"sh", "-c", "apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*"})
	return withWash(ctx, base)
}

func cargoBuildArgs(group buildGroup) []string {
	args := []string{"cargo", "build", "--locked", "--target", "wasm32-wasip2", "--release"}
	for _, component := range group.Components {
		args = append(args, "--package", component.PackageName)
	}
	return args
}

func fallbackTargetDir(component componentPlan) string {
	for dir := path.Dir(component.ArtifactPath); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if path.Base(dir) == "target" {
			return dir
		}
	}
	return path.Join(component.Dir, "target")
}

func fallbackTargetCacheKey(namespace string, component componentPlan) string {
	scope := component.WorkspaceRoot
	if scope == "" {
		scope = component.Dir
	}
	return strings.Join([]string{cacheSchema, "target", cachePart(namespace), cachePart(scope), cachePart(component.RustVersion), "wasm32-wasip2", "release"}, "/")
}

func (m *Wash) buildArtifacts(ctx context.Context, plans []componentPlan) (map[string]*dagger.File, error) {
	artifacts := map[string]*dagger.File{}
	for _, group := range groupBuilds(plans, m.RustImage) {
		targetCache := fmt.Sprintf("%s/target/%s/%s/%s/wasm32-wasip2/release", cacheSchema, cachePart(m.CacheNamespace), cachePart(group.WorkspaceRoot), cachePart(group.RustVersion))
		container := cargoToolchain(group.RustImage, group.RustVersion).
			WithDirectory(workspaceDir, m.Source).
			WithMountedCache(path.Join(workspaceDir, group.WorkspaceRoot, "target"), dag.CacheVolume(targetCache), dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModeLocked}).
			WithWorkdir(path.Join(workspaceDir, group.WorkspaceRoot)).WithExec(cargoBuildArgs(group))
		for _, component := range group.Components {
			out := path.Join(outputDir, component.ID+".wasm")
			container = container.WithExec([]string{"sh", "-c", fmt.Sprintf("mkdir -p %q && cp %q %q", outputDir, path.Join(workspaceDir, component.ArtifactPath), out)})
			artifacts[component.ID] = container.File(out)
		}
	}
	for _, component := range plans {
		if component.FastPath {
			continue
		}
		image := m.rustImageFor(component)
		container, err := washToolchain(ctx, image, component.RustVersion)
		if err != nil {
			return nil, err
		}
		targetDir := fallbackTargetDir(component)
		container = container.WithDirectory(workspaceDir, m.Source).
			WithMountedCache(path.Join(workspaceDir, targetDir), dag.CacheVolume(fallbackTargetCacheKey(m.CacheNamespace, component)), dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModeLocked}).
			WithWorkdir(path.Join(workspaceDir, component.Dir))
		args := []string{"wash", "build"}
		if component.Config.Wit.SkipFetch {
			args = append(args, "--skip-fetch")
		}
		out := path.Join(outputDir, component.ID+".wasm")
		container = container.WithExec(args).WithExec([]string{"sh", "-c", fmt.Sprintf("mkdir -p %q && cp %q %q", outputDir, path.Join(workspaceDir, component.ArtifactPath), out)})
		artifacts[component.ID] = container.File(out)
	}
	return artifacts, nil
}

// BuildComponents builds selected components, or discovers all components when omitted.
func (m *Wash) BuildComponents(ctx context.Context,
	// +optional
	// ComponentDirs limits the build; omitting it discovers all components.
	componentDirs []string,
) (*dagger.Directory, error) {
	plans, err := m.resolveComponents(ctx, componentDirs)
	if err != nil {
		return nil, err
	}
	artifacts, err := m.buildArtifacts(ctx, plans)
	if err != nil {
		return nil, err
	}
	out := dag.Directory()
	for _, plan := range plans {
		out = out.WithFile(plan.ID+".wasm", artifacts[plan.ID])
	}
	return out, nil
}

func (m *Wash) rustImageFor(plan componentPlan) string {
	if m.RustImage != "" {
		return m.RustImage
	}
	if plan.RustVersion != "" {
		return "rust:" + plan.RustVersion + "-bookworm"
	}
	return fallbackRustImage
}

var registryPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*)(?::[0-9]+)?$`)
var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)

func validatePublishInput(registry, repository, tag, username string, password *dagger.Secret, maxParallel int) error {
	if !registryPattern.MatchString(registry) || strings.Contains(registry, "://") {
		return fmt.Errorf("registry must be a hostname with optional port and no scheme")
	}
	if repository != "" && !namePattern.MatchString(strings.Trim(repository, "/")) {
		return fmt.Errorf("invalid repository %q", repository)
	}
	if tag != "" && !regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`).MatchString(tag) {
		return fmt.Errorf("invalid tag %q", tag)
	}
	if (strings.TrimSpace(username) == "") != (password == nil) {
		return fmt.Errorf("username and password must be provided together")
	}
	if maxParallel < 1 {
		return fmt.Errorf("maxParallel must be at least 1")
	}
	return nil
}

func refsFor(base, tag string) []string {
	if tag != "" && tag != "latest" {
		return []string{base + ":" + tag, base + ":latest"}
	}
	return []string{base + ":latest"}
}

type publishJob struct {
	Ref string
	Run func(context.Context) error
}
type publishResult struct {
	Ref string
	Err error
}

func runPublishJobs(ctx context.Context, jobs []publishJob, limit int) []publishResult {
	results := make([]publishResult, len(jobs))
	semaphore := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range jobs {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ctx.Err(); err != nil {
				results[i] = publishResult{jobs[i].Ref, err}
				return
			}
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				results[i] = publishResult{jobs[i].Ref, ctx.Err()}
				return
			}
			defer func() { <-semaphore }()
			results[i] = publishResult{jobs[i].Ref, jobs[i].Run(ctx)}
		}()
	}
	wg.Wait()
	return results
}

func formatPublishResults(results []publishResult) (string, error) {
	succeeded, failed := []string{}, []string{}
	for _, result := range results {
		if result.Err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", result.Ref, result.Err))
		} else {
			succeeded = append(succeeded, result.Ref)
		}
	}
	output := strings.Join(succeeded, "\n")
	if len(failed) == 0 {
		return output, nil
	}
	return output, fmt.Errorf("publication failures:\n%s\nsucceeded:\n%s", strings.Join(failed, "\n"), strings.Join(succeeded, "\n"))
}

// PublishComponents builds components and pushes an optional version followed by latest.
func (m *Wash) PublishComponents(ctx context.Context,
	// Registry is the required registry hostname with optional port. Though marked as optional to allow .env to supply it, it is required.
	// +optional
	registry string,
	// +optional
	// Repository is an optional repository prefix.
	repository string,
	// +optional
	// ComponentDirs limits publication; omitting it discovers all components.
	componentDirs []string,
	// +optional
	// RegistryService binds an optional in-session registry.
	registryService *dagger.Service,
	// +optional
	// Tag is pushed in addition to latest.
	tag string,
	// +optional
	// Username authenticates registry pushes when password is also supplied.
	username string,
	// +optional
	// Password authenticates registry pushes when username is also supplied.
	password *dagger.Secret,
	// +optional
	// Insecure allows an insecure registry connection.
	insecure bool,
	// +optional
	// +default=8
	maxParallel int,
) (string, error) {
	username = strings.TrimSpace(username)
	if err := validatePublishInput(registry, repository, tag, username, password, maxParallel); err != nil {
		return "", err
	}
	plans, err := m.resolveComponents(ctx, componentDirs)
	if err != nil {
		return "", err
	}
	artifacts, err := m.buildArtifacts(ctx, plans)
	if err != nil {
		return "", err
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", fmt.Errorf("create publish nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)
	jobs := []publishJob{}
	for _, plan := range plans {
		base := registry
		if repository != "" {
			base += "/" + strings.Trim(repository, "/")
		}
		base += "/" + plan.ID
		for _, ref := range refsFor(base, strings.TrimSpace(tag)) {
			ref, artifact := ref, artifacts[plan.ID]
			jobs = append(jobs, publishJob{Ref: ref, Run: func(ctx context.Context) error {
				c, err := publisherContainer(ctx)
				if err != nil {
					return err
				}
				c = c.WithFile("/artifact/component.wasm", artifact)
				if registryService != nil {
					alias := strings.Split(registry, ":")[0]
					c = c.WithServiceBinding(alias, registryService)
				}
				c = c.WithEnvVariable("_WASH_PUBLISH_NONCE", nonce).WithEnvVariable("WASH_REF", ref)
				script := `exec wash oci push "$WASH_REF" /artifact/component.wasm`
				if insecure {
					script = `exec wash oci push --insecure "$WASH_REF" /artifact/component.wasm`
				}
				if username != "" {
					c = c.WithEnvVariable("WASH_REG_USER", username).WithSecretVariable("WASH_REG_PASSWORD", password)
					script = `exec wash oci push -u "$WASH_REG_USER" -p "$WASH_REG_PASSWORD" "$WASH_REF" /artifact/component.wasm`
					if insecure {
						script = `exec wash oci push --insecure -u "$WASH_REG_USER" -p "$WASH_REG_PASSWORD" "$WASH_REF" /artifact/component.wasm`
					}
				}
				_, err = c.WithExec([]string{"sh", "-c", script}).Sync(ctx)
				return err
			}})
		}
	}
	return formatPublishResults(runPublishJobs(ctx, jobs, maxParallel))
}
