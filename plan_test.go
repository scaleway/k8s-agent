package main

import (
	"maps"
	"slices"
	"testing"
	"testing/fstest"
)

// testRelease mirrors the release layout expected in releases.yaml
var testRelease = []Component{
	{Name: "cni-plugins", Version: "1.9.1"},
	{Name: "runc", Version: "1.5.2"},
	{Name: "crictl", Version: "1.37.0"},
	{Name: "containerd", Version: "2.3.6", DependsOn: []string{"cni-plugins", "runc", "crictl"}},
	{Name: "kubelet", DependsOn: []string{"containerd"}},
}

// testInstalled returns the installed versions matching testRelease, with the given overrides
func testInstalled(overrides map[string]string) map[string]string {
	installed := map[string]string{
		"cni-plugins": "1.9.1",
		"runc":        "1.5.2",
		"crictl":      "1.37.0",
		"containerd":  "2.3.6",
		"kubelet":     "1.37.1",
	}
	maps.Copy(installed, overrides)
	return installed
}

func componentNames(components []Component) []string {
	names := []string{}
	for _, component := range components {
		names = append(names, component.Name)
	}
	return names
}

func TestValidateDependencies(t *testing.T) {
	tests := []struct {
		name       string
		components []Component
		wantErr    bool
	}{
		{
			name:       "valid chain",
			components: testRelease,
		},
		{
			name:       "no dependencies",
			components: []Component{{Name: "runc"}, {Name: "containerd"}},
		},
		{
			name:       "unknown dependency",
			components: []Component{{Name: "containerd", DependsOn: []string{"unknown"}}},
			wantErr:    true,
		},
		{
			name:       "dependency defined after",
			components: []Component{{Name: "containerd", DependsOn: []string{"runc"}}, {Name: "runc"}},
			wantErr:    true,
		},
		{
			name:       "self dependency",
			components: []Component{{Name: "runc", DependsOn: []string{"runc"}}},
			wantErr:    true,
		},
		{
			name:       "duplicate component",
			components: []Component{{Name: "runc"}, {Name: "runc"}},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDependencies(tt.components)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDependencies() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAffectedComponents(t *testing.T) {
	tests := []struct {
		name        string
		components  []Component
		installed   map[string]string
		poolVersion string
		expected    []string
	}{
		{
			name:        "nothing changed",
			components:  testRelease,
			installed:   testInstalled(nil),
			poolVersion: "1.37.1",
			expected:    []string{},
		},
		{
			name:        "only kubelet changed",
			components:  testRelease,
			installed:   testInstalled(nil),
			poolVersion: "1.37.2",
			expected:    []string{"kubelet"},
		},
		{
			name:        "runc changed",
			components:  testRelease,
			installed:   testInstalled(map[string]string{"runc": "1.4.3"}),
			poolVersion: "1.37.1",
			expected:    []string{"runc", "containerd", "kubelet"},
		},
		{
			name:        "crictl changed",
			components:  testRelease,
			installed:   testInstalled(map[string]string{"crictl": "1.36.0"}),
			poolVersion: "1.37.1",
			expected:    []string{"crictl", "containerd", "kubelet"},
		},
		{
			name:        "cni-plugins changed does not affect siblings",
			components:  testRelease,
			installed:   testInstalled(map[string]string{"cni-plugins": "1.9.0"}),
			poolVersion: "1.37.1",
			expected:    []string{"cni-plugins", "containerd", "kubelet"},
		},
		{
			name:        "fresh node",
			components:  testRelease,
			installed:   map[string]string{},
			poolVersion: "1.37.1",
			expected:    []string{"cni-plugins", "runc", "crictl", "containerd", "kubelet"},
		},
		{
			name:        "crash after containerd uninstall",
			components:  testRelease,
			installed:   testInstalled(map[string]string{"containerd": "uninstalled", "kubelet": "uninstalled"}),
			poolVersion: "1.37.1",
			expected:    []string{"containerd", "kubelet"},
		},
		{
			name:        "crash after containerd reinstall",
			components:  testRelease,
			installed:   testInstalled(map[string]string{"kubelet": "uninstalled"}),
			poolVersion: "1.37.1",
			expected:    []string{"kubelet"},
		},
		{
			name:        "sub version",
			components:  []Component{{Name: "runc", Version: "1.5.2"}, {Name: "kubelet", Version: "~1", DependsOn: []string{"runc"}}},
			installed:   map[string]string{"runc": "1.5.2", "kubelet": "1.37.1"},
			poolVersion: "1.37.1",
			expected:    []string{"kubelet"},
		},
		{
			name:        "dependency filtered out by tags",
			components:  []Component{{Name: "kubelet", DependsOn: []string{"containerd"}}},
			installed:   map[string]string{"containerd": "2.3.4", "kubelet": "1.37.1"},
			poolVersion: "1.37.1",
			expected:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := componentNames(affectedComponents(tt.components, tt.installed, tt.poolVersion))
			if !slices.Equal(result, tt.expected) {
				t.Errorf("affectedComponents() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// testRepoFS returns a repository with a runc component defining the given versions
func testRepoFS(versions ...string) fstest.MapFS {
	metadata := "versions:\n"
	for _, version := range versions {
		metadata += "  " + version + ":\n" +
			"    install:\n      - scripts:\n        - {'cmd': 'install " + version + "'}\n" +
			"    uninstall:\n      - scripts:\n        - {'cmd': 'uninstall " + version + "'}\n"
	}
	return fstest.MapFS{"runc/metadata.yaml": &fstest.MapFile{Data: []byte(metadata)}}
}

func TestPlanComponents(t *testing.T) {
	components := []Component{{Name: "runc", Version: "1.5.2"}}

	tests := []struct {
		name          string
		repoFS        fstest.MapFS
		installed     map[string]string
		wantErr       bool
		wantUninstall string
		wantReason    string
	}{
		{
			name:          "upgrade",
			repoFS:        testRepoFS("1.4.3", "1.5.2"),
			installed:     map[string]string{"runc": "1.4.3"},
			wantUninstall: "uninstall 1.4.3",
			wantReason:    "version changed",
		},
		{
			name:          "reinstall for a dependency",
			repoFS:        testRepoFS("1.5.2"),
			installed:     map[string]string{"runc": "1.5.2"},
			wantUninstall: "uninstall 1.5.2",
			wantReason:    "dependency changed",
		},
		{
			name:       "not installed",
			repoFS:     testRepoFS("1.5.2"),
			installed:  map[string]string{},
			wantReason: "version changed",
		},
		{
			name:       "previously uninstalled",
			repoFS:     testRepoFS("1.5.2"),
			installed:  map[string]string{"runc": "uninstalled"},
			wantReason: "version changed",
		},
		{
			name:      "installed version missing from metadata",
			repoFS:    testRepoFS("1.5.2"),
			installed: map[string]string{"runc": "1.3.0"},
			wantErr:   true,
		},
		{
			name:      "expected version missing from metadata",
			repoFS:    testRepoFS("1.4.3"),
			installed: map[string]string{"runc": "1.4.3"},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions, err := planComponents(tt.repoFS, components, tt.installed, "1.37.1")
			if (err != nil) != tt.wantErr {
				t.Fatalf("planComponents() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			action := actions[0]
			if action.Reason != tt.wantReason {
				t.Errorf("reason = %q, expected %q", action.Reason, tt.wantReason)
			}
			if len(action.Install) != 1 || action.Install[0].Scripts[0].Cmd != "install 1.5.2" {
				t.Errorf("install = %+v, expected the 1.5.2 recipe", action.Install)
			}
			uninstall := ""
			if len(action.Uninstall) > 0 {
				uninstall = action.Uninstall[0].Scripts[0].Cmd
			}
			if uninstall != tt.wantUninstall {
				t.Errorf("uninstall = %q, expected %q", uninstall, tt.wantUninstall)
			}
		})
	}
}

func TestReleaseComponents(t *testing.T) {
	tests := []struct {
		name     string
		releases string
		wantErr  bool
		expected []Component
	}{
		{
			name: "with dependencies",
			releases: `versions:
  1.37.1:
    - name: runc
      version: "1.5.2"
    - name: containerd
      version: "2.3.6"
      depends_on: [runc]
`,
			expected: []Component{
				{Name: "runc", Version: "1.5.2"},
				{Name: "containerd", Version: "2.3.6", DependsOn: []string{"runc"}},
			},
		},
		{
			name: "without dependencies",
			releases: `versions:
  1.37.1:
    - name: runc
      version: "1.5.2"
    - name: kubelet
`,
			expected: []Component{
				{Name: "runc", Version: "1.5.2"},
				{Name: "kubelet"},
			},
		},
		{
			name: "invalid dependency",
			releases: `versions:
  1.37.1:
    - name: containerd
      version: "2.3.6"
      depends_on: [runc]
    - name: runc
      version: "1.5.2"
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoFS := fstest.MapFS{"releases.yaml": &fstest.MapFile{Data: []byte(tt.releases)}}
			result, err := releaseComponents(repoFS, NodeMetadata{PoolVersion: "1.37.1"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("releaseComponents() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if !slices.EqualFunc(result, tt.expected, func(a, b Component) bool {
				return a.Name == b.Name && a.Version == b.Version && slices.Equal(a.DependsOn, b.DependsOn)
			}) {
				t.Errorf("releaseComponents() = %+v, expected %+v", result, tt.expected)
			}
		})
	}
}
