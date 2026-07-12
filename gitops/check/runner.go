package check

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"dagger/gitops/internal/dagger"
)

const manifestDir = "/tmp/manifests"

// ContainerFactory is a function that creates a new container.
type ContainerFactory func(opts ...dagger.ContainerOpts) *dagger.Container

// Config holds all configuration for the check runner.
type Config struct {
	// ClusterDir is the root directory containing Flux cluster definitions.
	ClusterDir string

	// Clusters overrides auto-discovered Flux cluster names.
	Clusters []string

	// TerraformEnvs overrides auto-discovered Terraform environment directories.
	TerraformEnvs []string

	// HelmSourceDirs overrides auto-discovered Helm source directories.
	HelmSourceDirs []string

	// OCISourceDirs overrides auto-discovered OCI source directories.
	OCISourceDirs []string

	// GitSourceDirs overrides auto-discovered Git source directories.
	GitSourceDirs []string

	// KubeVersion is the Kubernetes version used for helm template --kube-version.
	KubeVersion string

	// KubeconformSkips are resource kinds to skip during kubeconform validation.
	KubeconformSkips []string

	// KubeconformIgnores are filename patterns to ignore during kubeconform validation.
	KubeconformIgnores []string
}

// DefaultConfig returns sensible defaults for all config fields.
func DefaultConfig() Config {
	return Config{
		ClusterDir:         "clusters",
		KubeVersion:        "1.33.0",
		KubeconformSkips:   []string{"CustomResourceDefinition", "VaultDynamicSecret"},
		KubeconformIgnores: []string{"gotk-components", "gotk-sync"},
	}
}

// Runner executes check functions inside Dagger containers.
type Runner struct {
	newContainer ContainerFactory
	scripts      Scripts
	versions     ToolVersions
	config       Config
}

// NewRunner creates a new Runner.
func NewRunner(newContainer ContainerFactory, fsys fs.FS, scriptDir string, versions ToolVersions, config Config) Runner {
	scripts, err := loadScripts(fsys, scriptDir)
	if err != nil {
		panic(err)
	}
	return Runner{
		newContainer: newContainer,
		scripts:      scripts,
		versions:     versions,
		config:       config,
	}
}

// resolveClusters returns configured clusters or auto-discovers them.
func (r Runner) resolveClusters(ctx context.Context, source *dagger.Directory) ([]string, error) {
	return resolveClusters(ctx, source, r.config.ClusterDir, r.config.Clusters)
}

// discoverTerraformEnvs finds directories containing Terraform root modules.
// A root module is identified by the presence of .tf files.
func discoverTerraformEnvs(ctx context.Context, source *dagger.Directory) ([]string, error) {
	matches, err := source.Glob(ctx, "**/*.tf")
	if err != nil {
		return nil, err
	}

	// Extract unique directories from matched .tf files
	dirs := map[string]struct{}{}
	for _, m := range matches {
		dir := dirOf(m)
		if dir == "." {
			continue
		}
		dirs[dir] = struct{}{}
	}

	var out []string
	for d := range dirs {
		out = append(out, d)
	}
	return out, nil
}

// dirOf returns the directory portion of a file path.
func dirOf(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return "."
	}
	return p[:idx]
}

// resolveTerraformEnvs returns configured envs or auto-discovers them.
func (r Runner) resolveTerraformEnvs(ctx context.Context, source *dagger.Directory) ([]string, error) {
	if len(r.config.TerraformEnvs) > 0 {
		return r.config.TerraformEnvs, nil
	}
	return discoverTerraformEnvs(ctx, source)
}

// discoverHelmSourceDirs finds directories containing HelmRepository resources.
func discoverHelmSourceDirs(ctx context.Context, source *dagger.Directory) ([]string, error) {
	return discoverSourceDirs(ctx, source, "HelmRepository")
}

// discoverOCISourceDirs finds directories containing OCIRepository resources.
func discoverOCISourceDirs(ctx context.Context, source *dagger.Directory) ([]string, error) {
	return discoverSourceDirs(ctx, source, "OCIRepository")
}

// discoverGitSourceDirs finds directories containing HelmChart resources (Git-based).
func discoverGitSourceDirs(ctx context.Context, source *dagger.Directory) ([]string, error) {
	return discoverSourceDirs(ctx, source, "HelmChart")
}

// discoverSourceDirs finds directories containing YAML files with a specific Flux source kind.
func discoverSourceDirs(ctx context.Context, source *dagger.Directory, kind string) ([]string, error) {
	matches, err := source.Glob(ctx, "**/*.yaml")
	if err != nil {
		return nil, err
	}

	dirs := map[string]struct{}{}
	for _, m := range matches {
		content, err := source.File(m).Contents(ctx)
		if err != nil {
			continue
		}
		if strings.Contains(content, "kind: "+kind) {
			dirs[dirOf(m)] = struct{}{}
		}
	}

	var out []string
	for d := range dirs {
		out = append(out, d)
	}
	return out, nil
}

