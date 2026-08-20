package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RemoveAll removes path and its contents cleanly, resetting read-only permissions if needed on Windows.
func RemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	// Fallback: chmod all files/dirs to writable and retry removal
	_ = filepath.Walk(path, func(p string, fi os.FileInfo, wErr error) error {
		if wErr == nil {
			_ = os.Chmod(p, 0700)
		}
		return nil
	})
	return os.RemoveAll(path)
}

func copyFile(srcPath, destPath string, mode os.FileMode) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Ensure destination file is created with write permissions
	fileMode := mode
	if fileMode == 0 {
		fileMode = 0644
	}
	// Ensure owner write bit is set so subsequent updates can overwrite/remove
	fileMode = fileMode | 0200

	destFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		return err
	}
	return nil
}

func CopySkillFolder(srcDir, targetDir string) error {
	// Remove existing target first cleanly
	_ = RemoveAll(targetDir)

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
			return os.MkdirAll(destPath, info.Mode()|0700)
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
			if err := os.Symlink(linkTarget, destPath); err != nil {
				if runtime.GOOS == "windows" {
					// Fallback on Windows when symlink creation fails (e.g. Developer Mode off)
					resolvedTarget := linkTarget
					if !filepath.IsAbs(resolvedTarget) {
						resolvedTarget = filepath.Join(filepath.Dir(path), linkTarget)
					}
					if tInfo, statErr := os.Stat(resolvedTarget); statErr == nil {
						if tInfo.IsDir() {
							return CopySkillFolder(resolvedTarget, destPath)
						}
						return copyFile(resolvedTarget, destPath, tInfo.Mode())
					}
				}
				return err
			}
			return nil
		}

		return copyFile(path, destPath, info.Mode())
	})
}
