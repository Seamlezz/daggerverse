package check

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"dagger/gitops/internal/dagger"
	"gopkg.in/yaml.v3"
)

type fluxKustomizationMetadata struct {
	Name string `yaml:"name"`
}

type fluxKustomizationDependency struct {
	Name string `yaml:"name"`
}

type fluxKustomizationSpec struct {
	Path      string                        `yaml:"path"`
	DependsOn []fluxKustomizationDependency `yaml:"dependsOn"`
}

type fluxKustomization struct {
	APIVersion string                    `yaml:"apiVersion"`
	Kind       string                    `yaml:"kind"`
	Metadata   fluxKustomizationMetadata `yaml:"metadata"`
	Spec       fluxKustomizationSpec     `yaml:"spec"`
}

// discoverClusters returns cluster names by listing subdirectories of clusters/.
// If the clusters/ directory does not exist, returns nil.
func discoverClusters(ctx context.Context, source *dagger.Directory) ([]string, error) {
	entries, err := source.Directory("clusters").Entries(ctx)
	if err != nil {
		// clusters/ directory doesn't exist — no clusters to discover
		return nil, nil
	}
	var clusters []string
	for _, entry := range entries {
		// Only include directories (Flux cluster dirs contain kustomization.yaml files)
		clusters = append(clusters, entry)
	}
	sort.Strings(clusters)
	return clusters, nil
}

// resolveClusters returns the configured clusters if set, otherwise auto-discovers.
func resolveClusters(ctx context.Context, source *dagger.Directory, configured []string) ([]string, error) {
	if len(configured) > 0 {
		return configured, nil
	}
	return discoverClusters(ctx, source)
}

func discoverFluxKustomizePaths(ctx context.Context, source *dagger.Directory, clusters []string) ([]string, error) {
	kustomizations, err := collectFluxKustomizations(ctx, source, clusters)
	if err != nil {
		return nil, err
	}
	return uniqueSortedKustomizePaths(kustomizations), nil
}