// resolveHelmSourceDirs returns configured dirs or auto-discovers them.
func (r Runner) resolveHelmSourceDirs(ctx context.Context, source *dagger.Directory) ([]string, error) {
	if len(r.config.HelmSourceDirs) > 0 {
		return r.config.HelmSourceDirs, nil
	}
	return discoverHelmSourceDirs(ctx, source)
}

// resolveOCISourceDirs returns configured dirs or auto-discovers them.
func (r Runner) resolveOCISourceDirs(ctx context.Context, source *dagger.Directory) ([]string, error) {
	if len(r.config.OCISourceDirs) > 0 {
		return r.config.OCISourceDirs, nil
	}
	return discoverOCISourceDirs(ctx, source)
}

// resolveGitSourceDirs returns configured dirs or auto-discovers them.
func (r Runner) resolveGitSourceDirs(ctx context.Context, source *dagger.Directory) ([]string, error) {
	if len(r.config.GitSourceDirs) > 0 {
		return r.config.GitSourceDirs, nil
	}
	return discoverGitSourceDirs(ctx, source)
}

func (r Runner) CheckKustomizeBuild(ctx context.Context, source *dagger.Directory) error {
	clusters, err := r.resolveClusters(ctx, source)
	if err != nil {
		return err
	}
	paths, err := discoverFluxKustomizePaths(ctx, source, r.config.ClusterDir, clusters)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}

	c := buildKustomizePaths(r.withCheckRepo(r.kustomizeToolchain(), source), paths)
	_, err = c.WithExec([]string{"sh", "-c", "echo '[check] kustomize build passed'"}).Sync(ctx)
	return err
}

func (r Runner) CheckKubeconform(ctx context.Context, source *dagger.Directory, googleCredentials *dagger.Secret) error {
	clusters, err := r.resolveClusters(ctx, source)
	if err != nil {
		return err
	}
	paths, err := discoverFluxKustomizePaths(ctx, source, r.config.ClusterDir, clusters)
	if err != nil {
		return err
	}

	c := r.kubeconformToolchain()
	c = mountGoogleCredentials(c, googleCredentials)
	c = r.withCheckRepo(c, source)
	c = buildKustomizePaths(c, paths)
	c = r.withOptionalGhcrLogin(c)
	c = c.WithExec([]string{"mkdir", "-p", "/tmp/schemas/flux", "/tmp/schemas/helm"})

	// Extract Flux CRDs from gotk-components.yaml if it exists
	c = c.WithEnvVariable("CLUSTER_DIR", r.config.ClusterDir)
	c = c.WithExec([]string{"sh", "-c",
		"for f in \"/src/$CLUSTER_DIR\"/*/flux-system/gotk-components.yaml; do " +
			"if [ -f \"$f\" ]; then python3 /usr/local/bin/extract-crd-schemas.py /tmp/schemas/flux \"$f\"; fi; " +
			"done; exit 0"})

	// Set source dirs as env vars for scripts
	helmDirs, _ := r.resolveHelmSourceDirs(ctx, source)
	ociDirs, _ := r.resolveOCISourceDirs(ctx, source)
	gitDirs, _ := r.resolveGitSourceDirs(ctx, source)
	c = c.WithEnvVariable("HELM_SOURCE_DIRS", strings.Join(helmDirs, ":"))
	c = c.WithEnvVariable("OCI_SOURCE_DIRS", strings.Join(ociDirs, ":"))
	c = c.WithEnvVariable("GIT_SOURCE_DIRS", strings.Join(gitDirs, ":"))

	// Set kubeconform config
	c = c.WithEnvVariable("KUBECONFORM_SKIPS", strings.Join(r.config.KubeconformSkips, ","))
	c = c.WithEnvVariable("KUBECONFORM_IGNORES", strings.Join(r.config.KubeconformIgnores, "|"))

	c = r.withScript(c, "helm-source-resolver.sh")
	c = r.execScript(c, "fetch-helm-crds.sh")
	c = r.execScript(c, "fetch-eso-crds.sh")
	c = r.execScript(c, "kubeconform-validate.sh")
	_, err = c.Sync(ctx)
	return err
}

func (r Runner) CheckFluxIntegrity(ctx context.Context, source *dagger.Directory) error {
	clusters, err := r.resolveClusters(ctx, source)
	if err != nil {
		return err
	}
	return validateFluxIntegrity(ctx, source, r.config.ClusterDir, clusters)
}

