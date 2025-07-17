package main

import (
	"testing"
)

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
