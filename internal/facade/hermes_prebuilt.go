package facade

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const hermesArtifactOrigin = "https://hermes-assets.nousresearch.com"
const hermesMacFeed = hermesArtifactOrigin + "/releases/darwin/stable/stable-mac.yml"

var errHermesPrebuiltMissing = errors.New("no matching Hermes prebuilt artifact")

type hermesPrebuiltArtifact struct {
	Version string
	URL     string
	Digest  string
	SHA512  bool
	Size    int64
}

type hermesReleaseFeed struct {
	Version string `yaml:"version"`
	Files   []struct {
		URL    string `yaml:"url"`
		Digest string `yaml:"sha512"`
		Size   int64  `yaml:"size"`
	} `yaml:"files"`
}

func parseHermesPrebuiltFeed(data []byte, arch string) (*hermesPrebuiltArtifact, error) {
	var feed hermesReleaseFeed
	if err := yaml.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("parse Hermes prebuilt feed: %w", err)
	}
	if !harnessVersionPattern.MatchString(feed.Version) {
		return nil, fmt.Errorf("invalid Hermes prebuilt version")
	}
	for _, file := range feed.Files {
		if !strings.HasSuffix(file.URL, "-mac-"+arch+".zip") ||
			!strings.Contains(file.URL, "/HermesBundled-") {
			continue
		}
		target, err := url.Parse(file.URL)
		if err != nil {
			return nil, err
		}
		target = (&url.URL{Scheme: "https", Host: "hermes-assets.nousresearch.com"}).ResolveReference(
			target,
		)
		digest, err := base64.StdEncoding.DecodeString(file.Digest)
		if err != nil || len(digest) != sha512.Size || file.Size <= 0 ||
			file.Size > 4*1024*1024*1024 ||
			target.Scheme != "https" ||
			target.Host != "hermes-assets.nousresearch.com" ||
			target.User != nil {
			return nil, fmt.Errorf("invalid Hermes prebuilt artifact metadata")
		}
		return &hermesPrebuiltArtifact{
			Version: feed.Version,
			URL:     target.String(),
			Digest:  file.Digest,
			SHA512:  true,
			Size:    file.Size,
		}, nil
	}
	return nil, errHermesPrebuiltMissing
}

func (f *HarnessInstallFacade) hermesMetadata(
	ctx context.Context,
	target string,
) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "paxl")
	client := f.client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	const limit = 8 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if len(data) > limit {
		return nil, 0, fmt.Errorf("hermes metadata exceeds size limit")
	}
	return data, resp.StatusCode, err
}

// The stable feed can omit an architecture. Search older release receipts rather
// than silently selecting another architecture or invoking a source installer.
func (f *HarnessInstallFacade) resolveHermesPrebuilt(
	ctx context.Context,
	platform, arch string,
) (*hermesPrebuiltArtifact, error) {
	if platform != "darwin" {
		return nil, fmt.Errorf(
			"no verified standalone Hermes prebuilt installer is available for %s/%s; source compilation is disabled",
			platform,
			arch,
		)
	}
	if arch == "amd64" {
		arch = "x64"
	}
	if arch != "arm64" && arch != "x64" {
		return nil, fmt.Errorf("unsupported Hermes prebuilt architecture %s", arch)
	}
	data, status, err := f.hermesMetadata(ctx, hermesMacFeed)
	if err != nil {
		return nil, fmt.Errorf("fetch Hermes prebuilt feed: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf(
			"fetch Hermes prebuilt feed: HTTP %d; source compilation is disabled",
			status,
		)
	}
	artifact, err := parseHermesPrebuiltFeed(data, arch)
	if err != nil && !errors.Is(err, errHermesPrebuiltMissing) {
		return nil, err
	}
	if artifact != nil {
		return artifact, nil
	}
	return f.findHistoricalHermesPrebuilt(ctx, arch)
}

