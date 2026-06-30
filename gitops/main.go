package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/gitops/internal/dagger"
)

// Gitops is a reusable Dagger module for GitOps workflows.
// It provides local Git mirror push/reload, Flux bootstrap, and static validation checks.
type Gitops struct {
	// BootstrapPath is the Flux bootstrap kustomization path within the repo.
	// Example: "clusters/local/flux-system"
	BootstrapPath string

	// GitRepoName is the bare repo name in the in-cluster Git mirror.
	// Example: "infra.git"
	GitRepoName string

	// GitBranch is the branch Flux reconciles from.
	GitBranch string

	// FluxNamespace is the namespace where Flux controllers run.
	FluxNamespace string

	// FluxSourceName is the name of the Flux GitRepository source.
	FluxSourceName string

	// GitUserName is the Git committer name for local mirror pushes.
	GitUserName string

	// GitUserEmail is the Git committer email for local mirror pushes.
	GitUserEmail string

	// Clusters overrides auto-discovered Flux cluster names for checks.
	Clusters []string

	// KubeVersion is the Kubernetes version used for helm template --kube-version.
	KubeVersion string

	// KubeconformSkips are resource kinds to skip during kubeconform validation.
	KubeconformSkips []string

	// KubeconformIgnores are filename patterns to ignore during kubeconform validation.
	KubeconformIgnores []string

	// +private
	GithubToken *dagger.Secret
	// +private
	GoogleCredentials *dagger.Secret
	// +private
	KubernetesService *dagger.Service
	// +private
	GitService *dagger.Service
	// +private
	Kubeconfig *dagger.Secret
}

// New creates a GitOps workflow module configured for a specific repository.
func New(
	// BootstrapPath is the Flux bootstrap kustomization path within the repo.
	// Example: "clusters/local/flux-system"
	bootstrapPath string,

	// GitRepoName is the bare repo name in the in-cluster Git mirror.
	// Example: "infra.git"
	gitRepoName string,

	// +optional
	// +default="main"
	gitBranch string,

	// +optional
	// +default="flux-system"
	fluxNamespace string,

	// +optional
	// +default="flux-system"
	fluxSourceName string,

	// +optional
	// +default="Winston"
	gitUserName string,

	// +optional
	// +default="winston@seamlezz.com"
	gitUserEmail string,

	// +optional
	// Cluster names for Flux Kustomization discovery in checks.
	// Auto-discovered from clusters/ directory if not set.
	clusters []string,

	// +optional
	// +default="1.33.0"
	kubeVersion string,

	// +optional
	// Kubeconform resource kinds to skip.
	// +default=["CustomResourceDefinition", "VaultDynamicSecret"]
	kubeconformSkips []string,

	// +optional
	// Kubeconform filename patterns to ignore.
	// +default=["gotk-components", "gotk-sync"]
	kubeconformIgnores []string,

	// +optional
	// GithubToken is used for Helm registry login to ghcr.io.
	// Set via .env: GITHUB_TOKEN=cmd://"gh auth token"
	githubToken *dagger.Secret,

	// +optional
	// GoogleCredentials is used for SOPS decryption (GCP KMS).
	// Set via .env: GOOGLE_CREDENTIALS=cmd://"gcloud auth application-default print-access-token"
	googleCredentials *dagger.Secret,

	// +optional
	// Kubernetes API service (e.g. k3d API).
	// Set via .env: KUBERNETES_SERVICE=tcp://localhost:6550
	kubernetesService *dagger.Service,

	// +optional
	// In-cluster Git mirror service.
	// Set via .env: GIT_SERVICE=tcp://localhost:30080
	gitService *dagger.Service,

	// +optional
	// Kubeconfig secret for the cluster.
	// Set via .env: KUBECONFIG=file://~/.kube/config
	kubeconfig *dagger.Secret,
) *Gitops {
	return &Gitops{
		BootstrapPath:      bootstrapPath,
		GitRepoName:        gitRepoName,
		GitBranch:          gitBranch,
		FluxNamespace:      fluxNamespace,
		FluxSourceName:     fluxSourceName,
		GitUserName:        gitUserName,
		GitUserEmail:       gitUserEmail,
		Clusters:           clusters,
		KubeVersion:        kubeVersion,
		KubeconformSkips:   kubeconformSkips,
		KubeconformIgnores: kubeconformIgnores,
		GithubToken:        githubToken,
		GoogleCredentials:  googleCredentials,
		KubernetesService:  kubernetesService,
		GitService:         gitService,
		Kubeconfig:         kubeconfig,
	}
}

