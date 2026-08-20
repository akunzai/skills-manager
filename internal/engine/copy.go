package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func CopySkillFolder(srcDir, targetDir string) error {
	// Remove existing target first cleanly
	_ = os.RemoveAll(targetDir)

	if err := os.MkdirAll(filepath.Dir(targetDir), 0755); err != nil {
		return fmt.Errorf("failed to create parent dir: %w", err)
	}

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// Filter out ignored patterns
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			destPath := filepath.Join(targetDir, relPath)
			return os.MkdirAll(destPath, info.Mode())
		}

		if strings.HasSuffix(info.Name(), ".pyc") {
			return nil
		}

		destPath := filepath.Join(targetDir, relPath)
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		// Handle symlink or regular file
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, destPath)
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		destFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer destFile.Close()

		if _, err := io.Copy(destFile, srcFile); err != nil {
			return err
		}

		return nil
	})
}
