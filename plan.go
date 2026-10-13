package main

import (
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
)

// ComponentAction is an uninstall/install plan for a component
type ComponentAction struct {
	Name             string
	InstalledVersion string
	ExpectedVersion  string
	Reason           string
	Uninstall        []ComponentResources
	Install          []ComponentResources
}

// uninstalledVersion is the version stored for a component once uninstalled
const uninstalledVersion = "uninstalled"

// isInstalled returns true if the component must be uninstalled before a reinstall
func isInstalled(installedVersion string) bool {
	return installedVersion != "" && installedVersion != uninstalledVersion
}

// affectedComponents returns the outdated components and their dependents, in install order
func affectedComponents(components []Component, installed map[string]string, poolVersion string) []ComponentAction {
	actions := make([]ComponentAction, 0, len(components))
	affected := make(map[string]bool, len(components))
	for _, component := range components {
		expected := expandVersion(component.Version, poolVersion)
		changed := installed[component.Name] != expected
		if !changed && !slices.ContainsFunc(component.DependsOn, func(dependency string) bool { return affected[dependency] }) {
			slog.Info("Component already installed", slog.String("component", component.Name), slog.String("version", installed[component.Name]))
			continue
		}
		affected[component.Name] = true

		action := ComponentAction{
			Name:             component.Name,
			InstalledVersion: installed[component.Name],
			ExpectedVersion:  expected,
		}
		switch {
		case !isInstalled(action.InstalledVersion):
			action.Reason = "not installed"
		case changed:
			action.Reason = "version changed"
		default:
			action.Reason = "dependency changed"
		}
		actions = append(actions, action)
	}

	return actions
}

// planComponents fills the uninstall and install recipes of the given actions
func planComponents(repoFS fs.FS, actions []ComponentAction) error {
	for i, action := range actions {
		versions, err := componentVersions(repoFS, action.Name)
		if err != nil {
			return fmt.Errorf("failed to read %s metadata: %w", action.Name, err)
		}

		// Uninstall
		if isInstalled(action.InstalledVersion) {
			sections, err := versions.version(action.InstalledVersion)
			if err != nil {
				return fmt.Errorf("failed to read %s metadata for installed version %s: %w", action.Name, action.InstalledVersion, err)
			}
			actions[i].Uninstall = sections.Uninstall
		}

		// Install
		sections, err := versions.version(action.ExpectedVersion)
		if err != nil {
			return fmt.Errorf("failed to read %s metadata for expected version %s: %w", action.Name, action.ExpectedVersion, err)
		}
		actions[i].Install = sections.Install
	}

	return nil
}
