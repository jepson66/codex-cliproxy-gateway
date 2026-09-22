package dependency

import (
	"archive/tar"
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
	Client *http.Client
}

var pinnedChecksums = map[string]string{
	"darwin/aarch64": "5671e4c7cf96919f35bd2bd76c68c6d57448219de655f5ca2db186b9534b7754",
	"darwin/amd64":   "dc4a75ef7256667959e2eb7e5c37bec5178f08f36657becf73fa65ef8a1cc118",
	"linux/aarch64":  "8c5a2bd09aca61b2d7753bee25609e0483c9047dee383b08fa5c3527aa9a75ae",
	"linux/amd64":    "4fc3e20aa6ab896316ac70633c8ed08b10d4219d1c3075d5b145b422c6bfab64",
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
	name := fmt.Sprintf("CLIProxyAPI_%s_%s_%s.tar.gz", version, goos, arch)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return State{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return State{}, fmt.Errorf("download CLIProxyAPI: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return State{}, fmt.Errorf("download CLIProxyAPI: HTTP %d", resp.StatusCode)
	}
	archive, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveSize+1))
	if err != nil {
		return State{}, fmt.Errorf("read CLIProxyAPI archive: %w", err)
	}
	if len(archive) > maxArchiveSize {
		return State{}, fmt.Errorf("CLIProxyAPI archive exceeds %d bytes", maxArchiveSize)
	}
	sum := sha256.Sum256(archive)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actual, asset.SHA256) {
		return State{}, fmt.Errorf("CLIProxyAPI checksum mismatch: expected %s, got %s", asset.SHA256, actual)
	}
	binary, err := extractBinary(archive)
	if err != nil {
		return State{}, err
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

func extractBinary(archive []byte) ([]byte, error) {
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
		base := strings.ToLower(filepath.Base(header.Name))
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
