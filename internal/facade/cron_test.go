package facade_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pax-oss/paxl/internal/facade"
	"github.com/stretchr/testify/require"
)

func TestCronUpdatePreservesUnspecifiedFieldsAndUsesCurrentRevision(t *testing.T) {
	var patch map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/cron-jobs/job_1", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write(
				[]byte(`{"id":"job_1","revision":7,"prompt":"original","agent":"codex"}`),
			)
			return
		}
		require.Equal(t, http.MethodPatch, r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&patch))
		_, _ = w.Write([]byte(`{"id":"job_1","revision":8,"enabled":false,"agent":"codex"}`))
	}))
	defer server.Close()
	f := facade.NewCronFacade(facade.NewDaemonHTTPClient(server.URL, server.Client()))
	enabled := false
	job, err := f.Update(
		context.Background(),
		"job_1",
		&facade.UpdateCronRequest{Enabled: &enabled},
	)
	require.NoError(t, err)
	require.Equal(t, uint64(8), job.Revision)
	require.Equal(t, map[string]any{"enabled": false, "expected_revision": float64(7)}, patch)
}
