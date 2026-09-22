package dependency

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultCLIProxyAPIVersion = "7.3.11"
	maxArchiveSize            = 128 << 20
)

type Asset struct {
	Version string
	Name    string
	URL     string
	SHA256  string
}

type State struct {
	Version     string `json:"version"`
	SourceURL   string `json:"source_url"`
	SHA256      string `json:"sha256"`
	Destination string `json:"destination"`
	InstalledAt string `json:"installed_at"`
}

type Installer struct {
	Client      *http.Client
	BeforeWrite func() error
	MaxAttempts int
	RetryDelay  time.Duration
}

var pinnedChecksums = map[string]string{
	"darwin/aarch64":  "5671e4c7cf96919f35bd2bd76c68c6d57448219de655f5ca2db186b9534b7754",
	"darwin/amd64":    "dc4a75ef7256667959e2eb7e5c37bec5178f08f36657becf73fa65ef8a1cc118",
	"linux/aarch64":   "8c5a2bd09aca61b2d7753bee25609e0483c9047dee383b08fa5c3527aa9a75ae",
	"linux/amd64":     "4fc3e20aa6ab896316ac70633c8ed08b10d4219d1c3075d5b145b422c6bfab64",
	"windows/aarch64": "dee6f38286d03b4c267d6baa823484681ff2aeeda14d73c55f979f42871f0afc",
	"windows/amd64":   "510301e28ef5459d359bf2b508c014894b6601d1ba26e2e7800cc0964621c615",
}

func ResolveAsset(version, goos, goarch string) (Asset, error) {
	if version == "" {
		version = DefaultCLIProxyAPIVersion
	}
	if version != DefaultCLIProxyAPIVersion {
		return Asset{}, fmt.Errorf("CLIProxyAPI version %s is not pinned by this Gateway release", version)
	}
	arch := goarch
	if arch == "arm64" {
		arch = "aarch64"
	}
	checksum, ok := pinnedChecksums[goos+"/"+arch]
	if !ok {
		return Asset{}, fmt.Errorf("CLIProxyAPI managed install is unsupported on %s/%s", goos, goarch)
	}
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	name := fmt.Sprintf("CLIProxyAPI_%s_%s_%s%s", version, goos, arch, extension)
	return Asset{
		Version: version,
		Name:    name,
		URL:     fmt.Sprintf("https://github.com/router-for-me/CLIProxyAPI/releases/download/v%s/%s", version, name),
		SHA256:  checksum,
	}, nil
}

func CurrentAsset(version string) (Asset, error) {
	return ResolveAsset(version, runtime.GOOS, runtime.GOARCH)
}

func DefaultBinaryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		root := os.Getenv("LOCALAPPDATA")
		if root == "" {
			root = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(root, "codex-cliproxy-gateway", "bin", "cli-proxy-api.exe"), nil
	}
	return filepath.Join(home, ".local", "bin", "cli-proxy-api"), nil
}

func (i Installer) Install(ctx context.Context, asset Asset, destination, statePath string) (State, error) {
	if destination == "" {
		return State{}, fmt.Errorf("CLIProxyAPI destination is required")
	}
	client := i.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	archive, err := i.download(ctx, client, asset.URL)
	if err != nil {
		return State{}, err
	}
	sum := sha256.Sum256(archive)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actual, asset.SHA256) {
		return State{}, fmt.Errorf("CLIProxyAPI checksum mismatch: expected %s, got %s", asset.SHA256, actual)
	}
	binary, err := extractBinary(asset.Name, archive)
	if err != nil {
		return State{}, err
	}
	if i.BeforeWrite != nil {
		if err := i.BeforeWrite(); err != nil {
			return State{}, fmt.Errorf("prepare CLIProxyAPI update: %w", err)
		}
	}
	if err := writeExecutableAtomic(destination, binary); err != nil {
		return State{}, err
	}
	state := State{
		Version:     asset.Version,
		SourceURL:   asset.URL,
		SHA256:      actual,
		Destination: destination,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	if statePath != "" {
		if err := writeState(statePath, state); err != nil {
			return State{}, err
		}
	}
	return state, nil
}

