package main

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/heimdalr/dag"
	"gopkg.in/yaml.v3"
)

// Structs to unmarshal releases.yaml
type Releases struct {
	Versions map[string][]Component
}

type Component struct {
	Name      string
	Version   string
	Tags      []string
	DependsOn []string `yaml:"depends_on,omitempty"`
}

// releaseComponents reads the releases.yaml file and returns the components for the given node version
func releaseComponents(repoFS fs.FS, nodemetadata NodeMetadata) ([]Component, error) {
	// Read and unmarshal "releases.yaml" file at the root of the repository
	var releases Releases
	releasesFile, err := fs.ReadFile(repoFS, "releases.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to read releases file: %w", err)
	}
	err = yaml.Unmarshal(releasesFile, &releases)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal releases file: %w", err)
	}

	// Get the release components for the node version
	releaseComponents, ok := releases.Versions[nodemetadata.PoolVersion]
	if !ok {
		return nil, fmt.Errorf("release %s not found", nodemetadata.PoolVersion)
	}

	// Validate and sort the dependencies on the whole release
	releaseComponents, err = sortDependencies(releaseComponents)
	if err != nil {
		return nil, fmt.Errorf("invalid release %s: %w", nodemetadata.PoolVersion, err)
	}

	filteredComponents := []Component{}

	// If no installer tags are specified, include all components
	if len(nodemetadata.InstallerTags) == 0 {
		filteredComponents = append(filteredComponents, releaseComponents...)
	} else {
		// Otherwise, include only components that match at least one of the installer tags
		for _, component := range releaseComponents {
			for _, tag := range nodemetadata.InstallerTags {
				if slices.Contains(component.Tags, tag) {
					filteredComponents = append(filteredComponents, component)
					break
				}
			}
		}
	}

	return filteredComponents, nil
}

// componentsGraph returns the dependency graph of the components, with an edge from each dependency to its dependents.
// It fails if a component is defined twice, depends on an unknown component or is part of a dependency cycle.
func componentsGraph(components []Component) (*dag.DAG, error) {
	graph := dag.NewDAG()
	for _, component := range components {
		err := graph.AddVertexByID(component.Name, component.Name)
		if err != nil {
			return nil, fmt.Errorf("invalid component %s: %w", component.Name, err)
		}
	}

	for _, component := range components {
		for _, dependency := range component.DependsOn {
			err := graph.AddEdge(dependency, component.Name)
			if err != nil && !errors.As(err, &dag.EdgeDuplicateError{}) {
				return nil, fmt.Errorf("invalid dependency %s of component %s: %w", dependency, component.Name, err)
			}
		}
	}

	return graph, nil
}

// sortDependencies orders components so that each one comes after its dependencies, keeping the release order otherwise
// (dag.OrderedWalk would order independent components by name instead)
func sortDependencies(components []Component) ([]Component, error) {
	graph, err := componentsGraph(components)
	if err != nil {
		return nil, err
	}

	// Repeatedly pick the first remaining component without pending dependencies, then remove it from the graph
	sorted := make([]Component, 0, len(components))
	remaining := slices.Clone(components)
	for len(remaining) > 0 {
		index := slices.IndexFunc(remaining, func(component Component) bool {
			isRoot, err := graph.IsRoot(component.Name)
			return err == nil && isRoot
		})
		if index == -1 {
			return nil, fmt.Errorf("no component without pending dependencies in %v", componentNames(remaining))
		}

		err = graph.DeleteVertex(remaining[index].Name)
		if err != nil {
			return nil, fmt.Errorf("failed to remove component %s from graph: %w", remaining[index].Name, err)
		}
		sorted = append(sorted, remaining[index])
		remaining = slices.Delete(remaining, index, index+1)
	}

	return sorted, nil
}

// componentNames returns the names of the given components
func componentNames(components []Component) []string {
	names := make([]string, 0, len(components))
	for _, component := range components {
		names = append(names, component.Name)
	}
	return names
}
