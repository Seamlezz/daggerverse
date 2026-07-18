package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"dagger/tests/internal/dagger"
)

const fixtureRoot = "wash/testdata/workspace"

type Tests struct{}

func fixtureWash() *dagger.Wash {
	return dag.Wash("wash-integration-tests", dagger.WashOpts{RootDir: fixtureRoot})
}

func fixtureRegistry() *dagger.Service {
	return dag.Container().
		From("registry:2").
		WithExposedPort(5000).
		AsService()
}

// Build verifies multi-component artifact export through the public API.
func (m *Tests) Build(ctx context.Context) error {
	artifacts := fixtureWash().BuildComponents(dagger.WashBuildComponentsOpts{
		ComponentDirs: []string{"components/a", "components/b"},
	})
	entries, err := artifacts.Entries(ctx)
	if err != nil {
		return err
	}
	sort.Strings(entries)
	want := []string{"component-a.wasm", "component-b.wasm"}
	if fmt.Sprint(entries) != fmt.Sprint(want) {
		return fmt.Errorf("artifacts = %v, want %v", entries, want)
	}
	return nil
}

// Fallback verifies an incompatible wash command still builds correctly.
func (m *Tests) Fallback(ctx context.Context) error {
	artifacts := fixtureWash().BuildComponents(dagger.WashBuildComponentsOpts{
		ComponentDirs: []string{"components/fallback"},
	})
	entries, err := artifacts.Entries(ctx)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0] != "fallback.wasm" {
		return fmt.Errorf("fallback artifacts = %v", entries)
	}
	return nil
}

// Publish verifies version/latest publication through an in-session registry.
func (m *Tests) Publish(ctx context.Context) error {
	registry := fixtureRegistry()
	published, err := fixtureWash().PublishComponents(ctx, "registry:5000", dagger.WashPublishComponentsOpts{
		ComponentDirs:   []string{"components/a", "components/b"},
		RegistryService: registry,
		Repository:      "wash-tests",
		Tag:             "v1",
		Insecure:        true,
		MaxParallel:     4,
	})
	if err != nil {
		return err
	}
	for _, ref := range []string{
		"registry:5000/wash-tests/component-a:v1",
		"registry:5000/wash-tests/component-a:latest",
		"registry:5000/wash-tests/component-b:v1",
		"registry:5000/wash-tests/component-b:latest",
	} {
		if !strings.Contains(published, ref) {
			return fmt.Errorf("publish result missing %s:\n%s", ref, published)
		}
	}
	client := dag.Container().
		From("curlimages/curl:8.12.1").
		WithServiceBinding("registry", registry)
	for _, component := range []string{"component-a", "component-b"} {
		body, err := client.
			WithExec([]string{"curl", "-fsS", "http://registry:5000/v2/wash-tests/" + component + "/tags/list"}).
			Stdout(ctx)
		if err != nil {
			return err
		}
		if !strings.Contains(body, `"v1"`) || !strings.Contains(body, `"latest"`) {
			return fmt.Errorf("unexpected tags for %s: %s", component, body)
		}
	}
	return nil
}

// All runs the public-boundary integration suite.
func (m *Tests) All(ctx context.Context) error {
	if err := m.Build(ctx); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	if err := m.Fallback(ctx); err != nil {
		return fmt.Errorf("fallback: %w", err)
	}
	if err := m.Publish(ctx); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	return nil
}
