package model

import (
	"fmt"
	"strings"
)

type AgentPermission string

const (
	AgentPermissionUnknown        AgentPermission = ""
	AgentPermissionObserve        AgentPermission = "observe"
	AgentPermissionSearch         AgentPermission = "search"
	AgentPermissionGet            AgentPermission = "get"
	AgentPermissionChannelSend    AgentPermission = "channel_send"
	AgentPermissionChannelReceive AgentPermission = "channel_receive"
)

var supportedAgentPermissions = map[AgentPermission]struct{}{
	AgentPermissionObserve:        {},
	AgentPermissionSearch:         {},
	AgentPermissionGet:            {},
	AgentPermissionChannelSend:    {},
	AgentPermissionChannelReceive: {},
}

func ParseAgentPermission(raw string) (AgentPermission, error) {
	permission := AgentPermission(strings.TrimSpace(strings.ToLower(raw)))
	if _, ok := supportedAgentPermissions[permission]; !ok {
		return AgentPermissionUnknown, fmt.Errorf(
			"parse agent permission %q: unsupported permission",
			raw,
		)
	}
	return permission, nil
}

func AgentPermissionStrings(permissions []AgentPermission) []string {
	values := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		values = append(values, string(permission))
	}
	return values
}

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
