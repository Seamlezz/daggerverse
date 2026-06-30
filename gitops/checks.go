package main

import (
	"context"
	"embed"

	checks "dagger/gitops/check"
)

//go:embed scripts/check/*
var checkScriptFS embed.FS

func (m *Gitops) checkRunner() checks.Runner {
	config := checks.Config{
		Clusters:           m.Clusters,
		KubeVersion:        m.KubeVersion,
		KubeconformSkips:   m.KubeconformSkips,
		KubeconformIgnores: m.KubeconformIgnores,
	}
	return checks.NewRunner(dag.Container, checkScriptFS, "scripts/check", checks.DefaultToolVersions(), config)
}

// CheckKustomizeBuild renders every Flux Kustomization path for all clusters.
// +check
func (m *Gitops) CheckKustomizeBuild(
	ctx context.Context,
) error {
	return m.checkRunner().CheckKustomizeBuild(ctx, m.Source)
}

// CheckKubeconform validates rendered manifests against core and CRD schemas.
// +check
func (m *Gitops) CheckKubeconform(
	ctx context.Context,
) error {
	return m.checkRunner().CheckKubeconform(ctx, m.Source, m.GoogleCredentials)
}

// CheckFluxIntegrity validates Flux Kustomization paths and dependsOn references.
// +check
func (m *Gitops) CheckFluxIntegrity(
	ctx context.Context,
) error {
	return m.checkRunner().CheckFluxIntegrity(ctx, m.Source)
}

// CheckSopsDecrypt verifies all encrypted secrets can be decrypted.
// +check
func (m *Gitops) CheckSopsDecrypt(
	ctx context.Context,
) error {
	return m.checkRunner().CheckSopsDecrypt(ctx, m.Source, m.GoogleCredentials)
}

// CheckTerraform validates and format-checks Terraform environments.
// +check
func (m *Gitops) CheckTerraform(
	ctx context.Context,
) error {
	return m.checkRunner().CheckTerraform(ctx, m.Source)
}

// CheckYamlLint lints YAML files.
// +check
func (m *Gitops) CheckYamlLint(
	ctx context.Context,
) error {
	return m.checkRunner().CheckYamlLint(ctx, m.Source)
}

// CheckHelmReleases dry-runs HelmRelease templates.
// +check
func (m *Gitops) CheckHelmReleases(
	ctx context.Context,
) error {
	return m.checkRunner().CheckHelmReleases(ctx, m.Source, m.GoogleCredentials)
}