func (f *HarnessInstallFacade) findHistoricalHermesPrebuilt(
	ctx context.Context,
	arch string,
) (*hermesPrebuiltArtifact, error) {
	for page := 1; page <= 10; page++ {
		target := fmt.Sprintf(
			"https://api.github.com/repos/NousResearch/hermes-agent/releases?per_page=100&page=%d",
			page,
		)
		data, status, err := f.hermesMetadata(ctx, target)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("fetch Hermes release history: HTTP %d", status)
		}
		var releases []struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if err := json.Unmarshal(data, &releases); err != nil {
			return nil, fmt.Errorf("parse Hermes release history: %w", err)
		}
		for _, release := range releases {
			if release.Draft || release.Prerelease {
				continue
			}
			artifact, err := f.hermesReceiptArtifact(ctx, release.Tag, arch)
			if errors.Is(err, errHermesPrebuiltMissing) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if artifact != nil {
				return artifact, nil
			}
		}
		if len(releases) < 100 {
			break
		}
	}
	return nil, fmt.Errorf(
		"no published verified Hermes prebuilt package found for darwin/%s: %w",
		arch, errHermesPrebuiltMissing,
	)
}

func (f *HarnessInstallFacade) hermesReceiptArtifact(
	ctx context.Context,
	tag, arch string,
) (*hermesPrebuiltArtifact, error) {
	target := hermesArtifactOrigin + "/releases/tag/" + url.PathEscape(
		tag,
	) + "/handoff-darwin-" + arch + ".json"
	data, status, err := f.hermesMetadata(ctx, target)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, errHermesPrebuiltMissing
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("fetch Hermes artifact receipt: HTTP %d", status)
	}
	var receipt struct {
		Schema int    `json:"schema"`
		Tag    string `json:"tag"`
		Name   string `json:"name"`
		Files  []struct {
			Path   string `json:"path"`
			Size   int64  `json:"size"`
			Digest string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return nil, err
	}
	if receipt.Schema != 1 || receipt.Tag != tag || receipt.Name != "darwin-"+arch {
		return nil, fmt.Errorf("hermes artifact receipt identity mismatch")
	}
	for _, file := range receipt.Files {
		if !strings.HasPrefix(file.Path, "HermesBundled-") ||
			!strings.HasSuffix(file.Path, "-mac-"+arch+".zip") ||
			filepath.Base(file.Path) != file.Path {
			continue
		}
		digest, err := hex.DecodeString(file.Digest)
		if err != nil || len(digest) != sha256.Size || file.Size <= 0 ||
			file.Size > 4*1024*1024*1024 {
			return nil, fmt.Errorf("invalid Hermes artifact receipt")
		}
		return &hermesPrebuiltArtifact{
			Version: strings.TrimSuffix(
				strings.TrimPrefix(file.Path, "HermesBundled-"),
				"-mac-"+arch+".zip",
			),
			URL: hermesArtifactOrigin + "/releases/tag/" + url.PathEscape(
				tag,
			) + "/" + url.PathEscape(
				file.Path,
			),
			Digest: file.Digest,
			Size:   file.Size,
		}, nil
	}
	return nil, errHermesPrebuiltMissing
}

func (f *HarnessInstallFacade) installHermesPrebuilt(
	ctx context.Context,
	result *HarnessInstallResult,
	option *Option,
) (*HarnessInstallResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if option.Stderr != nil {
		_, _ = fmt.Fprintln(option.Stderr, "Finding the newest compatible prebuilt Hermes package.")
	}
	artifact, err := f.resolveHermesPrebuilt(ctx, runtime.GOOS, runtime.GOARCH)
	if errors.Is(err, errHermesPrebuiltMissing) && runtime.GOOS == "darwin" &&
		runtime.GOARCH == "amd64" {
		return f.installHermesSource(ctx, result, option)
	}
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	base := filepath.Join(home, ".local", "share", "paxl", "harnesses", "hermes")
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(base, ".install-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	archive := filepath.Join(stage, "hermes.zip")
	if option.Stderr != nil {
		_, _ = fmt.Fprintf(
			option.Stderr,
			"Downloading Hermes %s prebuilt package (%d MB).\n",
			artifact.Version,
			artifact.Size/1024/1024,
		)
	}
	if err := f.downloadHermesPrebuilt(ctx, artifact, archive); err != nil {
		return nil, err
	}
	runner := f.runner
	if runner == nil {
		runner = &nativeHarnessInstallRunner{input: option.Stdin, output: option.Stderr}
	}
	ditto, err := runner.LookPath("ditto")
	if err != nil {
		return nil, err
	}
	unpack := filepath.Join(stage, "bundle")
	if err := runner.Run(ctx, ditto, []string{"-x", "-k", archive, unpack}); err != nil {
		return nil, fmt.Errorf("extract Hermes bundle: %w", err)
	}
	launcher, err := hermesBundledLauncher(unpack)
	if err != nil {
		return nil, err
	}
	path, err := publishHermesBundle(home, stage, unpack, launcher, artifact.Version)
	if err != nil {
		return nil, err
	}
	result.Version, result.State, result.Package = artifact.Version, "installed", artifact.URL
	result.Command = []string{path}
	return result, nil
}

func publishHermesBundle(home, stage, unpack, launcher, version string) (string, error) {
	if !harnessVersionPattern.MatchString(version) {
		return "", fmt.Errorf("invalid Hermes artifact version")
	}
	relative, err := filepath.Rel(unpack, launcher)
	if err != nil {
		return "", err
	}
	// Preserve the complete signed app and its embedded relative runtime layout.
	destination := filepath.Join(
		filepath.Dir(stage),
		"bundle-"+version+"-"+strings.TrimPrefix(filepath.Base(stage), ".install-"),
	)
	if err := os.Rename(unpack, destination); err != nil {
		return "", err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(destination)
		}
	}()
	bindir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bindir, 0750); err != nil {
		return "", err
	}
	temporary := filepath.Join(bindir, ".hermes-"+filepath.Base(stage))
	if err := os.Symlink(filepath.Join(destination, relative), temporary); err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(temporary) }()
	path := filepath.Join(bindir, "hermes")
	if err := os.Rename(temporary, path); err != nil {
		return "", err
	}
	published = true
	return path, nil
}

