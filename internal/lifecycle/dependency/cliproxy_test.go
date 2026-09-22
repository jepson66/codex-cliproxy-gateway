package dependency

import (
	"archive/tar"
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
	"strings"
	"testing"
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

func TestInstallVerifiesChecksumAndWritesPrivateState(t *testing.T) {
	archive := testArchive(t, "nested/cli-proxy-api", []byte("test-binary"))
	sum := sha256.Sum256(archive)
	asset := Asset{
		Version: "test",
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
	if _, err := (Installer{Client: client}).Install(context.Background(), asset, destination, statePath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "test-binary" {
		t.Fatalf("binary = %q, %v", data, err)
	}
	if info, _ := os.Stat(destination); info.Mode().Perm() != 0o755 {
		t.Fatalf("binary mode = %o", info.Mode().Perm())
	}
	if info, _ := os.Stat(statePath); info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o", info.Mode().Perm())
	}

	asset.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := (Installer{Client: client}).Install(context.Background(), asset, destination, statePath); err == nil {
		t.Fatal("checksum mismatch was accepted")
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
