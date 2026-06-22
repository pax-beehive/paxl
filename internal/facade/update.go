package facade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const DefaultUpdateManifestURL = "https://storage.googleapis.com/pax-tech-bucket/paxl/releases/latest/stable/manifest.json"
const DefaultUpdateResolverURL = "https://api.paxtech.net/api/v1/public/artifacts/download"
const DefaultUpdateTag = "stable"

type UpdateStatus string

const (
	UpdateStatusUnknown     UpdateStatus = "unknown"
	UpdateStatusUpToDate    UpdateStatus = "up_to_date"
	UpdateStatusAvailable   UpdateStatus = "update_available"
	UpdateStatusAhead       UpdateStatus = "ahead"
	UpdateStatusDevelopment UpdateStatus = "development"
)

type CheckUpdateRequest struct {
	CurrentVersion string
	CurrentCommit  string
	ManifestURL    string
	ResolverURL    string
	Platform       string
	Tag            string
}

type CheckUpdateResponse struct {
	CurrentVersion  string       `json:"current_version"`
	CurrentCommit   string       `json:"current_commit,omitempty"`
	LatestVersion   string       `json:"latest_version"`
	Status          UpdateStatus `json:"status"`
	UpdateAvailable bool         `json:"update_available"`
	Platform        string       `json:"platform"`
	DownloadURL     string       `json:"download_url"`
	SHA256          string       `json:"sha256"`
	SizeBytes       int64        `json:"size_bytes"`
	CheckedAt       time.Time    `json:"checked_at"`
}

type UpdateFacade struct {
	client UpdateHTTPClient
}

type UpdateHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

func NewUpdateFacade(client UpdateHTTPClient) *UpdateFacade {
	if client == nil {
		client = http.DefaultClient
	}
	return &UpdateFacade{client: client}
}

func (f *UpdateFacade) Check(
	ctx context.Context,
	req *CheckUpdateRequest,
	opts ...func(*Option),
) (*CheckUpdateResponse, error) {
	_ = applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("check update request is required")
	}
	platform := firstNonEmpty(req.Platform, currentPlatform())
	artifact, err := f.resolveArtifact(ctx, req, platform)
	if err != nil {
		return nil, err
	}
	status, updateAvailable, err := compareUpdateVersions(req.CurrentVersion, artifact.Version)
	if err != nil {
		return nil, err
	}
	return &CheckUpdateResponse{
		CurrentVersion:  req.CurrentVersion,
		CurrentCommit:   req.CurrentCommit,
		LatestVersion:   artifact.Version,
		Status:          status,
		UpdateAvailable: updateAvailable,
		Platform:        platform,
		DownloadURL:     normalizeArtifactURL(artifact.URL),
		SHA256:          artifact.SHA256,
		SizeBytes:       artifact.Size,
		CheckedAt:       time.Now().UTC(),
	}, nil
}

func (f *UpdateFacade) resolveArtifact(
	ctx context.Context,
	req *CheckUpdateRequest,
	platform string,
) (*updateArtifact, error) {
	if strings.TrimSpace(req.ManifestURL) != "" {
		manifest, err := f.fetchManifest(ctx, req.ManifestURL)
		if err != nil {
			return nil, fmt.Errorf("fetch update manifest: %w", err)
		}
		artifact, ok := manifest.artifactForPlatform(platform)
		if !ok {
			return nil, fmt.Errorf("missing paxl artifact for platform %q", platform)
		}
		return &updateArtifact{
			Version: manifest.Version,
			URL:     artifact.StorageURL,
			SHA256:  artifact.SHA256,
			Size:    artifact.Size,
		}, nil
	}
	artifact, err := f.fetchResolverArtifact(
		ctx,
		firstNonEmpty(req.ResolverURL, DefaultUpdateResolverURL),
		platform,
		firstNonEmpty(req.Tag, DefaultUpdateTag),
	)
	if err != nil {
		return nil, fmt.Errorf("fetch update resolver: %w", err)
	}
	return artifact, nil
}

