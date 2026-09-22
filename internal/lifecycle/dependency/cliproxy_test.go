package dependency

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveAssetUsesPinnedOfficialRelease(t *testing.T) {
	asset, err := ResolveAsset(DefaultCLIProxyAPIVersion, "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if asset.Name != "CLIProxyAPI_7.3.11_darwin_aarch64.tar.gz" {
		t.Fatalf("asset name = %q", asset.Name)
	}
	if asset.SHA256 != "5671e4c7cf96919f35bd2bd76c68c6d57448219de655f5ca2db186b9534b7754" {
		t.Fatalf("checksum = %q", asset.SHA256)
	}
	if _, err := ResolveAsset("9.9.9", "darwin", "arm64"); err == nil {
		t.Fatal("unpinned version was accepted")
	}
}

func TestResolveAssetUsesPinnedWindowsZip(t *testing.T) {
	for _, test := range []struct {
		arch     string
		name     string
		checksum string
	}{
		{"arm64", "CLIProxyAPI_7.3.11_windows_aarch64.zip", "dee6f38286d03b4c267d6baa823484681ff2aeeda14d73c55f979f42871f0afc"},
		{"amd64", "CLIProxyAPI_7.3.11_windows_amd64.zip", "510301e28ef5459d359bf2b508c014894b6601d1ba26e2e7800cc0964621c615"},
	} {
		asset, err := ResolveAsset(DefaultCLIProxyAPIVersion, "windows", test.arch)
		if err != nil {
			t.Fatal(err)
		}
		if asset.Name != test.name || asset.SHA256 != test.checksum {
			t.Errorf("ResolveAsset(windows/%s) = %#v", test.arch, asset)
		}
	}
}

func TestOfficialPinnedAssetDownload(t *testing.T) {
	if os.Getenv("CLIPROXY_E2E_DOWNLOAD") != "1" {
		t.Skip("set CLIPROXY_E2E_DOWNLOAD=1 to verify the pinned upstream release")
	}
	asset, err := CurrentAsset(DefaultCLIProxyAPIVersion)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "cli-proxy-api")
	if _, err := (Installer{}).Install(context.Background(), asset, destination, ""); err != nil {
		t.Fatal(err)
	}
	output, _ := exec.Command(destination, "--version").CombinedOutput()
	if !strings.Contains(string(output), "CLIProxyAPI Version: "+DefaultCLIProxyAPIVersion) {
		t.Fatalf("unexpected version output: %s", output)
	}
}

func TestOfficialPinnedWindowsAssetDownloads(t *testing.T) {
	if os.Getenv("CLIPROXY_E2E_WINDOWS_DOWNLOAD") != "1" {
		t.Skip("set CLIPROXY_E2E_WINDOWS_DOWNLOAD=1 to verify pinned Windows releases")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			asset, err := ResolveAsset(DefaultCLIProxyAPIVersion, "windows", arch)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "cli-proxy-api.exe")
			if _, err := (Installer{}).Install(context.Background(), asset, destination, ""); err != nil {
				t.Fatal(err)
			}
			binary, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			if len(binary) < 2 || string(binary[:2]) != "MZ" {
				t.Fatalf("downloaded file does not have a Windows PE signature")
			}
		})
	}
}

func TestInstallVerifiesChecksumAndWritesPrivateState(t *testing.T) {
	archive := testArchive(t, "nested/cli-proxy-api", []byte("test-binary"))
	sum := sha256.Sum256(archive)
	asset := Asset{
		Version: "test",
		Name:    "release.tar.gz",
		URL:     "https://example.test/release.tar.gz",
		SHA256:  hex.EncodeToString(sum[:]),
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	})}
	dir := t.TempDir()
	destination := filepath.Join(dir, "bin", "cli-proxy-api")
	statePath := filepath.Join(dir, "state", "dependency.json")
	beforeWriteCalls := 0
	installer := Installer{Client: client, BeforeWrite: func() error {
		beforeWriteCalls++
		return nil
	}}
	if _, err := installer.Install(context.Background(), asset, destination, statePath); err != nil {
		t.Fatal(err)
	}
	if beforeWriteCalls != 1 {
		t.Fatalf("before-write calls = %d", beforeWriteCalls)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "test-binary" {
		t.Fatalf("binary = %q, %v", data, err)
	}
	if info, _ := os.Stat(destination); runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatalf("binary mode = %o", info.Mode().Perm())
	}
	if info, _ := os.Stat(statePath); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o", info.Mode().Perm())
	}

	asset.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := installer.Install(context.Background(), asset, destination, statePath); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	if beforeWriteCalls != 1 {
		t.Fatalf("before-write ran before checksum verification: %d calls", beforeWriteCalls)
	}
}

func TestInstallWindowsZip(t *testing.T) {
	archive := testZipArchive(t, "nested/cli-proxy-api.exe", []byte("windows-binary"))
	sum := sha256.Sum256(archive)
	asset := Asset{
		Version: "test",
		Name:    "release.zip",
		URL:     "https://example.test/release.zip",
		SHA256:  hex.EncodeToString(sum[:]),
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	})}
	destination := filepath.Join(t.TempDir(), "bin", "cli-proxy-api.exe")
	if _, err := (Installer{Client: client}).Install(context.Background(), asset, destination, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "windows-binary" {
		t.Fatalf("binary = %q, %v", data, err)
	}
}

func TestDownloadRetriesTransientErrorsOnly(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, io.ErrUnexpectedEOF
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("archive")),
		}, nil
	})}
	installer := Installer{MaxAttempts: 2, RetryDelay: time.Nanosecond}
	data, err := installer.download(context.Background(), client, "https://example.test/release")
	if err != nil || string(data) != "archive" || attempts != 2 {
		t.Fatalf("download = %q, attempts = %d, error = %v", data, attempts, err)
	}

	attempts = 0
	client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("missing")),
		}, nil
	})
	_, err = installer.download(context.Background(), client, "https://example.test/missing")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || attempts != 1 {
		t.Fatalf("non-retryable download attempts = %d, error = %v", attempts, err)
	}
}

func TestZipExtractionRejectsUnsafePathAndSymlink(t *testing.T) {
	unsafe := testZipArchive(t, "../cli-proxy-api.exe", []byte("bad"))
	if _, err := extractBinary("release.zip", unsafe); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe archive error = %v", err)
	}

	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: "cli-proxy-api.exe", Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("target")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractBinary("release.zip", buffer.Bytes()); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink archive error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testArchive(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testZipArchive(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	entry, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
