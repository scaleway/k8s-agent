package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
)

func writeFile(cacheFS fs.FS, name, src, dst, mode, owner, group string) (string, error) {
	parsedMode, err := strconv.ParseUint(mode, 8, 32)
	if err != nil {
		return "", fmt.Errorf("failed to parse mode: %w", err)
	}

	srcFile, err := fs.ReadFile(cacheFS, fmt.Sprintf("%s/%s", name, src))
	if err != nil {
		return "", fmt.Errorf("failed to open src file: %w", err)
	}

	// If the destination is a directory, use the base name of the source file
	// if not, use the name of the destination file
	if strings.HasSuffix(dst, "/") {
		dst = dst + filepath.Base(src)
	}

	dstFile, err := openDstFile(dst, os.FileMode(parsedMode))
	if err != nil {
		return "", err
	}

	_, err = dstFile.Write(srcFile)
	if err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	err = dstFile.Close()
	if err != nil {
		return "", fmt.Errorf("failed to close dst file: %w", err)
	}

	err = chown(dst, owner, group)
	if err != nil {
		return "", fmt.Errorf("failed to chown file: %w", err)
	}

	return dst, nil
}

func templateFile(cacheFS fs.FS, name, src, dst, mode, owner, group string, metadata NodeMetadata) (string, error) {
	parsedMode, err := strconv.ParseUint(mode, 8, 32)
	if err != nil {
		return "", fmt.Errorf("failed to parse mode: %w", err)
	}

	srcFile, err := fs.ReadFile(cacheFS, fmt.Sprintf("%s/%s", name, src))
	if err != nil {
		return "", fmt.Errorf("failed to open src file: %w", err)
	}

	tmpl, err := template.New("tmpl").Funcs(sprig.FuncMap()).Parse(string(srcFile))
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var rendered strings.Builder
	err = tmpl.Execute(&rendered, metadata)
	if err != nil {
		return "", fmt.Errorf("failed to render template: %w", err)
	}

	// If the destination is a directory, use the base name of the source file
	// if not, use the name of the destination file
	if strings.HasSuffix(dst, "/") {
		dst = dst + filepath.Base(src)
	}

	dstFile, err := openDstFile(dst, os.FileMode(parsedMode))
	if err != nil {
		return "", err
	}

	_, err = dstFile.Write([]byte(rendered.String()))
	if err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	err = dstFile.Close()
	if err != nil {
		return "", fmt.Errorf("failed to close dst file: %w", err)
	}

	err = chown(dst, owner, group)
	if err != nil {
		return "", fmt.Errorf("failed to chown file: %w", err)
	}

	return dst, nil
}

func openDstFile(dst string, mode os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err == nil {
		return f, nil
	}
	if !errors.Is(err, syscall.ETXTBSY) {
		return nil, fmt.Errorf("failed to open dst file: %w", err)
	}
	if stopErr := stopProcessesUsingFile(dst); stopErr != nil {
		return nil, fmt.Errorf("failed to open dst file: %w (could not stop blocking processes: %v)", err, stopErr)
	}
	f, err = os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return nil, fmt.Errorf("failed to open dst file: %w", err)
	}
	return f, nil
}

func stopProcessesUsingFile(path string) error {
	pids, err := findProcessesUsingFile(path)
	if err != nil {
		return err
	}
	for _, pid := range pids {
		syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		alive := 0
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil {
				alive++
			}
		}
		if alive == 0 {
			return nil
		}
	}
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == nil {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	time.Sleep(500 * time.Millisecond)
	return nil
}

func findProcessesUsingFile(path string) ([]int, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		exePath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err != nil {
			continue
		}
		if exePath == absPath {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func mkdir(path string, mode string, owner string, group string) error {
	parsedMode, err := strconv.ParseUint(mode, 8, 32)
	if err != nil {
		return fmt.Errorf("failed to parse mode: %w", err)
	}

	err = os.Mkdir(path, os.FileMode(parsedMode))
	if err != nil && !os.IsExist(err) {
		// Ignore directory already exists error
		return fmt.Errorf("failed to create dir: %w", err)
	}

	// Since the mode is only set by mkdir at creation
	// we also ensure the mode is set when the directory already exists
	err = os.Chmod(path, os.FileMode(parsedMode))
	if err != nil {
		return fmt.Errorf("failed to chmod dir: %w", err)
	}

	err = chown(path, owner, group)
	if err != nil {
		return fmt.Errorf("failed to chown dir: %w", err)
	}

	return nil
}

func chown(path string, owner string, group string) error {
	ownerID, err := lookupUserID(owner)
	if err != nil {
		return fmt.Errorf("failed to lookup user id: %w", err)
	}

	groupID, err := lookupGroupID(group)
	if err != nil {
		return fmt.Errorf("failed to lookup group id: %w", err)
	}

	err = os.Chown(path, ownerID, groupID)
	if err != nil {
		return fmt.Errorf("failed to chown: %w", err)
	}

	return nil
}

func lookupUserID(username string) (int, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return 0, fmt.Errorf("failed to lookup user: %w", err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, fmt.Errorf("failed to parse user id: %w", err)
	}
	return uid, nil
}

func lookupGroupID(groupname string) (int, error) {
	g, err := user.LookupGroup(groupname)
	if err != nil {
		return 0, fmt.Errorf("failed to lookup group: %w", err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, fmt.Errorf("failed to parse group id: %w", err)
	}
	return gid, nil
}
