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

// isInstalled returns true if the given installed version must be uninstalled before a reinstall
func isInstalled(installedVersion string) bool {
	return installedVersion != "" && installedVersion != "uninstalled"
}

// affectedComponents returns in install order an action, without recipes, for components whose installed version differs from the expected one
// and every component depending on them, directly or through another affected component.
// Components must be in install order, as sorted by releaseComponents.
func affectedComponents(components []Component, installed map[string]string, poolVersion string) []ComponentAction {
	expected := make(map[string]string, len(components))
	changed := make(map[string]bool, len(components))
	for _, component := range components {
		expected[component.Name] = expandVersion(component.Version, poolVersion)
		changed[component.Name] = installed[component.Name] != expected[component.Name]
	}

	actions := make([]ComponentAction, 0, len(components))
	affected := make(map[string]bool, len(components))
	for _, component := range components {
		if !changed[component.Name] && !slices.ContainsFunc(component.DependsOn, func(dependency string) bool { return affected[dependency] }) {
			slog.Info("Component already installed", slog.String("component", component.Name), slog.String("version", installed[component.Name]))
			continue
		}
		affected[component.Name] = true

		action := ComponentAction{
			Name:             component.Name,
			InstalledVersion: installed[component.Name],
			ExpectedVersion:  expected[component.Name],
			Reason:           "dependency changed",
		}
		if !isInstalled(action.InstalledVersion) {
			action.Reason = "not installed"
		} else if changed[component.Name] {
			action.Reason = "version changed"
		}
		actions = append(actions, action)
	}

	return actions
}

// planComponents fills the uninstall and install recipes of the given actions
func planComponents(repoFS fs.FS, actions []ComponentAction) ([]ComponentAction, error) {
	for i, action := range actions {
		versions, err := componentVersions(repoFS, action.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s metadata: %w", action.Name, err)
		}

		// Uninstall
		if isInstalled(action.InstalledVersion) {
			sections, err := versions.version(action.InstalledVersion)
			if err != nil {
				return nil, fmt.Errorf("failed to read %s metadata for installed version %s: %w", action.Name, action.InstalledVersion, err)
			}
			actions[i].Uninstall = sections.Uninstall
		}

		// Install
		sections, err := versions.version(action.ExpectedVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s metadata for expected version %s: %w", action.Name, action.ExpectedVersion, err)
		}
		actions[i].Install = sections.Install
	}

	return actions, nil
}