func uniqueSortedKustomizePaths(kustomizations []fluxKustomization) []string {
	paths := make([]string, 0, len(kustomizations))
	seen := map[string]struct{}{}
	for _, ks := range kustomizations {
		p := normalizeFluxPath(ks.Spec.Path)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func normalizeFluxPath(p string) string {
	return strings.TrimPrefix(p, "./")
}

func collectFluxKustomizations(ctx context.Context, source *dagger.Directory, clusters []string) ([]fluxKustomization, error) {
	var out []fluxKustomization
	for _, cluster := range clusters {
		kustomizations, err := collectFluxKustomizationsForCluster(ctx, source, cluster)
		if err != nil {
			return nil, err
		}
		out = append(out, kustomizations...)
	}
	return out, nil
}

func parseFluxKustomizationDocuments(content string) ([]fluxKustomization, error) {
	var docs []fluxKustomization
	dec := yaml.NewDecoder(strings.NewReader(content))
	for {
		var doc fluxKustomization
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if !isFluxKustomization(doc) {
			continue
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func isFluxKustomization(doc fluxKustomization) bool {
	return doc.Kind == "Kustomization" && doc.APIVersion == "kustomize.toolkit.fluxcd.io/v1"
}

func validateFluxIntegrity(ctx context.Context, source *dagger.Directory, clusters []string) error {
	var errs []string
	for _, cluster := range clusters {
		clusterErrs, err := validateFluxCluster(ctx, source, cluster)
		if err != nil {
			return err
		}
		errs = append(errs, clusterErrs...)
	}

	if len(errs) > 0 {
		return fmt.Errorf("flux integrity failed:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

func validateFluxCluster(ctx context.Context, source *dagger.Directory, cluster string) ([]string, error) {
	kustomizations, err := collectFluxKustomizationsForCluster(ctx, source, cluster)
	if err != nil {
		return nil, err
	}

	names, errs := collectKustomizationNames(cluster, kustomizations)
	pathErrs, err := validateKustomizationPaths(ctx, source, cluster, kustomizations)
	if err != nil {
		return nil, err
	}
	errs = append(errs, pathErrs...)
	errs = append(errs, validateKustomizationDependencies(cluster, kustomizations, names)...)
	return errs, nil
}

func collectKustomizationNames(cluster string, kustomizations []fluxKustomization) (map[string]struct{}, []string) {
	names := map[string]struct{}{}
	var errs []string
	for _, ks := range kustomizations {
		name := ks.Metadata.Name
		if name == "" {
			continue
		}
		if _, exists := names[name]; exists {
			errs = append(errs, fmt.Sprintf("clusters/%s: duplicate Flux Kustomization name: %s", cluster, name))
			continue
		}
		names[name] = struct{}{}
	}
	return names, errs
}

func validateKustomizationPaths(ctx context.Context, source *dagger.Directory, cluster string, kustomizations []fluxKustomization) ([]string, error) {
	var errs []string
	for _, ks := range kustomizations {
		p := normalizeFluxPath(ks.Spec.Path)
		if p == "" {
			errs = append(errs, fmt.Sprintf("clusters/%s: Kustomization/%s has empty spec.path", cluster, ks.Metadata.Name))
			continue
		}
		ok, err := source.Directory(p).Exists(ctx, ".")
		if err != nil {
			return nil, err
		}
		if !ok {
			errs = append(errs, fmt.Sprintf("clusters/%s: missing spec.path for Kustomization/%s: %s", cluster, ks.Metadata.Name, p))
		}
	}
	return errs, nil
}

func validateKustomizationDependencies(cluster string, kustomizations []fluxKustomization, names map[string]struct{}) []string {
	var errs []string
	for _, ks := range kustomizations {
		for _, dep := range ks.Spec.DependsOn {
			if dep.Name == "" {
				continue
			}
			if _, ok := names[dep.Name]; ok {
				continue
			}
			errs = append(errs, fmt.Sprintf("clusters/%s: Kustomization/%s dependsOn unknown target: %s", cluster, ks.Metadata.Name, dep.Name))
		}
	}
	return errs
}

func collectFluxKustomizationsForCluster(ctx context.Context, source *dagger.Directory, cluster string) ([]fluxKustomization, error) {
	var out []fluxKustomization
	clusterDir := source.Directory(path.Join("clusters", cluster))
	entries, err := clusterDir.Entries(ctx)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry, ".yaml") && !strings.HasSuffix(entry, ".yml") {
			continue
		}
		content, err := clusterDir.File(entry).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("read clusters/%s/%s: %w", cluster, entry, err)
		}
		docs, err := parseFluxKustomizationDocuments(content)
		if err != nil {
			return nil, fmt.Errorf("parse clusters/%s/%s: %w", cluster, entry, err)
		}
		out = append(out, docs...)
	}
	return out, nil
}

func globEncryptedSecrets(ctx context.Context, source *dagger.Directory) ([]string, error) {
	matches, err := source.Glob(ctx, "**/*.enc.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func globYAMLFiles(ctx context.Context, source *dagger.Directory) ([]string, error) {
	files, err := globFiles(ctx, source, "**/*.yaml", "**/*.yml")
	if err != nil {
		return nil, err
	}
	return filterFiles(files, shouldLintYAMLFile), nil
}

func globFiles(ctx context.Context, source *dagger.Directory, patterns ...string) ([]string, error) {
	var files []string
	for _, pattern := range patterns {
		matches, err := source.Glob(ctx, pattern)
		if err != nil {
			return nil, err
		}
		files = append(files, matches...)
	}
	sort.Strings(files)
	return files, nil
}

func filterFiles(files []string, keep func(string) bool) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		if !keep(file) {
			continue
		}
		out = append(out, file)
	}
	return out
}

func shouldLintYAMLFile(file string) bool {
	if strings.Contains(file, "/.git/") || strings.HasPrefix(file, ".git/") {
		return false
	}
	if strings.Contains(file, "/.dagger/") || strings.HasPrefix(file, ".dagger/") {
		return false
	}
	if strings.HasSuffix(file, "gotk-components.yaml") || strings.HasSuffix(file, "Taskfile.yml") {
		return false
	}
	if strings.HasSuffix(file, "helmfile.yaml") || strings.HasPrefix(file, "sandbox/") {
		return false
	}
	if strings.Contains(file, "/_helm/") || strings.HasPrefix(file, "_helm/") {
		return false
	}
	return true
}

var manifestNameReplacer = strings.NewReplacer("/", "__", ".", "__")

func pathSafeName(p string) string {
	return manifestNameReplacer.Replace(p)
}
