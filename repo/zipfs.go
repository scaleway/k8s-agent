package repo

import (
	"archive/zip"
	"fmt"
	"os"
)

type ZipFS struct {
	*zip.ReadCloser
	path string
}

func (z *ZipFS) Cleanup() error {
	// Cleanly close the zip file
	err := z.Close()
	if err != nil {
		return fmt.Errorf("failed to close zip file: %w", err)
	}

	// Check if the zip file exists before attempting to remove it
	_, err = os.Stat(z.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf("failed to stat zip file: %w", err)
	}

	// Remove zip file
	err = os.Remove(z.path)
	if err != nil {
		return fmt.Errorf("failed to remove zip file: %w", err)
	}

	return nil
}
