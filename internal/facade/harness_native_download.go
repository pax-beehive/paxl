package facade

import (
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

const claudeDownloadRoot = "https://downloads.claude.ai/claude-code-releases/"

const maxClaudeBinarySize int64 = 1 << 30

type claudeNativeArtifact struct {
	Binary   string `json:"binary"`
	Checksum string `json:"checksum"`
	Size     int64  `json:"size"`
}

type claudeNativeManifest struct {
	Version   string                          `json:"version"`
	Platforms map[string]claudeNativeArtifact `json:"platforms"`
}

func (f *HarnessUpgradeFacade) resolveHarnessTarget(
	ctx context.Context,
	installed *HarnessInstallation,
) (string, error) {
	if installed.Source != "native" {
		return resolveLatestHarnessVersion(ctx, installed.Package)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	data, err := f.claudeMetadata(ctx, "latest", 128)
	if err != nil {
		return "", fmt.Errorf("resolve latest native Claude: %w", err)
	}
	version := strings.TrimSpace(string(data))
	if !harnessVersionPattern.MatchString(version) {
		return "", fmt.Errorf("native Claude latest response is not an exact version")
	}
	return version, nil
}

func (f *HarnessUpgradeFacade) claudeResponse(
	ctx context.Context,
	suffix string,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeDownloadRoot+suffix, nil)
	if err != nil {
		return nil, fmt.Errorf("create native Claude request: %w", err)
	}
	resp, err := NewArtifactHTTPClient(f.nativeHTTP).Do(req)
	if err != nil {
		return nil, fmt.Errorf("download native Claude: %w", SanitizeArtifactHTTPError(err))
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("native Claude download returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func (f *HarnessUpgradeFacade) claudeMetadata(
	ctx context.Context,
	suffix string,
	limit int64,
) ([]byte, error) {
	resp, err := f.claudeResponse(ctx, suffix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read native Claude metadata: %w", SanitizeArtifactHTTPError(err))
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("native Claude metadata exceeds size limit")
	}
	return data, nil
}

func claudeNativePlatform() (string, error) {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	if (runtime.GOOS != "darwin" && runtime.GOOS != "linux") || (arch != "x64" && arch != "arm64") {
		return "", fmt.Errorf(
			"unsupported native Claude platform %s/%s",
			runtime.GOOS,
			runtime.GOARCH,
		)
	}
	platform := runtime.GOOS + "-" + arch
	if runtime.GOOS == "linux" {
		loaders, _ := filepath.Glob("/lib/ld-musl-*.so.1")
		if len(loaders) > 0 {
			platform += "-musl"
		}
	}
	return platform, nil
}

func (f *HarnessUpgradeFacade) claudeArtifact(
	ctx context.Context,
	version, platform string,
) (*claudeNativeArtifact, error) {
	data, err := f.claudeMetadata(ctx, version+"/manifest.json", 1<<20)
	if err != nil {
		return nil, err
	}
	var manifest claudeNativeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode native Claude manifest: %w", err)
	}
	artifact, ok := manifest.Platforms[platform]
	if manifest.Version != version || !ok || artifact.Binary != "claude" || artifact.Size <= 0 ||
		artifact.Size > maxClaudeBinarySize {
		return nil, fmt.Errorf("invalid native Claude manifest for %s/%s", version, platform)
	}
	checksum, err := hex.DecodeString(artifact.Checksum)
	if err != nil || len(checksum) != sha256.Size {
		return nil, fmt.Errorf("invalid native Claude checksum")
	}
	return &artifact, nil
}

func (f *HarnessUpgradeFacade) downloadClaudeNative(
	ctx context.Context,
	version, target string,
) error {
	platform, err := claudeNativePlatform()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	artifact, err := f.claudeArtifact(ctx, version, platform)
	if err != nil {
		return err
	}
	resp, err := f.claudeResponse(ctx, version+"/"+platform+"/claude")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return fmt.Errorf("create native Claude directory: %w", err)
	}
	file, err := os.OpenFile(
		target,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0600,
	) // #nosec G304 -- Target is inside the private staging directory.
	if err != nil {
		return fmt.Errorf("create native Claude binary: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, artifact.Size+1))
	if err != nil {
		return fmt.Errorf("write native Claude binary: %w", SanitizeArtifactHTTPError(err))
	}
	if size != artifact.Size ||
		!strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), artifact.Checksum) {
		return fmt.Errorf("native Claude binary size or SHA-256 checksum mismatch")
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync native Claude binary: %w", err)
	}
	if err := file.Chmod(0755); err != nil {
		return fmt.Errorf("make native Claude executable: %w", err)
	} // #nosec G302 -- Verified executable must be runnable.
	return nil
}