const (
	workspaceDir = "/tmp/workspace"
	repoDir      = "/tmp/repo"
)

// kubeClient returns a container with kubectl, flux CLI, git, and rsync,
// configured to talk to the Kubernetes cluster via a service binding.
func (m *Gitops) kubeClient() *dagger.Container {
	return dag.Container().
		From("fluxcd/flux-cli:v2.8.6").
		WithUser("root").
		WithExec([]string{"apk", "add", "--no-cache", "git", "rsync"}).
		WithServiceBinding("kubernetes", m.KubernetesService).
		WithMountedSecret("/tmp/kubeconfig", m.Kubeconfig, dagger.ContainerWithMountedSecretOpts{Mode: 0444}).
		WithExec([]string{"sh", "-c", "sed 's#https://127\\.0\\.0\\.1:6550#https://kubernetes:6550#g' /tmp/kubeconfig > /tmp/kubeconfig.docker"}).
		WithEnvVariable("KUBECONFIG", "/tmp/kubeconfig.docker")
}

// pushLocalGit clones the in-cluster Git mirror, rsyncs the working tree,
// commits, and pushes.
func (m *Gitops) pushLocalGit(ctx context.Context, source *dagger.Directory) (string, error) {
	gitURL := fmt.Sprintf("http://gitServer:30080/git/%s", m.GitRepoName)

	c := m.kubeClient().
		WithDirectory(workspaceDir, source, dagger.ContainerWithDirectoryOpts{
			Exclude:   []string{".git"},
			Gitignore: true,
		}).
		WithServiceBinding("gitServer", m.GitService)

	return c.
		WithExec([]string{"git", "clone", "-b", m.GitBranch, gitURL, repoDir}).
		WithWorkdir(repoDir).
		WithExec([]string{"git", "config", "user.name", m.GitUserName}).
		WithExec([]string{"git", "config", "user.email", m.GitUserEmail}).
		WithExec([]string{"sh", "-c", fmt.Sprintf("rsync -a --delete --exclude .git %s/ .", workspaceDir)}).
		WithExec([]string{"git", "add", "-A"}).
		WithExec([]string{"sh", "-c", "git diff --cached --quiet && echo 'nothing to commit' || git commit -m 'chore: sync from Dagger'"}).
		WithExec([]string{"git", "push", "origin", m.GitBranch}).
		Stdout(ctx)
}

// fluxBootstrap installs Flux and applies the bootstrap kustomization.
func (m *Gitops) fluxBootstrap(ctx context.Context, source *dagger.Directory) (string, error) {
	return m.kubeClient().
		WithExec([]string{"flux", "install"}).
		WithDirectory(workspaceDir, source).
		WithExec([]string{"kubectl", "apply", "-k", workspaceDir + "/" + m.BootstrapPath}).
		Stdout(ctx)
}

// fluxReconcile reconciles the Flux Git source and all Kustomizations.
func (m *Gitops) fluxReconcile(ctx context.Context) (string, error) {
	ns := m.FluxNamespace
	src := m.FluxSourceName

	var out strings.Builder

	// Reconcile Git source
	c := m.kubeClient().
		WithExec([]string{"flux", "reconcile", "source", "git", src, "--namespace", ns})
	stdout, err := c.Stdout(ctx)
	if err != nil {
		return stdout, err
	}
	out.WriteString(stdout)

	// Reconcile all Kustomizations
	c = m.kubeClient().
		WithExec([]string{"sh", "-c", fmt.Sprintf(
			`kubectl -n %s get kustomizations.kustomize.toolkit.fluxcd.io -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | while read -r name; do [ -n "$name" ] && flux reconcile kustomization "$name" --namespace %s --with-source; done`,
			ns, ns,
		)})
	stdout, err = c.Stdout(ctx)
	if err != nil {
		return out.String() + "\n" + stdout, err
	}
	out.WriteString("\n")
	out.WriteString(stdout)

	return out.String(), nil
}

