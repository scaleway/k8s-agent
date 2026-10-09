package main

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestWriteFileReplacesRunningBinary(t *testing.T) {
	currentUser, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	currentGroup, err := user.LookupGroupId(currentUser.Gid)
	if err != nil {
		t.Fatal(err)
	}

	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep binary not found")
	}
	sleepBinary, err := os.ReadFile(sleepPath)
	if err != nil {
		t.Fatal(err)
	}

	// Run a binary from the destination path
	dst := filepath.Join(t.TempDir(), "sleep")
	err = os.WriteFile(dst, sleepBinary, 0755)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(dst, "60")
	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Replace it while it is running
	repoFS := fstest.MapFS{"sleep/bin/sleep": &fstest.MapFile{Data: []byte("new binary")}}
	_, err = writeFile(repoFS, "sleep", "bin/sleep", dst, "0750", currentUser.Username, currentGroup.Name)
	if err != nil {
		t.Fatalf("writeFile() error = %v", err)
	}

	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new binary" {
		t.Errorf("writeFile() content = %q, expected %q", content, "new binary")
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0750 {
		t.Errorf("writeFile() mode = %v, expected %v", info.Mode().Perm(), os.FileMode(0750))
	}

	// No temporary file is left behind
	entries, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("writeFile() left %d files in the destination directory, expected 1", len(entries))
	}
}
