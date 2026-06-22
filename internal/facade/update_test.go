package facade

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"
)

type UpdateFacadeSuite struct {
	suite.Suite
}

func TestUpdateFacadeSuite(t *testing.T) {
	suite.Run(t, new(UpdateFacadeSuite))
}

func (s *UpdateFacadeSuite) TestCheckReportsAvailableUpdateForCurrentPlatform() {
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		s.Equal("https://example.test/manifest.json", req.URL.String())
		return jsonResponse(`{
			"product": "paxl",
			"version": "0.1.1",
			"created_at": "2026-06-22T10:00:00Z",
			"artifacts": [
				{
					"platform": "darwin/arm64",
					"file": "paxl_0.1.1_darwin_arm64",
					"sha256": "abc123",
					"size": 42,
					"storage_url": "gs://pax-tech-bucket/paxl/releases/0.1.1/paxl_0.1.1_darwin_arm64"
				}
			]
		}`), nil
	})
	updateFacade := NewUpdateFacade(client)

	resp, err := updateFacade.Check(context.Background(), &CheckUpdateRequest{
		CurrentVersion: "0.1.0",
		CurrentCommit:  "local",
		ManifestURL:    "https://example.test/manifest.json",
		Platform:       "darwin/arm64",
	})

	s.Require().NoError(err)
	s.True(resp.UpdateAvailable)
	s.Equal(UpdateStatusAvailable, resp.Status)
	s.Equal("0.1.0", resp.CurrentVersion)
	s.Equal("0.1.1", resp.LatestVersion)
	s.Equal("darwin/arm64", resp.Platform)
	s.Equal("abc123", resp.SHA256)
	s.Equal(int64(42), resp.SizeBytes)
	s.Equal(
		"https://storage.googleapis.com/pax-tech-bucket/paxl/releases/0.1.1/paxl_0.1.1_darwin_arm64",
		resp.DownloadURL,
	)
}

func (s *UpdateFacadeSuite) TestCheckReportsUpToDateWhenVersionsMatch() {
	updateFacade := NewUpdateFacade(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(`{
			"product": "paxl",
			"version": "0.1.0",
			"artifacts": [{"platform": "linux/amd64", "sha256": "abc123", "size": 1, "storage_url": "https://example.test/paxl"}]
		}`), nil
	}))

	resp, err := updateFacade.Check(context.Background(), &CheckUpdateRequest{
		CurrentVersion: "0.1.0",
		ManifestURL:    "https://example.test/manifest.json",
		Platform:       "linux/amd64",
	})

	s.Require().NoError(err)
	s.False(resp.UpdateAvailable)
	s.Equal(UpdateStatusUpToDate, resp.Status)
}

func (s *UpdateFacadeSuite) TestCheckTreatsDevelopmentBuildAsUnknownAgainstLatest() {
	updateFacade := NewUpdateFacade(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(`{
			"product": "paxl",
			"version": "0.1.1",
			"artifacts": [{"platform": "linux/amd64", "sha256": "abc123", "size": 1, "storage_url": "https://example.test/paxl"}]
		}`), nil
	}))

	resp, err := updateFacade.Check(context.Background(), &CheckUpdateRequest{
		CurrentVersion: "dev",
		ManifestURL:    "https://example.test/manifest.json",
		Platform:       "linux/amd64",
	})

	s.Require().NoError(err)
	s.False(resp.UpdateAvailable)
	s.Equal(UpdateStatusDevelopment, resp.Status)
}

func (s *UpdateFacadeSuite) TestCheckRejectsManifestWithoutCurrentPlatformArtifact() {
	updateFacade := NewUpdateFacade(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(`{
			"product": "paxl",
			"version": "0.1.1",
			"artifacts": [{"platform": "linux/amd64", "sha256": "abc123", "size": 1, "storage_url": "https://example.test/paxl"}]
		}`), nil
	}))

	_, err := updateFacade.Check(context.Background(), &CheckUpdateRequest{
		CurrentVersion: "0.1.0",
		ManifestURL:    "https://example.test/manifest.json",
		Platform:       "darwin/arm64",
	})

	s.Require().Error(err)
	s.Contains(err.Error(), "missing paxl artifact for platform")
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}