func (f *HarnessInstallFacade) downloadHermesPrebuilt(
	ctx context.Context,
	artifact *hermesPrebuiltArtifact,
	destination string,
) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return err
	}
	client := f.client
	if client == nil {
		client = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || req.URL.Host != "hermes-assets.nousresearch.com" ||
				len(via) > 5 {
				return fmt.Errorf("untrusted Hermes artifact redirect")
			}
			return nil
		}}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download Hermes prebuilt: HTTP %d", resp.StatusCode)
	}
	file, err := os.OpenFile(
		destination,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0600,
	) // #nosec G304 -- Destination is a private staging path created by paxl.
	if err != nil {
		return err
	}
	hash := sha512.New()
	if !artifact.SHA512 {
		hash = sha256.New()
	}
	count, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, artifact.Size+1))
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if artifact.SHA512 {
		digest = base64.StdEncoding.EncodeToString(hash.Sum(nil))
	}
	if count != artifact.Size || digest != artifact.Digest {
		return fmt.Errorf("hermes prebuilt package size or checksum mismatch")
	}
	return nil
}

func hermesBundledLauncher(root string) (string, error) {
	apps, err := filepath.Glob(filepath.Join(root, "*.app"))
	if err != nil {
		return "", err
	}
	if len(apps) != 1 {
		return "", fmt.Errorf("hermes archive must contain one complete app bundle")
	}
	resources := filepath.Join(apps[0], "Contents", "Resources")
	data, err := os.ReadFile(
		filepath.Join(resources, "install-stamp.json"),
	) // #nosec G304 -- Manifest is read from the verified publisher bundle in private staging.
	if err != nil {
		return "", fmt.Errorf("read Hermes bundled runtime manifest: %w", err)
	}
	var stamp struct {
		Payload string `json:"payload"`
		Runtime struct {
			Commands map[string]string `json:"commands"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(data, &stamp); err != nil {
		return "", err
	}
	command := stamp.Runtime.Commands["hermes"]
	if stamp.Payload != "bundled" || command == "" || filepath.IsAbs(command) ||
		!filepath.IsLocal(command) {
		return "", fmt.Errorf("hermes package does not contain a prebuilt CLI runtime")
	}
	payload := filepath.Join(resources, "agent-payload")
	launcher := filepath.Join(payload, command)
	resolved, err := filepath.EvalSymlinks(launcher)
	if err != nil {
		return "", err
	}
	canonicalPayload, err := filepath.EvalSymlinks(payload)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(canonicalPayload, resolved)
	if err != nil || !filepath.IsLocal(relative) {
		return "", fmt.Errorf("hermes launcher escapes bundled runtime")
	}
	info, err := os.Stat(launcher)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("hermes bundled CLI launcher is not executable")
	}
	return launcher, nil
}
