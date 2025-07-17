package repo

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// HTTPFS is a fs.FS implementation that reads files from an HTTP server
// Only the ReadFile method is implemented
type httpFS struct {
	baseURL string
	client  *http.Client
}

func NewHTTPFS(baseURL string) *httpFS {
	return &httpFS{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (h *httpFS) Open(name string) (fs.File, error) {
	return &httpFile{}, nil
}

func (h *httpFS) ReadFile(name string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s", h.baseURL, name)

	resp, err := h.client.Get(url)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fs.ErrNotExist
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	err = resp.Body.Close()
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (h *httpFS) Cleanup() error {
	// No cleanup needed for HTTPFS
	return nil
}

type httpFile struct{}

func (f *httpFile) Stat() (fs.FileInfo, error) {
	return &httpFileInfo{}, nil
}

func (f *httpFile) Read(b []byte) (int, error) {
	return 0, io.EOF
}

func (f *httpFile) Close() error {
	return nil
}

type httpFileInfo struct{}

func (fi *httpFileInfo) Name() string {
	return ""
}

func (fi *httpFileInfo) Size() int64 {
	return 0
}

func (fi *httpFileInfo) Mode() fs.FileMode {
	return 0
}

func (fi *httpFileInfo) ModTime() time.Time {
	return time.Time{}
}

func (fi *httpFileInfo) IsDir() bool {
	return false
}

func (fi *httpFileInfo) Sys() interface{} {
	return nil
}
