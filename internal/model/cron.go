package model

import (
	"fmt"
	"time"
)

type CronSessionMode string

const (
	CronSessionUnknown CronSessionMode = ""
	CronSessionNew     CronSessionMode = "new"
	CronSessionReuse   CronSessionMode = "reuse"
)

func ParseCronSessionMode(raw string) (CronSessionMode, error) {
	switch CronSessionMode(raw) {
	case CronSessionNew, CronSessionReuse:
		return CronSessionMode(raw), nil
	case CronSessionUnknown:
	}
	return CronSessionUnknown, fmt.Errorf("session mode must be new or reuse")
}

type CronJob struct {
	AgentID        string          `json:"agent_id"`
	RemoteID       string          `json:"remote_id"`
	CloudAPIURL    string          `json:"cloud_api_url"`
	ConnectionID   string          `json:"connection_id"`
	SessionMode    CronSessionMode `json:"session_mode"`
	BoundSessionID string          `json:"bound_session_id,omitempty"`
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Agent          AgentName       `json:"agent"`
	Prompt         string          `json:"prompt"`
	WorkingDir     string          `json:"working_dir"`
	Schedule       string          `json:"schedule"`
	Timezone       string          `json:"timezone"`
	Enabled        bool            `json:"enabled"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	NextRunAt      *time.Time      `json:"next_run_at"`
	ActiveRunID    string          `json:"active_run_id,omitempty"`
	Revision       uint64          `json:"revision"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// Run payloads are rendered verbatim; status remains forward-compatible with paxd.
type CronRun struct {
	SessionID       string          `json:"session_id,omitempty"`
	LocalSessionID  string          `json:"local_session_id,omitempty"`
	NativeSessionID string          `json:"native_session_id,omitempty"`
	ConnectionID    string          `json:"connection_id"`
	SessionMode     CronSessionMode `json:"session_mode"`
	Prompt          string          `json:"prompt"`
	WorkingDir      string          `json:"working_dir"`
	ID              string          `json:"id"`
	JobID           string          `json:"job_id"`
	Trigger         string          `json:"trigger"`
	Status          string          `json:"status"`
	ScheduledAt     time.Time       `json:"scheduled_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	Output          string          `json:"output,omitempty"`
	Error           string          `json:"error,omitempty"`
}
