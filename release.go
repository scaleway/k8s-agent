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

	// Ignore dependencies filtered out by installer tags, their own dependencies are already resolved
	kept := make(map[string]bool, len(filteredComponents))
	for _, component := range filteredComponents {
		kept[component.Name] = true
	}
	for i, component := range filteredComponents {
		filteredComponents[i].DependsOn = slices.DeleteFunc(slices.Clone(component.DependsOn), func(dependency string) bool {
			return !kept[dependency]
		})
	}

	return filteredComponents, nil
}

// componentsGraph returns the dependency graph of the components, with a pointer to each component stored in its vertex
// and an edge from each dependency to its dependents.
// It fails if a component is defined twice, depends on an unknown component or is part of a dependency cycle.
func componentsGraph(components []Component) (*dag.DAG, error) {
	graph := dag.NewDAG()
	for i, component := range components {
		err := graph.AddVertexByID(component.Name, &components[i])
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

// componentsVisitor collects the visited components in visit order
type componentsVisitor struct {
	sorted []Component
}

func (v *componentsVisitor) Visit(vertex dag.Vertexer) {
	_, component := vertex.Vertex()
	v.sorted = append(v.sorted, *component.(*Component))
}

// sortDependencies orders components so that each one comes after its dependencies.
// Each component dependencies are resolved to all its direct and indirect ones, in install order.
func sortDependencies(components []Component) ([]Component, error) {
	graph, err := componentsGraph(components)
	if err != nil {
		return nil, err
	}

	visitor := &componentsVisitor{sorted: make([]Component, 0, len(components))}
	graph.OrderedWalk(visitor)

	names := componentNames(visitor.sorted)
	for i, component := range visitor.sorted {
		ancestors, err := graph.GetAncestors(component.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get dependencies of %s: %w", component.Name, err)
		}
		visitor.sorted[i].DependsOn = slices.DeleteFunc(slices.Clone(names[:i]), func(name string) bool {
			_, ok := ancestors[name]
			return !ok
		})
	}

	return visitor.sorted, nil
}

// componentNames returns the names of the given components
func componentNames(components []Component) []string {
	names := make([]string, 0, len(components))
	for _, component := range components {
		names = append(names, component.Name)
	}
	return names
}
