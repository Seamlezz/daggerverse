package check

import "dagger/gitops/internal/dagger"

// ToolVersions holds version pins for all external tools used in checks.
type ToolVersions struct {
	Kubeconform string
	Kustomize   string
	Yq          string
}

// DefaultToolVersions returns sensible version defaults.
func DefaultToolVersions() ToolVersions {
	return ToolVersions{
		Kubeconform: "v0.6.7",
		Kustomize:   "v5.6.0",
		Yq:          "v4.44.5",
	}
}

const (
	alpineImage = "alpine:3.21"
	checkBinDir = "/usr/local/bin/check"
	checkSrcDir = "/src"
)

func (r Runner) container() *dagger.Container {
	if r.newContainer == nil {
		panic("check runner container factory is nil")
	}
	return r.newContainer()
}

func (r Runner) withCheckRepo(c *dagger.Container, source *dagger.Directory) *dagger.Container {
	return c.
		WithDirectory(checkSrcDir, source, dagger.ContainerWithDirectoryOpts{
			Exclude:   []string{".git", ".dagger", "sandbox"},
			Gitignore: true,
		}).
		WithWorkdir(checkSrcDir).
		WithEnvVariable("SRC", checkSrcDir)
}

func (r Runner) alpineToolchain(packages ...string) *dagger.Container {
	c := r.container().From(alpineImage)
	if len(packages) == 0 {
		return c
	}
	return c.WithExec(append([]string{"apk", "add", "--no-cache"}, packages...))
}

func (r Runner) withKustomize(c *dagger.Container) *dagger.Container {
	return r.execScript(
		c.WithEnvVariable("KUSTOMIZE_VERSION", r.versions.Kustomize),
		"install-kustomize.sh",
	)
}

func (r Runner) withYq(c *dagger.Container) *dagger.Container {
	return r.execScript(
		c.WithEnvVariable("YQ_VERSION", r.versions.Yq),
		"install-yq.sh",
	)
}

func (r Runner) withKubeconform(c *dagger.Container) *dagger.Container {
	return r.execScript(
		c.WithEnvVariable("KUBECONFORM_VERSION", r.versions.Kubeconform),
		"install-kubeconform.sh",
	)
}

func (r Runner) withPyYAML(c *dagger.Container) *dagger.Container {
	return c.WithExec([]string{"pip", "install", "--break-system-packages", "pyyaml"})
}

func (r Runner) kustomizeToolchain() *dagger.Container {
	return r.withKustomize(r.alpineToolchain("curl"))
}

func (r Runner) kubeconformToolchain() *dagger.Container {
	return r.withKubeconform(r.withYq(r.withPyYAML(r.withKustomize(r.alpineToolchain("curl", "python3", "py3-pip", "jq", "helm", "sops"))))).
		WithNewFile("/usr/local/bin/extract-crd-schemas.py", r.scripts.get("extract-crd-schemas.py"), dagger.ContainerWithNewFileOpts{Permissions: 0o755})
}

func (r Runner) helmToolchain() *dagger.Container {
	return r.withYq(r.withPyYAML(r.withKustomize(r.alpineToolchain("curl", "jq", "helm", "sops", "python3", "py3-pip")))).
		WithNewFile("/usr/local/bin/merge-helm-values.py", r.scripts.get("merge-helm-values.py"), dagger.ContainerWithNewFileOpts{Permissions: 0o755})
}

func (r Runner) yamllintToolchain() *dagger.Container {
	return r.alpineToolchain("yamllint")
}

func (r Runner) sopsToolchain() *dagger.Container {
	return r.alpineToolchain("sops")
}

func (r Runner) terraformToolchain() *dagger.Container {
	return r.container().From("hashicorp/terraform:1.15")
}

func (r Runner) execScript(c *dagger.Container, name string, args ...string) *dagger.Container {
	command := append([]string{scriptPath(name)}, args...)
	return r.withScript(c, name).WithExec(command)
}

func (r Runner) withScript(c *dagger.Container, name string) *dagger.Container {
	return c.WithNewFile(scriptPath(name), r.scripts.get(name), dagger.ContainerWithNewFileOpts{Permissions: 0o755})
}

func scriptPath(name string) string {
	return checkBinDir + "/" + name
}
