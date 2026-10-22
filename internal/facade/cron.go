package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pax-oss/paxl/internal/model"
)

type CronFacade struct{ client *DaemonLocalAPIClient }

type CreateCronRequest struct {
	ConnectionID   string                `json:"connection_id"`
	SessionMode    model.CronSessionMode `json:"session_mode"`
	Name           string                `json:"name"`
	Agent          model.AgentName       `json:"agent"`
	Prompt         string                `json:"prompt"`
	WorkingDir     string                `json:"working_dir"`
	Schedule       string                `json:"schedule"`
	Timezone       string                `json:"timezone"`
	Enabled        *bool                 `json:"enabled,omitempty"`
	TimeoutSeconds int                   `json:"timeout_seconds,omitempty"`
}

type UpdateCronRequest struct {
	ConnectionID     *string                `json:"connection_id,omitempty"`
	SessionMode      *model.CronSessionMode `json:"session_mode,omitempty"`
	ResetSession     bool                   `json:"reset_session,omitempty"`
	Name             *string                `json:"name,omitempty"`
	Agent            *model.AgentName       `json:"agent,omitempty"`
	Prompt           *string                `json:"prompt,omitempty"`
	WorkingDir       *string                `json:"working_dir,omitempty"`
	Schedule         *string                `json:"schedule,omitempty"`
	Timezone         *string                `json:"timezone,omitempty"`
	Enabled          *bool                  `json:"enabled,omitempty"`
	TimeoutSeconds   *int                   `json:"timeout_seconds,omitempty"`
	ExpectedRevision uint64                 `json:"expected_revision"`
}

type CronStatus struct {
	Running bool `json:"running"`
}

func NewCronFacade(client *DaemonLocalAPIClient) *CronFacade {
	if client == nil {
		client = NewDaemonUnixClient("")
	}
	return &CronFacade{client: client}
}

func (f *CronFacade) Create(
	ctx context.Context,
	req *CreateCronRequest,
	opts ...func(*Option),
) (*model.CronJob, error) {
	if req == nil {
		return nil, fmt.Errorf("create cron job: request is required")
	}
	var job model.CronJob
	err := f.request(ctx, http.MethodPost, "/v1/cron-jobs", req, &job, opts...)
	return &job, err
}

func (f *CronFacade) Get(
	ctx context.Context,
	id string,
	opts ...func(*Option),
) (*model.CronJob, error) {
	path, err := cronJobPath(id)
	if err != nil {
		return nil, err
	}
	var job model.CronJob
	err = f.request(ctx, http.MethodGet, path, nil, &job, opts...)
	return &job, err
}

func (f *CronFacade) List(
	ctx context.Context,
	agent model.AgentName,
	opts ...func(*Option),
) (*[]model.CronJob, error) {
	path := "/v1/cron-jobs"
	if agent != "" && agent != model.AgentNameUnknown {
		path += "?agent=" + url.QueryEscape(string(agent))
	}
	jobs := []model.CronJob{}
	err := f.request(ctx, http.MethodGet, path, nil, &jobs, opts...)
	return &jobs, err
}

func (f *CronFacade) Update(
	ctx context.Context,
	id string,
	req *UpdateCronRequest,
	opts ...func(*Option),
) (*model.CronJob, error) {
	if req == nil {
		return nil, fmt.Errorf("update cron job: request is required")
	}
	patch := *req
	if patch.ExpectedRevision == 0 {
		current, err := f.Get(ctx, id, opts...)
		if err != nil {
			return nil, err
		}
		patch.ExpectedRevision = current.Revision
	}
	path, err := cronJobPath(id)
	if err != nil {
		return nil, err
	}
	var job model.CronJob
	err = f.request(ctx, http.MethodPatch, path, &patch, &job, opts...)
	return &job, err
}

func (f *CronFacade) Delete(ctx context.Context, id string, opts ...func(*Option)) error {
	path, err := cronJobPath(id)
	if err != nil {
		return err
	}
	return f.request(ctx, http.MethodDelete, path, nil, nil, opts...)
}

func (f *CronFacade) Run(
	ctx context.Context,
	id string,
	opts ...func(*Option),
) (*model.CronRun, error) {
	path, err := cronJobPath(id)
	if err != nil {
		return nil, err
	}
	var run model.CronRun
	err = f.request(ctx, http.MethodPost, path+"/run", nil, &run, opts...)
	return &run, err
}

func (f *CronFacade) Runs(
	ctx context.Context,
	id string,
	limit int,
	opts ...func(*Option),
) (*[]model.CronRun, error) {
	runs := []model.CronRun{}
	path := "/v1/cron-runs?job_id=" + url.QueryEscape(id) + "&limit=" + strconv.Itoa(limit)
	err := f.request(ctx, http.MethodGet, path, nil, &runs, opts...)
	return &runs, err
}

func (f *CronFacade) Status(ctx context.Context, opts ...func(*Option)) (*CronStatus, error) {
	var status CronStatus
	err := f.request(ctx, http.MethodGet, "/v1/cron-status", nil, &status, opts...)
	return &status, err
}

func cronJobPath(id string) (string, error) {
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, "/?#") || id == "." || id == ".." {
		return "", fmt.Errorf("cron job id is invalid")
	}
	return "/v1/cron-jobs/" + url.PathEscape(id), nil
}

func (f *CronFacade) request(
	ctx context.Context,
	method, path string,
	body, result any,
	opts ...func(*Option),
) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode cron request: %w", err)
		}
	}
	verbosef(applyOptions(opts), "Requesting cron API: %s %s.", method, path)
	req, err := http.NewRequestWithContext(
		ctx,
		method,
		f.client.baseURL+path,
		bytes.NewReader(data),
	)
	if err != nil {
		return fmt.Errorf("create cron request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.client.http.Do(req)
	if err != nil {
		return fmt.Errorf("contact paxd cron API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf(
			"cron API returned %s: %s",
			resp.Status,
			strings.TrimSpace(string(detail)),
		)
	}
	if result == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024*1024)).Decode(result); err != nil {
		return fmt.Errorf("decode cron response: %w", err)
	}
	return nil
}
