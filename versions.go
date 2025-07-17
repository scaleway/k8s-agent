package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// JSON File template to store installed components versions
//
//	{
//	   "component1": "2.1.0",
//	   "component2": "3.8.9",
//	   "component3": "0.3.5"
//	}

const versionsFile = "/etc/scw-k8s-versions.json"

func SetComponentVersion(component string, version string) error {
	versions := make(map[string]string)

	// Check if versions file exists
	if info, err := os.Stat(versionsFile); err == nil && !info.IsDir() {
		// File exists: read and unmarshal its content
		jsonVersions, err := os.ReadFile(versionsFile)
		if err != nil {
			return fmt.Errorf("failed to read versions file: %w", err)
		}

		err = json.Unmarshal(jsonVersions, &versions)
		if err != nil {
			return fmt.Errorf("failed to unmarshal versions file: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		// An error occurred while checking the file (other than it not existing)
		return fmt.Errorf("failed to stat versions file: %w", err)
	}

	// Set component version
	versions[component] = version

	// Marshal the updated map to JSON
	jsonVersions, err := json.Marshal(versions)
	if err != nil {
		return fmt.Errorf("failed to marshal versions: %w", err)
	}

	// Write the JSON back to the file
	err = os.WriteFile(versionsFile, jsonVersions, 0644)
	if err != nil {
		return fmt.Errorf("failed to write versions file: %w", err)
	}

	return nil
}

func GetComponentVersion(component string) (string, error) {
	versions := make(map[string]string)

	// Check if versions file exists
	if info, err := os.Stat(versionsFile); err == nil && !info.IsDir() {
		// File exists: read and unmarshal its content
		jsonVersions, err := os.ReadFile(versionsFile)
		if err != nil {
			return "", fmt.Errorf("failed to read versions file: %w", err)
		}

		err = json.Unmarshal(jsonVersions, &versions)
		if err != nil {
			return "", fmt.Errorf("failed to unmarshal versions file: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		// An error occurred while checking the file (other than it not existing)
		return "", fmt.Errorf("failed to stat versions file: %w", err)
	}

	// Get component version
	version, ok := versions[component]
	if !ok {
		return "", nil
	}

	return version, nil
}

func ListComponentsVersions() (map[string]string, error) {
	versions := make(map[string]string)

	// Check if versions file exists
	if info, err := os.Stat(versionsFile); err == nil && !info.IsDir() {
		// File exists: read and unmarshal its content
		jsonVersions, err := os.ReadFile(versionsFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read versions file: %w", err)
		}

		err = json.Unmarshal(jsonVersions, &versions)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal versions file: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		// An error occurred while checking the file (other than it not existing)
		return nil, fmt.Errorf("failed to stat versions file: %w", err)
	}

	return versions, nil
}

func expandVersion(version, defaultVersion string) string {
	// If version is empty, return defaultVersion
	if version == "" {
		return defaultVersion
	}

	// If version starts with ~, prepend defaultVersion
	if version[0] == '~' {
		return defaultVersion + version
	}

	// Otherwise, return the version as is
	return version
}

func trimVersion(version string) string {
	splittedVersion := strings.Split(version, "~")
	if len(splittedVersion) > 0 {
		return splittedVersion[0]
	}
	return version
}
