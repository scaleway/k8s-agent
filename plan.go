package main

import (
	"fmt"
	"io/fs"
	"slices"
)

// ComponentAction is an uninstall/install plan for a component
type ComponentAction struct {
	Component        Component
	InstalledVersion string
	ExpectedVersion  string
	Reason           string
	Uninstall        []ComponentResources
	Install          []ComponentResources
}

// isInstalled returns true if the given installed version must be uninstalled before a reinstall
func isInstalled(installedVersion string) bool {
	return installedVersion != "" && installedVersion != "uninstalled"
}

// affectedComponents returns in install order components whose installed version differs from the expected one and every component depending on them.
// Components dependencies must include their indirect ones, as resolved by releaseComponents.
func affectedComponents(components []Component, installed map[string]string, poolVersion string) []Component {
	changed := make(map[string]bool, len(components))
	for _, component := range components {
		changed[component.Name] = installed[component.Name] != expandVersion(component.Version, poolVersion)
	}

	return slices.DeleteFunc(slices.Clone(components), func(component Component) bool {
		return !changed[component.Name] && !slices.ContainsFunc(component.DependsOn, func(dependency string) bool {
			return changed[dependency]
		})
	})
}

// planComponents resolves every uninstall and install to be performed for the given components and returns them in install order
func planComponents(repoFS fs.FS, components []Component, installed map[string]string, poolVersion string) ([]ComponentAction, error) {
	actions := make([]ComponentAction, 0, len(components))
	for _, component := range components {
		action := ComponentAction{
			Component:        component,
			InstalledVersion: installed[component.Name],
			ExpectedVersion:  expandVersion(component.Version, poolVersion),
			Reason:           "version changed",
		}
		if !isInstalled(action.InstalledVersion) {
			action.Reason = "not installed"
		} else if action.InstalledVersion == action.ExpectedVersion {
			action.Reason = "dependency changed"
		}

		versions, err := componentVersions(repoFS, component.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s metadata: %w", component.Name, err)
		}

		// Uninstall
		if isInstalled(action.InstalledVersion) {
			sections, err := versions.version(action.InstalledVersion)
			if err != nil {
				return nil, fmt.Errorf("failed to read %s metadata for installed version %s: %w", component.Name, action.InstalledVersion, err)
			}
			action.Uninstall = sections.Uninstall
		}

		// Install
		sections, err := versions.version(action.ExpectedVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s metadata for expected version %s: %w", component.Name, action.ExpectedVersion, err)
		}
		action.Install = sections.Install

		actions = append(actions, action)
	}

	return actions, nil
}