func (f *UpdateFacade) fetchManifest(
	ctx context.Context,
	manifestURL string,
) (*updateManifest, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil) // #nosec G107
	if err != nil {
		return nil, fmt.Errorf("create manifest request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "paxl-update-check")
	resp, err := f.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request manifest: %w", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("manifest returned HTTP %d", resp.StatusCode)
	}
	var manifest updateManifest
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := manifest.validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (f *UpdateFacade) fetchResolverArtifact(
	ctx context.Context,
	resolverURL string,
	platform string,
	tag string,
) (*updateArtifact, error) {
	endpoint, err := url.Parse(resolverURL)
	if err != nil {
		return nil, fmt.Errorf("parse resolver URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("product", "paxl")
	query.Set("platform", platform)
	query.Set("tags", tag)
	endpoint.RawQuery = query.Encode()

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	) // #nosec G107
	if err != nil {
		return nil, fmt.Errorf("create resolver request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "paxl-update-check")
	resp, err := f.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request resolver: %w", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("resolver returned HTTP %d", resp.StatusCode)
	}
	var resolverResp updateResolverResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&resolverResp); err != nil {
		return nil, fmt.Errorf("decode resolver response: %w", err)
	}
	artifact := resolverResp.Data.toArtifact()
	if err := artifact.validate(); err != nil {
		return nil, err
	}
	return artifact, nil
}

type updateManifest struct {
	Product   string                    `json:"product"`
	Version   string                    `json:"version"`
	Artifacts []*updateManifestArtifact `json:"artifacts"`
}

type updateManifestArtifact struct {
	Platform   string `json:"platform"`
	File       string `json:"file"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	StorageURL string `json:"storage_url"`
}

type updateResolverResponse struct {
	Data updateResolverArtifact `json:"data"`
}

type updateResolverArtifact struct {
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Version   string `json:"version"`
	Product   string `json:"product"`
	Platform  string `json:"platform"`
	SizeBytes int64  `json:"size_bytes"`
	Size      int64  `json:"size"`
}

func (a *updateResolverArtifact) toArtifact() *updateArtifact {
	size := a.SizeBytes
	if size == 0 {
		size = a.Size
	}
	return &updateArtifact{
		Product:  a.Product,
		Platform: a.Platform,
		Version:  a.Version,
		URL:      a.URL,
		SHA256:   a.SHA256,
		Size:     size,
	}
}

type updateArtifact struct {
	Product  string
	Platform string
	Version  string
	URL      string
	SHA256   string
	Size     int64
}

func (a *updateArtifact) validate() error {
	if a.Product != "" && a.Product != "paxl" {
		return fmt.Errorf("resolver product %q is not paxl", a.Product)
	}
	if strings.TrimSpace(a.Version) == "" {
		return fmt.Errorf("resolver version is required")
	}
	if strings.TrimSpace(a.URL) == "" {
		return fmt.Errorf("resolver download URL is required")
	}
	if strings.TrimSpace(a.SHA256) == "" {
		return fmt.Errorf("resolver sha256 is required")
	}
	return nil
}

func (m *updateManifest) validate() error {
	if m.Product != "" && m.Product != "paxl" {
		return fmt.Errorf("manifest product %q is not paxl", m.Product)
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("manifest version is required")
	}
	return nil
}

func (m *updateManifest) artifactForPlatform(platform string) (*updateManifestArtifact, bool) {
	for _, artifact := range m.Artifacts {
		if artifact == nil {
			continue
		}
		if artifact.Platform == platform {
			return artifact, true
		}
	}
	return nil, false
}

func compareUpdateVersions(current string, latest string) (UpdateStatus, bool, error) {
	currentVersion, ok := parseOptionalSemver(current)
	if !ok {
		return UpdateStatusDevelopment, false, nil
	}
	latestVersion, err := parseSemver(latest)
	if err != nil {
		return UpdateStatusUnknown, false, fmt.Errorf("parse latest version: %w", err)
	}
	switch currentVersion.compare(latestVersion) {
	case -1:
		return UpdateStatusAvailable, true, nil
	case 0:
		return UpdateStatusUpToDate, false, nil
	default:
		return UpdateStatusAhead, false, nil
	}
}

func parseOptionalSemver(raw string) (*semver, bool) {
	version, err := parseSemver(raw)
	if err != nil {
		return nil, false
	}
	return version, true
}

type semver struct {
	major int
	minor int
	patch int
}

func parseSemver(raw string) (*semver, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	parts := strings.Split(clean, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("version %q is not semantic", raw)
	}
	major, err := parseSemverPart(parts[0], raw)
	if err != nil {
		return nil, err
	}
	minor, err := parseSemverPart(parts[1], raw)
	if err != nil {
		return nil, err
	}
	patch, err := parseSemverPart(parts[2], raw)
	if err != nil {
		return nil, err
	}
	return &semver{major: major, minor: minor, patch: patch}, nil
}

func parseSemverPart(part string, raw string) (int, error) {
	if part == "" {
		return 0, fmt.Errorf("version %q is not semantic", raw)
	}
	value, err := strconv.Atoi(part)
	if err != nil {
		return 0, fmt.Errorf("version %q is not semantic: %w", raw, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("version %q is not semantic", raw)
	}
	return value, nil
}

func (v *semver) compare(other *semver) int {
	if v.major != other.major {
		return compareInt(v.major, other.major)
	}
	if v.minor != other.minor {
		return compareInt(v.minor, other.minor)
	}
	return compareInt(v.patch, other.patch)
}

func compareInt(left int, right int) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func normalizeArtifactURL(raw string) string {
	if !strings.HasPrefix(raw, "gs://") {
		return raw
	}
	rest := strings.TrimPrefix(raw, "gs://")
	bucket, object, ok := strings.Cut(rest, "/")
	if !ok {
		return raw
	}
	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, object)
}

func currentPlatform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func closeBody(body io.Closer) {
	_ = body.Close()
}
