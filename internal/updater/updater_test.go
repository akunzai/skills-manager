package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSemver(t *testing.T) {
	v1 := ParseSemver("v0.2.0")
	if v1 != [3]int{0, 2, 0} {
		t.Errorf("expected [0, 2, 0], got %v", v1)
	}

	v2 := ParseSemver("1.10.5-beta.1")
	if v2 != [3]int{1, 10, 5} {
		t.Errorf("expected [1, 10, 5], got %v", v2)
	}

	if !IsNewerVersion("0.3.0", "0.2.0") {
		t.Errorf("expected 0.3.0 > 0.2.0")
	}
	if IsNewerVersion("0.1.9", "0.2.0") {
		t.Errorf("expected 0.1.9 not > 0.2.0")
	}
	if CompareSemver("0.4.0", "0.3.0") <= 0 {
		t.Errorf("expected 0.4.0 > 0.3.0")
	}
	if CompareSemver("0.3.0", "0.3.0") != 0 {
		t.Errorf("expected 0.3.0 == 0.3.0")
	}
	if CompareSemver("0.4.0", "0.4.0-rc.1") <= 0 {
		t.Errorf("expected release to be newer than pre-release")
	}
	if CompareSemver("0.4.0-rc.2", "0.4.0-rc.1") <= 0 {
		t.Errorf("expected later pre-release to be newer")
	}
	if CompareSemver("0.4.0-beta", "0.4.0-beta.1") >= 0 {
		t.Errorf("expected shorter pre-release to sort first")
	}
}

func TestFindMatchingAsset(t *testing.T) {
	assets := []ReleaseAsset{
		{Name: "checksums.txt", BrowserDownloadURL: "http://example.com/sums"},
		{Name: "skills_linux_amd64.tar.gz.evil", BrowserDownloadURL: "http://example.com/evil"},
		{Name: "skills_linux_amd64.tar.gz", BrowserDownloadURL: "http://example.com/linux"},
		{Name: "skills_darwin_arm64.tar.gz", BrowserDownloadURL: "http://example.com/darwin-arm"},
		{Name: "skills_windows_amd64.zip", BrowserDownloadURL: "http://example.com/win"},
	}

	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "http://example.com/linux"},
		{"darwin", "arm64", "http://example.com/darwin-arm"},
		{"windows", "amd64", "http://example.com/win"},
		{"linux", "arm64", ""},
	}
	for _, tt := range tests {
		got := findAsset(assets, ArchiveName(tt.goos, tt.goarch))
		switch {
		case tt.want == "" && got != nil:
			t.Errorf("%s/%s: matched %q; want none", tt.goos, tt.goarch, got.Name)
		case tt.want != "" && (got == nil || got.BrowserDownloadURL != tt.want):
			t.Errorf("%s/%s: matched %v; want %s", tt.goos, tt.goarch, got, tt.want)
		}
	}

	if FindMatchingAsset([]ReleaseAsset{{Name: "skills", BrowserDownloadURL: "http://example.com/bare"}}) != nil {
		t.Errorf("a lone asset with another name must not match")
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("archive")
	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:])
	manifest := []byte("deadbeef  skills_linux_arm64.tar.gz\n" + good + "  skills_linux_amd64.tar.gz\n")

	if err := VerifyChecksum(manifest, "skills_linux_amd64.tar.gz", data); err != nil {
		t.Errorf("matching checksum rejected: %v", err)
	}
	if err := VerifyChecksum(manifest, "skills_linux_arm64.tar.gz", data); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("mismatched checksum error = %v; want mismatch", err)
	}
	if err := VerifyChecksum(manifest, "skills_darwin_arm64.tar.gz", data); err == nil || !strings.Contains(err.Error(), "no checksum") {
		t.Errorf("unlisted archive error = %v; want no checksum", err)
	}
}

func TestDownloadAndInstallBinaryVerifiesChecksum(t *testing.T) {
	binary := bytes.Repeat([]byte("x"), 200)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{Name: "skills", Mode: 0755, Size: int64(len(binary))})
	_, _ = tw.Write(binary)
	_ = tw.Close()
	_ = gw.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)

	tests := []struct {
		name     string
		manifest string
		noSums   bool
		wantErr  string
	}{
		{name: "match", manifest: hex.EncodeToString(sum[:]) + "  skills_linux_amd64.tar.gz\n"},
		{name: "mismatch", manifest: strings.Repeat("0", 64) + "  skills_linux_amd64.tar.gz\n", wantErr: "mismatch"},
		{name: "unlisted", manifest: hex.EncodeToString(sum[:]) + "  other.tar.gz\n", wantErr: "no checksum"},
		{name: "no manifest", noSums: true, wantErr: "unverified"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "checksums.txt") {
					_, _ = w.Write([]byte(tt.manifest))
					return
				}
				_, _ = w.Write(archive)
			}))
			defer srv.Close()

			dest := filepath.Join(t.TempDir(), "skills")
			if err := os.WriteFile(dest, []byte("old"), 0755); err != nil {
				t.Fatal(err)
			}
			sumsURL := srv.URL + "/checksums.txt"
			if tt.noSums {
				sumsURL = ""
			}

			_, err := DownloadAndInstallBinary(srv.URL+"/skills_linux_amd64.tar.gz", sumsURL, dest, 5)
			got, _ := os.ReadFile(dest)
			if tt.wantErr == "" {
				if err != nil || !bytes.Equal(got, binary) {
					t.Fatalf("err = %v, installed %d bytes; want the new binary", err, len(got))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v; want %q", err, tt.wantErr)
			}
			if string(got) != "old" {
				t.Fatalf("binary replaced despite %s", tt.name)
			}
		})
	}
}

func TestTarGzExtraction(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := []byte("#!/bin/sh\necho test\n")
	header := &tar.Header{
		Name: "skills",
		Mode: 0755,
		Size: int64(len(content)),
	}
	_ = tw.WriteHeader(header)
	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gw.Close()

	extracted, err := extractBinaryFromTarGz(buf.Bytes(), "skills")
	if err != nil {
		t.Fatalf("extractBinaryFromTarGz failed: %v", err)
	}
	if string(extracted) != string(content) {
		t.Errorf("extracted content mismatch: %q", string(extracted))
	}
}

func TestZipExtraction(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	content := []byte("#!/bin/sh\necho test\n")
	w, _ := zw.Create("skills")
	_, _ = w.Write(content)
	_ = zw.Close()

	extracted, err := extractBinaryFromZip(buf.Bytes(), "skills")
	if err != nil {
		t.Fatalf("extractBinaryFromZip failed: %v", err)
	}
	if string(extracted) != string(content) {
		t.Errorf("extracted content mismatch: %q", string(extracted))
	}
}
