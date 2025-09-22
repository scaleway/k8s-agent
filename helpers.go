package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

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

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(parsedMode))
	if err != nil {
		return "", fmt.Errorf("failed to open dst file: %w", err)
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

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(parsedMode))
	if err != nil {
		return "", fmt.Errorf("failed to open dst file: %w", err)
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