func (i Installer) download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	attempts := i.MaxAttempts
	if attempts == 0 {
		attempts = 3
	}
	if attempts < 1 {
		return nil, fmt.Errorf("CLIProxyAPI download attempts must be positive")
	}
	delay := i.RetryDelay
	if delay == 0 {
		delay = 250 * time.Millisecond
	}
	var lastErr error
	usedAttempts := 0
	for attempt := 1; attempt <= attempts; attempt++ {
		usedAttempts = attempt
		archive, retry, err := downloadOnce(ctx, client, url)
		if err == nil {
			return archive, nil
		}
		lastErr = err
		if !retry || attempt == attempts {
			break
		}
		if err := waitForRetry(ctx, delay*time.Duration(attempt)); err != nil {
			return nil, fmt.Errorf("download CLIProxyAPI: %w", err)
		}
	}
	return nil, fmt.Errorf("download CLIProxyAPI after %d attempt(s): %w", usedAttempts, lastErr)
}

func downloadOnce(ctx context.Context, client *http.Client, url string) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return nil, retry, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	archive, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveSize+1))
	if err != nil {
		return nil, true, fmt.Errorf("read archive: %w", err)
	}
	if len(archive) > maxArchiveSize {
		return nil, false, fmt.Errorf("archive exceeds %d bytes", maxArchiveSize)
	}
	return archive, false, nil
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func extractBinary(assetName string, archive []byte) ([]byte, error) {
	switch {
	case strings.HasSuffix(strings.ToLower(assetName), ".tar.gz"):
		return extractTarGzipBinary(archive)
	case strings.HasSuffix(strings.ToLower(assetName), ".zip"):
		return extractZipBinary(archive)
	default:
		return nil, fmt.Errorf("unsupported CLIProxyAPI archive format %q", assetName)
	}
}

func extractTarGzipBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open CLIProxyAPI archive: %w", err)
	}
	defer gz.Close()
	tarReader := tar.NewReader(gz)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read CLIProxyAPI archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if err := validateArchivePath(header.Name); err != nil {
			return nil, err
		}
		base := strings.ToLower(path.Base(strings.ReplaceAll(header.Name, `\`, "/")))
		if base != "cli-proxy-api" && base != "cliproxyapi" {
			continue
		}
		if header.Size < 1 || header.Size > maxArchiveSize {
			return nil, fmt.Errorf("invalid CLIProxyAPI binary size %d", header.Size)
		}
		data, err := io.ReadAll(io.LimitReader(tarReader, maxArchiveSize+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) != header.Size {
			return nil, fmt.Errorf("truncated CLIProxyAPI binary")
		}
		return data, nil
	}
	return nil, fmt.Errorf("CLIProxyAPI archive contains no cli-proxy-api binary")
}

func extractZipBinary(archive []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open CLIProxyAPI archive: %w", err)
	}
	var binary []byte
	for _, file := range reader.File {
		if err := validateArchivePath(file.Name); err != nil {
			return nil, err
		}
		base := strings.ToLower(path.Base(strings.ReplaceAll(file.Name, `\`, "/")))
		if base != "cli-proxy-api.exe" && base != "cliproxyapi.exe" {
			continue
		}
		if !file.FileInfo().Mode().IsRegular() {
			return nil, fmt.Errorf("CLIProxyAPI archive binary is not a regular file")
		}
		if binary != nil {
			return nil, fmt.Errorf("CLIProxyAPI archive contains multiple matching binaries")
		}
		if file.UncompressedSize64 < 1 || file.UncompressedSize64 > maxArchiveSize {
			return nil, fmt.Errorf("invalid CLIProxyAPI binary size %d", file.UncompressedSize64)
		}
		entry, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open CLIProxyAPI binary: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(entry, maxArchiveSize+1))
		closeErr := entry.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read CLIProxyAPI binary: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close CLIProxyAPI binary: %w", closeErr)
		}
		if uint64(len(data)) != file.UncompressedSize64 {
			return nil, fmt.Errorf("truncated CLIProxyAPI binary")
		}
		binary = data
	}
	if binary == nil {
		return nil, fmt.Errorf("CLIProxyAPI archive contains no cli-proxy-api.exe binary")
	}
	return binary, nil
}

func validateArchivePath(name string) error {
	normalized := strings.ReplaceAll(name, `\`, "/")
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return fmt.Errorf("unsafe CLIProxyAPI archive path %q", name)
	}
	first := strings.SplitN(clean, "/", 2)[0]
	if strings.Contains(first, ":") {
		return fmt.Errorf("unsafe CLIProxyAPI archive path %q", name)
	}
	return nil
}

func writeExecutableAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".cli-proxy-api-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o755); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func writeState(path string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".cliproxyapi-state-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
