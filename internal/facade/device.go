package facade

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
)

type DeviceFacade struct {
	client AuthHTTPClient
	store  *store.Store
}

type ConnectDeviceRequest struct {
	Kind             model.ChannelKind
	URL              string
	DeviceName       string
	EnrollmentToken  string
	CAFile           string
	AllowTailnetHTTP bool
}

type ConnectDeviceResponse struct {
	Device *model.DeviceCredential `json:"device"`
}

type DeviceStatusRequest struct{}

type DeviceStatusResponse struct {
	Device *model.DeviceCredential `json:"device"`
}

type ProvisionDeviceAgentRequest struct {
	AgentID     string
	DisplayName string
	AgentType   model.AgentName
	Permissions []string
}

type ProvisionDeviceAgentResponse struct {
	URL              string   `json:"url"`
	APIKey           string   `json:"api_key"`
	AgentID          string   `json:"agent_id"`
	UserID           string   `json:"user_id"`
	CredentialID     string   `json:"credential_id"`
	Permissions      []string `json:"permissions,omitempty"`
	IdentityVerified bool     `json:"-"`
}

type deviceEnrollmentResponse struct {
	CredentialID string   `json:"credential_id"`
	APIKey       string   `json:"api_key"`
	UserID       string   `json:"user_id"`
	Permissions  []string `json:"permissions"`
}

type provisionedAgentCredential struct {
	CredentialID string   `json:"credential_id"`
	AgentID      string   `json:"agent_id"`
	UserID       string   `json:"user_id"`
	Permissions  []string `json:"permissions"`
}

type provisionDeviceAgentAPIResponse struct {
	APIKey       string                      `json:"api_key"`
	CredentialID string                      `json:"credential_id"`
	AgentID      string                      `json:"agent_id"`
	UserID       string                      `json:"user_id"`
	Permissions  []string                    `json:"permissions"`
	Credential   *provisionedAgentCredential `json:"credential"`
}

func NewDeviceFacade(client AuthHTTPClient, sessionStore *store.Store) *DeviceFacade {
	if client == nil {
		client = http.DefaultClient
	}
	return &DeviceFacade{client: client, store: sessionStore}
}

func (f *DeviceFacade) Connect(
	ctx context.Context,
	req *ConnectDeviceRequest,
	opts ...func(*Option),
) (*ConnectDeviceResponse, error) {
	option := applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("connect device: request is required")
	}
	if req.Kind != model.ChannelKindOnPrem {
		return nil, fmt.Errorf("connect device: unsupported kind %q", req.Kind)
	}
	deviceName := strings.TrimSpace(req.DeviceName)
	if deviceName == "" {
		return nil, fmt.Errorf("connect device: device name is required")
	}
	if strings.TrimSpace(req.EnrollmentToken) == "" {
		return nil, fmt.Errorf("connect device: enrollment token is required")
	}
	if f.store == nil {
		return nil, fmt.Errorf("connect device: store is required")
	}
	origin, _, err := resolveChannelOrigin(
		req.URL,
		req.EnrollmentToken,
		req.AllowTailnetHTTP,
	)
	if err != nil {
		return nil, fmt.Errorf("connect device: %w", err)
	}
	client, err := channelHTTPClient(f.client, req.CAFile)
	if err != nil {
		return nil, fmt.Errorf("connect device: %w", err)
	}
	verbosef(option, "Exchanging device enrollment for %q.", deviceName)
	var exchanged deviceEnrollmentResponse
	if err := doOnPremJSON(
		ctx,
		client,
		http.MethodPost,
		origin,
		"/v1/agent-enrollments/exchange",
		"",
		map[string]string{
			"token":       req.EnrollmentToken,
			"device_name": deviceName,
		},
		&exchanged,
		"exchange device enrollment",
		"",
	); err != nil {
		return nil, fmt.Errorf("connect device: %w", err)
	}
	if exchanged.APIKey == "" || exchanged.CredentialID == "" || exchanged.UserID == "" {
		return nil, fmt.Errorf(
			"connect device: enrollment exchange returned incomplete device credential",
		)
	}
	if len(exchanged.Permissions) != 1 ||
		exchanged.Permissions[0] != "agent_provision" {
		return nil, fmt.Errorf(
			"connect device: enrollment exchange did not return an agent_provision-only credential",
		)
	}
	device := &model.DeviceCredential{
		URL:          origin,
		APIKey:       exchanged.APIKey,
		CAFile:       strings.TrimSpace(req.CAFile),
		DeviceName:   deviceName,
		CredentialID: exchanged.CredentialID,
		UserID:       exchanged.UserID,
		Permissions:  append([]string(nil), exchanged.Permissions...),
		Status:       model.DeviceStatusConnected,
	}
	if _, err := f.store.SaveDeviceCredential(
		ctx,
		&store.SaveDeviceCredentialRequest{Credential: device},
	); err != nil {
		return nil, fmt.Errorf(
			"connect device: enrollment was consumed but save credential failed: %w",
			err,
		)
	}
	return &ConnectDeviceResponse{Device: device}, nil
}