func (r Runner) CheckSopsDecrypt(ctx context.Context, source *dagger.Directory, googleCredentials *dagger.Secret) error {
	files, err := globEncryptedSecrets(ctx, source)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}

	c, err := mountRequiredGoogleCredentials(r.sopsToolchain(), googleCredentials)
	if err != nil {
		return err
	}
	c = r.withCheckRepo(c, source)
	c = c.WithNewFile("/tmp/enc-files.txt", strings.Join(files, "\n")+"\n")
	c = r.execScript(c, "sops-decrypt.sh")
	_, err = c.Sync(ctx)
	return err
}

func (r Runner) CheckTerraform(ctx context.Context, source *dagger.Directory) error {
	envs, err := r.resolveTerraformEnvs(ctx, source)
	if err != nil {
		return err
	}
	if len(envs) == 0 {
		return fmt.Errorf("no Terraform environments discovered")
	}

	c := r.withCheckRepo(r.terraformToolchain(), source)
	c = r.withScript(c, "terraform-validate.sh")
	for _, env := range envs {
		c = c.WithExec([]string{scriptPath("terraform-validate.sh"), env})
	}
	_, err = c.WithExec([]string{"sh", "-c", "echo '[check] terraform passed'"}).Sync(ctx)
	return err
}

func (r Runner) CheckYamlLint(ctx context.Context, source *dagger.Directory) error {
	files, err := globYAMLFiles(ctx, source)
	if err != nil {
		return err
	}

	c := r.yamllintToolchain()
	c = r.withCheckRepo(c, source)
	c = c.WithNewFile("/tmp/.yamllint", r.scripts.get("yamllint.yml"))
	c = c.WithNewFile("/tmp/yaml-files.txt", strings.Join(files, "\n")+"\n")
	c = r.execScript(c, "yamllint.sh")
	_, err = c.Sync(ctx)
	return err
}

func (r Runner) CheckHelmReleases(ctx context.Context, source *dagger.Directory, googleCredentials *dagger.Secret) error {
	clusters, err := r.resolveClusters(ctx, source)
	if err != nil {
		return err
	}
	paths, err := discoverFluxKustomizePaths(ctx, source, r.config.ClusterDir, clusters)
	if err != nil {
		return err
	}

	c := r.helmToolchain()
	c = mountGoogleCredentials(c, googleCredentials)
	c = r.withCheckRepo(c, source)
	c = buildKustomizePaths(c, paths)
	c = r.withOptionalGhcrLogin(c)

	// Set source dirs and kube version as env vars for scripts
	helmDirs, _ := r.resolveHelmSourceDirs(ctx, source)
	ociDirs, _ := r.resolveOCISourceDirs(ctx, source)
	gitDirs, _ := r.resolveGitSourceDirs(ctx, source)
	c = c.WithEnvVariable("HELM_SOURCE_DIRS", strings.Join(helmDirs, ":"))
	c = c.WithEnvVariable("OCI_SOURCE_DIRS", strings.Join(ociDirs, ":"))
	c = c.WithEnvVariable("GIT_SOURCE_DIRS", strings.Join(gitDirs, ":"))
	c = c.WithEnvVariable("KUBE_VERSION", r.config.KubeVersion)

	c = r.withScript(c, "helm-source-resolver.sh")
	c = r.execScript(c, "helm-template.sh")
	_, err = c.Sync(ctx)
	return err
}

func mountRequiredGoogleCredentials(c *dagger.Container, googleCredentials *dagger.Secret) (*dagger.Container, error) {
	if googleCredentials == nil {
		return nil, fmt.Errorf("googleCredentials secret is required; set a Dagger local default like CHECK_SOPS_DECRYPT_GOOGLE_CREDENTIALS=file://$HOME/.config/gcloud/application_default_credentials.json")
	}
	return mountGoogleCredentials(c, googleCredentials), nil
}

func mountGoogleCredentials(c *dagger.Container, googleCredentials *dagger.Secret) *dagger.Container {
	if googleCredentials == nil {
		return c
	}
	mounted := "/tmp/gcp-adc.json"
	return c.
		WithMountedSecret(mounted, googleCredentials).
		WithEnvVariable("GOOGLE_APPLICATION_CREDENTIALS", mounted)
}

func buildKustomizePaths(c *dagger.Container, paths []string) *dagger.Container {
	c = c.WithExec([]string{"mkdir", "-p", manifestDir})
	for _, p := range paths {
		safe := pathSafeName(p)
		c = c.WithExec([]string{
			"sh", "-c",
			fmt.Sprintf("echo '[build] %s' && kustomize build %q > %s/%s.yaml", p, p, manifestDir, safe),
		})
	}
	return c
}

func (r Runner) withOptionalGhcrLogin(c *dagger.Container) *dagger.Container {
	return r.execScript(c, "optional-ghcr-login.sh")
}
