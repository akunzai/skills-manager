package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/akunzai/skills-manager/internal/models"
)

var Version = "0.6.0"
var GitHubRepo = "akunzai/skills-manager"

type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type ReleaseInfo struct {
	TagName string         `json:"tag_name"`
	Body    string         `json:"body"`
	HTMLURL string         `json:"html_url"`
	Assets  []ReleaseAsset `json:"assets"`
}

type SelfUpdateInfo struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	LatestTag       string `json:"latest_tag"`
	UpdateAvailable bool   `json:"update_available"`
	AssetURL        string `json:"asset_url,omitempty"`
	AssetName       string `json:"asset_name,omitempty"`
	AssetSize       int64  `json:"asset_size,omitempty"`
	ReleaseNotes    string `json:"release_notes,omitempty"`
	HTMLURL         string `json:"html_url,omitempty"`
}

func GetCurrentExecutablePath() string {
	if exe, err := os.Executable(); err == nil {
		if realExe, err := filepath.EvalSymlinks(exe); err == nil {
			return realExe
		}
		return exe
	}

	if path, err := exec.LookPath("skills"); err == nil {
		if abs, err := filepath.Abs(path); err == nil {
			return abs
		}
		return path
	}

	if runtime.GOOS == "windows" {
		return filepath.Join(models.UserHomeDir(), ".local", "bin", "skills.exe")
	}
	return filepath.Join(models.UserHomeDir(), ".local", "bin", "skills")
}

func ParseSemver(v string) [3]int {
	clean := strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(strings.Split(clean, "-")[0], ".")
	var res [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		num, _ := strconv.Atoi(parts[i])
		res[i] = num
	}
	return res
}

func CompareSemver(v1, v2 string) int {
	s1, pre1 := parseSemver(v1)
	s2, pre2 := parseSemver(v2)
	for i := 0; i < 3; i++ {
		if s1[i] > s2[i] {
			return 1
		}
		if s1[i] < s2[i] {
			return -1
		}
	}
	if pre1 == "" && pre2 != "" {
		return 1
	}
	if pre1 != "" && pre2 == "" {
		return -1
	}
	return comparePrerelease(pre1, pre2)
}

func parseSemver(v string) ([3]int, string) {
	clean := strings.TrimPrefix(strings.TrimSpace(v), "v")
	clean = strings.SplitN(clean, "+", 2)[0]
	parts := strings.SplitN(clean, "-", 2)
	version := ParseSemver(parts[0])
	if len(parts) == 2 {
		return version, parts[1]
	}
	return version, ""
}

func comparePrerelease(v1, v2 string) int {
	if v1 == v2 {
		return 0
	}
	parts1, parts2 := strings.Split(v1, "."), strings.Split(v2, ".")
	for i := 0; i < len(parts1) && i < len(parts2); i++ {
		p1, p2 := parts1[i], parts2[i]
		n1, err1 := strconv.Atoi(p1)
		n2, err2 := strconv.Atoi(p2)
		switch {
		case err1 == nil && err2 == nil:
			if n1 > n2 {
				return 1
			}
			if n1 < n2 {
				return -1
			}
		case err1 == nil:
			return -1
		case err2 == nil:
			return 1
		case p1 > p2:
			return 1
		case p1 < p2:
			return -1
		}
	}
	if len(parts1) > len(parts2) {
		return 1
	}
	if len(parts1) < len(parts2) {
		return -1
	}
	return 0
}

func IsNewerVersion(latest, current string) bool {
	return CompareSemver(latest, current) > 0
}

func FetchReleaseInfo(versionTag string, timeoutSec int) (*ReleaseInfo, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", GitHubRepo)
	if versionTag != "" {
		tag := versionTag
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		apiURL = fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", GitHubRepo, tag)
	}

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "skills-manager/"+Version)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch release info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("release not found: %s", versionTag)
	}
	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("GitHub API rate limit exceeded")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API error HTTP %d", resp.StatusCode)
	}

	var rel ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("failed to decode release JSON: %w", err)
	}

	return &rel, nil
}

