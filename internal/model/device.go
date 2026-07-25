package model

import (
	"fmt"
	"strings"
)

type DeviceStatus string

const (
	DeviceStatusUnknown   DeviceStatus = ""
	DeviceStatusConnected DeviceStatus = "connected"
)

func ParseDeviceStatus(raw string) (DeviceStatus, error) {
	status := DeviceStatus(strings.TrimSpace(strings.ToLower(raw)))
	if status != DeviceStatusConnected {
		return DeviceStatusUnknown, fmt.Errorf("parse device status %q: unsupported status", raw)
	}
	return status, nil
}

type DeviceCredential struct {
	URL               string       `json:"url"`
	APIKey            string       `json:"-"`
	CAFile            string       `json:"ca_file,omitempty"`
	DeviceName        string       `json:"device_name"`
	CredentialID      string       `json:"credential_id"`
	UserID            string       `json:"user_id"`
	Permissions       []string     `json:"permissions,omitempty"`
	ProvisionedAgents []string     `json:"-"`
	ProvisionedCount  int          `json:"provisioned_count"`
	Status            DeviceStatus `json:"status"`
	CreatedAt         string       `json:"created_at,omitempty"`
	UpdatedAt         string       `json:"updated_at,omitempty"`
}
