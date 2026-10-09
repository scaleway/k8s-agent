package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetComponentVersion(t *testing.T) {
	dir := t.TempDir()
	defaultVersionsFile := versionsFile
	versionsFile = filepath.Join(dir, "versions.json")
	t.Cleanup(func() { versionsFile = defaultVersionsFile })

	err := SetComponentVersion("runc", "1.5.2")
	if err != nil {
		t.Fatalf("SetComponentVersion() error = %v", err)
	}
	err = SetComponentVersion("kubelet", "uninstalled")
	if err != nil {
		t.Fatalf("SetComponentVersion() error = %v", err)
	}

	content, err := os.ReadFile(versionsFile)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"kubelet":"uninstalled","runc":"1.5.2"}`
	if string(content) != expected {
		t.Errorf("versions file = %s, expected %s", content, expected)
	}

	info, err := os.Stat(versionsFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("versions file mode = %v, expected %v", info.Mode().Perm(), os.FileMode(0644))
	}

	// No temporary file is left behind
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("SetComponentVersion() left %d files in the versions directory, expected 1", len(entries))
	}
}

func TestExpandVersion(t *testing.T) {
	tests := []struct {
		name           string
		version        string
		defaultVersion string
		expected       string
	}{
		{
			name:           "normal version without sub",
			version:        "1.2.3",
			defaultVersion: "2.0.0",
			expected:       "1.2.3",
		},
		{
			name:           "version with sub",
			version:        "1.2.3~4",
			defaultVersion: "2.0.0",
			expected:       "1.2.3~4",
		},
		{
			name:           "empty main version with sub",
			version:        "~4",
			defaultVersion: "2.0.0",
			expected:       "2.0.0~4",
		},
		{
			name:           "empty version",
			version:        "",
			defaultVersion: "2.0.0",
			expected:       "2.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expandVersion(tt.version, tt.defaultVersion)
			if result != tt.expected {
				t.Errorf("expandVersion(%q, %q) = %q, expected %q",
					tt.version, tt.defaultVersion, result, tt.expected)
			}
		})
	}
}

func TestTrimVersion(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		expected string
	}{
		{
			name:     "normal version without sub",
			version:  "1.2.3",
			expected: "1.2.3",
		},
		{
			name:     "version with sub",
			version:  "1.2.3~4",
			expected: "1.2.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := trimVersion(tt.version)
			if result != tt.expected {
				t.Errorf("trimVersion(%q) = %q, expected %q",
					tt.version, result, tt.expected)
			}
		})
	}
}