func FindMatchingAsset(assets []ReleaseAsset) *ReleaseAsset {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// 1. Precise OS & Arch match (e.g. skills_darwin_arm64.tar.gz or skills-linux-amd64)
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if (strings.Contains(name, goos) || (goos == "darwin" && strings.Contains(name, "macos"))) &&
			(strings.Contains(name, goarch) || (goarch == "amd64" && strings.Contains(name, "x86_64"))) {
			return &asset
		}
	}

	// 2. Generic name match ("skills" or "skills.exe")
	for _, asset := range assets {
		if asset.Name == "skills" || (goos == "windows" && asset.Name == "skills.exe") {
			return &asset
		}
	}

	// 3. Fallback to any standalone binary
	if len(assets) == 1 {
		return &assets[0]
	}

	return nil
}

func CheckSelfUpdate(targetVersion string) (*SelfUpdateInfo, error) {
	currentV := Version
	rel, err := FetchReleaseInfo(targetVersion, 10)
	if err != nil {
		return nil, err
	}

	latestTag := strings.TrimSpace(rel.TagName)
	latestV := strings.TrimPrefix(latestTag, "v")

	matchedAsset := FindMatchingAsset(rel.Assets)

	isNewer := false
	if targetVersion != "" {
		isNewer = (latestV != strings.TrimPrefix(targetVersion, "v"))
	} else {
		isNewer = IsNewerVersion(latestV, currentV)
	}

	info := &SelfUpdateInfo{
		CurrentVersion:  currentV,
		LatestVersion:   latestV,
		LatestTag:       latestTag,
		UpdateAvailable: isNewer,
		ReleaseNotes:    rel.Body,
		HTMLURL:         rel.HTMLURL,
	}

	if matchedAsset != nil {
		info.AssetURL = matchedAsset.BrowserDownloadURL
		info.AssetName = matchedAsset.Name
		info.AssetSize = matchedAsset.Size
	}

	return info, nil
}

func extractBinaryFromTarGz(data []byte, targetName string) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		baseName := filepath.Base(header.Name)
		if baseName == targetName || baseName == "skills" || baseName == "skills.exe" {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, tr); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		}
	}

	return nil, fmt.Errorf("executable not found in archive")
}

func extractBinaryFromZip(data []byte, targetName string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	for _, file := range zr.File {
		baseName := filepath.Base(file.Name)
		if baseName == targetName || baseName == "skills" || baseName == "skills.exe" {
			rc, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()

			var buf bytes.Buffer
			if _, err := io.Copy(&buf, rc); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		}
	}

	return nil, fmt.Errorf("executable not found in zip archive")
}

func DownloadAndInstallBinary(assetURL string, targetPath string, timeoutSec int) (string, error) {
	dest := targetPath
	if dest == "" {
		dest = GetCurrentExecutablePath()
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("failed to create target directory: %w", err)
	}

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	req, err := http.NewRequest("GET", assetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "skills-manager/"+Version)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}

	downloadBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var binaryBytes []byte
	lowerURL := strings.ToLower(assetURL)
	if strings.HasSuffix(lowerURL, ".tar.gz") || strings.HasSuffix(lowerURL, ".tgz") {
		b, err := extractBinaryFromTarGz(downloadBytes, filepath.Base(dest))
		if err != nil {
			return "", fmt.Errorf("archive extraction failed: %w", err)
		}
		binaryBytes = b
	} else if strings.HasSuffix(lowerURL, ".zip") {
		b, err := extractBinaryFromZip(downloadBytes, filepath.Base(dest))
		if err != nil {
			return "", fmt.Errorf("zip extraction failed: %w", err)
		}
		binaryBytes = b
	} else {
		binaryBytes = downloadBytes
	}

	if len(binaryBytes) < 100 {
		return "", fmt.Errorf("downloaded binary is too small or corrupted")
	}

	// Write to temporary file in same directory for atomic rename
	tmpFile, err := os.CreateTemp(filepath.Dir(dest), "skills_update_*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(binaryBytes); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("failed to write binary: %w", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return "", fmt.Errorf("failed to set executable permission: %w", err)
	}

	// On Windows, rename old binary if exists
	if runtime.GOOS == "windows" {
		oldBackup := dest + ".old"
		_ = os.Remove(oldBackup)
		_ = os.Rename(dest, oldBackup)
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		return "", fmt.Errorf("failed to replace binary at %s: %w", dest, err)
	}

	return dest, nil
}
