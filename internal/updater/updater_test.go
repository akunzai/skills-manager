package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
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
		{Name: "skills_checksums.txt", BrowserDownloadURL: "http://example.com/sums"},
		{Name: "skills_linux_amd64.tar.gz", BrowserDownloadURL: "http://example.com/linux"},
		{Name: "skills_darwin_arm64.tar.gz", BrowserDownloadURL: "http://example.com/darwin-arm"},
		{Name: "skills_windows_amd64.zip", BrowserDownloadURL: "http://example.com/win"},
	}

	matched := FindMatchingAsset(assets)
	if matched == nil {
		t.Fatalf("expected matched asset")
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