func (f *DeviceFacade) Status(
	ctx context.Context,
	req *DeviceStatusRequest,
	opts ...func(*Option),
) (*DeviceStatusResponse, error) {
	_ = req
	option := applyOptions(opts)
	if f.store == nil {
		return nil, fmt.Errorf("device status: store is required")
	}
	verbosef(option, "Loading local device status.")
	got, err := f.store.GetDeviceCredential(ctx)
	if err != nil {
		return nil, fmt.Errorf("device status: %w", err)
	}
	if got.Credential == nil {
		return nil, fmt.Errorf("device status: device is not connected")
	}
	return &DeviceStatusResponse{Device: got.Credential}, nil
}

func (f *DeviceFacade) Provision(
	ctx context.Context,
	req *ProvisionDeviceAgentRequest,
	opts ...func(*Option),
) (*ProvisionDeviceAgentResponse, error) {
	option := applyOptions(opts)
	if req == nil {
		return nil, fmt.Errorf("provision device agent: request is required")
	}
	agentID := strings.TrimSpace(req.AgentID)
	displayName := strings.TrimSpace(req.DisplayName)
	if agentID == "" || displayName == "" ||
		req.AgentType == model.AgentNameUnknown ||
		strings.TrimSpace(string(req.AgentType)) == "" {
		return nil, fmt.Errorf(
			"provision device agent: agent id, display name, and agent type are required",
		)
	}
	if f.store == nil {
		return nil, fmt.Errorf("provision device agent: store is required")
	}
	got, err := f.store.GetDeviceCredential(ctx)
	if err != nil {
		return nil, fmt.Errorf("provision device agent: load device credential: %w", err)
	}
	if got.Credential == nil {
		return nil, fmt.Errorf(
			"provision device agent: device is not connected; run paxl device connect onprem first",
		)
	}
	verbosef(option, "Provisioning Agent %q from the local device.", agentID)
	client, err := channelHTTPClient(f.client, got.Credential.CAFile)
	if err != nil {
		return nil, fmt.Errorf("provision device agent: %w", err)
	}
	payload := struct {
		AgentID     string          `json:"agent_id"`
		DisplayName string          `json:"display_name"`
		AgentType   model.AgentName `json:"agent_type"`
		Permissions []string        `json:"permissions,omitempty"`
	}{
		AgentID: agentID, DisplayName: displayName, AgentType: req.AgentType,
		Permissions: append([]string(nil), req.Permissions...),
	}
	var provisioned provisionDeviceAgentAPIResponse
	err = doOnPremJSON(
		ctx,
		client,
		http.MethodPost,
		got.Credential.URL,
		"/v1/device/agent-provisions",
		got.Credential.APIKey,
		&payload,
		&provisioned,
		"provision device agent",
		"agent_provision",
	)
	if err != nil {
		var statusErr *onPremStatusCodeError
		if errors.As(err, &statusErr) && statusErr.status == http.StatusConflict {
			return nil, fmt.Errorf(
				"provision device agent: agent %q belongs to another device or was registered manually",
				agentID,
			)
		}
		return nil, fmt.Errorf("provision device agent: %w", err)
	}
	response, err := normalizeProvisionedAgentResponse(req, got.Credential, &provisioned)
	if err != nil {
		return nil, fmt.Errorf("provision device agent: normalize response: %w", err)
	}
	if err := verifyProvisionedAgentIdentity(
		ctx,
		client,
		agentID,
		response,
		option,
	); err != nil {
		return nil, fmt.Errorf("provision device agent: verify identity: %w", err)
	}
	got.Credential.ProvisionedAgents = append(
		got.Credential.ProvisionedAgents,
		response.AgentID,
	)
	if _, err := f.store.SaveDeviceCredential(
		ctx,
		&store.SaveDeviceCredentialRequest{Credential: got.Credential},
	); err != nil {
		// Delivery of the one-time key takes precedence over the advisory local
		// count because a repeated provision rotates and revokes the old key.
		verbosef(
			option,
			"Agent credential was minted, but local device state could not be saved: %v.",
			err,
		)
	}
	return response, nil
}

