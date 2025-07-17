package repo

import (
	"archive/zip"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
)

type RepoFS interface {
	fs.FS
	Cleanup() error
}

// NewRepoFS opens a repository based on the URI scheme
func NewRepoFS(uri string) (RepoFS, error) {
	// Split repositories (support for multiple URIs is not yet implemented)
	repos := strings.Split(uri, ",")
	if len(repos) == 0 {
		return nil, fmt.Errorf("at least one URI must be defined")
	}

	for _, repo := range repos {
		switch {
		case strings.HasPrefix(repo, "http://"), strings.HasPrefix(repo, "https://"):
			slog.Info("Using repository", slog.String("repo", repo))
			return NewHTTPFS(repo), nil
		case strings.HasPrefix(repo, "zip://"):
			// zip package already implement fs.FS interface
			path := strings.TrimPrefix(repo, "zip://")

			r, err := zip.OpenReader(path)
			if err != nil {
				slog.Info("Failed to open zip file, trying next URI", slog.String("uri", repo), slog.Any("error", err))
				continue
			}

			slog.Info("Using repository", slog.String("repo", repo))
			return &ZipFS{ReadCloser: r, path: path}, nil
		}
	}

	return nil, fmt.Errorf("no valid repository found in %v", repos)
}