// fluxWait waits for Flux controllers, Git source, and Kustomizations to be ready.
func (m *Gitops) fluxWait(ctx context.Context) (string, error) {
	ns := m.FluxNamespace
	src := m.FluxSourceName

	var out strings.Builder

	// Wait for controller deployments
	c := m.kubeClient().
		WithExec([]string{"sh", "-c", fmt.Sprintf(
			`kubectl -n %s get deployments -o name | while read -r obj; do [ -n "$obj" ] && kubectl -n %s rollout status "$obj" --timeout=180s; done`,
			ns, ns,
		)})
	stdout, err := c.Stdout(ctx)
	if err != nil {
		return stdout, err
	}
	out.WriteString(stdout)

	// Wait for GitRepository
	c = m.kubeClient().
		WithExec([]string{"kubectl", "-n", ns, "wait", "gitrepository/" + src, "--for=condition=ready", "--timeout=120s"})
	stdout, err = c.Stdout(ctx)
	if err != nil {
		return out.String() + "\n" + stdout, err
	}
	out.WriteString("\n")
	out.WriteString(stdout)

	// Wait for Kustomizations
	c = m.kubeClient().
		WithExec([]string{"sh", "-c", fmt.Sprintf(
			`kubectl -n %s get kustomizations.kustomize.toolkit.fluxcd.io -o name | while read -r obj; do [ -n "$obj" ] && kubectl -n %s wait "$obj" --for=condition=ready --timeout=180s; done`,
			ns, ns,
		)})
	stdout, err = c.Stdout(ctx)
	if err != nil {
		return out.String() + "\n" + stdout, err
	}
	out.WriteString("\n")
	out.WriteString(stdout)

	// Flux status
	c = m.kubeClient().
		WithExec([]string{"flux", "get", "sources", "git", "-n", ns})
	stdout, err = c.Stdout(ctx)
	if err != nil {
		return out.String() + "\n" + stdout, err
	}
	out.WriteString("\n")
	out.WriteString(stdout)

	c = m.kubeClient().
		WithExec([]string{"flux", "get", "kustomizations", "-n", ns})
	stdout, err = c.Stdout(ctx)
	if err != nil {
		return out.String() + "\n" + stdout, err
	}
	out.WriteString("\n")
	out.WriteString(stdout)

	return out.String(), nil
}

// Initialize bootstraps Flux on a cluster and pushes the local repo into
// the in-cluster Git mirror. Runs: push → flux install → apply bootstrap → wait ready.
// +cache="never"
func (m *Gitops) Initialize(
	ctx context.Context,
	// +defaultPath="/"
	// +ignore=["target", ".git", "docs"]
	source *dagger.Directory,
) error {
	if _, err := m.pushLocalGit(ctx, source); err != nil {
		return err
	}
	if _, err := m.fluxBootstrap(ctx, source); err != nil {
		return err
	}
	if _, err := m.fluxWait(ctx); err != nil {
		return err
	}
	return nil
}

// Reload pushes the current working tree into the in-cluster Git mirror
// and reconciles Flux. Runs: push → reconcile → wait ready.
// +cache="never"
func (m *Gitops) Reload(
	ctx context.Context,
	// +defaultPath="/"
	// +ignore=["target", ".git", "docs"]
	source *dagger.Directory,
) (string, error) {
	var out strings.Builder

	pushed, err := m.pushLocalGit(ctx, source)
	if err != nil {
		return pushed, err
	}
	out.WriteString(pushed)

	reconciled, err := m.fluxReconcile(ctx)
	if err != nil {
		return out.String() + "\n" + reconciled, err
	}
	out.WriteString("\n")
	out.WriteString(reconciled)

	ready, err := m.fluxWait(ctx)
	if err != nil {
		return out.String() + "\n" + ready, err
	}
	out.WriteString("\n")
	out.WriteString(ready)

	return out.String(), nil
}