func normalizeProvisionedAgentResponse(
	req *ProvisionDeviceAgentRequest,
	device *model.DeviceCredential,
	provisioned *provisionDeviceAgentAPIResponse,
) (*ProvisionDeviceAgentResponse, error) {
	credentialID := provisioned.CredentialID
	responseAgentID := provisioned.AgentID
	userID := provisioned.UserID
	permissions := append([]string(nil), provisioned.Permissions...)
	// Team Memory revisions may return credential metadata either at the top
	// level or in a nested credential object. Normalize both at this boundary.
	if provisioned.Credential != nil {
		credentialID = firstNonEmpty(credentialID, provisioned.Credential.CredentialID)
		responseAgentID = firstNonEmpty(responseAgentID, provisioned.Credential.AgentID)
		userID = firstNonEmpty(userID, provisioned.Credential.UserID)
		if len(permissions) == 0 {
			permissions = append([]string(nil), provisioned.Credential.Permissions...)
		}
	}
	responseAgentID = firstNonEmpty(responseAgentID, strings.TrimSpace(req.AgentID))
	userID = firstNonEmpty(userID, device.UserID)
	if len(permissions) == 0 {
		permissions = append([]string(nil), req.Permissions...)
	}
	if strings.TrimSpace(provisioned.APIKey) == "" || strings.TrimSpace(credentialID) == "" {
		return nil, fmt.Errorf(
			"provision device agent: server returned incomplete agent credential",
		)
	}
	return &ProvisionDeviceAgentResponse{
		URL: device.URL, APIKey: provisioned.APIKey, AgentID: responseAgentID,
		UserID: userID, CredentialID: credentialID, Permissions: permissions,
	}, nil
}

func verifyProvisionedAgentIdentity(
	ctx context.Context,
	client AuthHTTPClient,
	agentID string,
	response *ProvisionDeviceAgentResponse,
	option *Option,
) error {
	identity, err := fetchChannelIdentity(
		ctx,
		client,
		&model.ChannelProfile{URL: response.URL, APIKey: response.APIKey},
	)
	if err != nil {
		// A transport failure must not discard a one-time key that may already
		// have rotated the prior credential. Channel connect retries verification.
		verbosef(
			option,
			"Agent credential was minted, but identity could not be verified: %v.",
			err,
		)
		return nil
	}
	if identity.AgentID != agentID {
		return fmt.Errorf(
			"provision device agent: provisioned identity %q does not match requested Agent %q",
			identity.AgentID,
			agentID,
		)
	}
	response.AgentID = identity.AgentID
	response.UserID = identity.UserID
	response.CredentialID = identity.CredentialID
	response.Permissions = append([]string(nil), identity.Permissions...)
	response.IdentityVerified = true
	return nil
}
