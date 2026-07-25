package facade

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/internal/model/store"
	"github.com/stretchr/testify/require"
)

func TestDeviceConnectExchangesEnrollmentAndStoresCredential(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/v1/agent-enrollments/exchange", req.URL.Path)
		body := decodeJSONBody(t, req)
		require.Equal(t, "tm_enroll_device_once", body["token"])
		require.Equal(t, "todd-macbook-air", body["device_name"])
		return jsonResponse(
			`{"credential_id":"cred-device-1","api_key":"tm_key_device","user_id":"usr-1","permissions":["agent_provision"]}`,
		), nil
	})

	connected, err := NewDeviceFacade(client, opened.Store).Connect(
		ctx,
		&ConnectDeviceRequest{
			Kind: "onprem", URL: "https://memory.internal",
			DeviceName: "todd-macbook-air", EnrollmentToken: "tm_enroll_device_once",
		},
	)

	require.NoError(t, err)
	require.Equal(t, "todd-macbook-air", connected.Device.DeviceName)
	require.Equal(t, "connected", string(connected.Device.Status))
	stored, err := opened.Store.GetDeviceCredential(ctx)
	require.NoError(t, err)
	require.Equal(t, "tm_key_device", stored.Credential.APIKey)
}

func TestDeviceStatusRequiresLocalConnection(t *testing.T) {
	opened, err := store.Open(
		context.Background(),
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()

	_, err = NewDeviceFacade(
		roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("status must not call remote")
		}),
		opened.Store,
	).Status(context.Background(), &DeviceStatusRequest{})

	require.ErrorContains(t, err, "device is not connected")
}

func TestDeviceProvisionMintsAgentCredentialAndTracksAgentOnce(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	_, err = opened.Store.SaveDeviceCredential(ctx, &store.SaveDeviceCredentialRequest{
		Credential: &model.DeviceCredential{
			URL: "https://memory.internal", APIKey: "tm_key_device",
			DeviceName: "todd-macbook-air", CredentialID: "cred-device",
			UserID: "usr-1", Permissions: []string{"agent_provision"},
			Status: model.DeviceStatusConnected,
		},
	})
	require.NoError(t, err)
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/device/agent-provisions":
			require.Equal(t, "Bearer tm_key_device", req.Header.Get("Authorization"))
			body := decodeJSONBody(t, req)
			require.Equal(t, "personal-codex", body["agent_id"])
			require.Equal(t, "Personal Codex", body["display_name"])
			require.Equal(t, "codex", body["agent_type"])
			return jsonResponse(
				`{"api_key":"tm_key_agent","credential":{"credential_id":"cred-agent","agent_id":"personal-codex","user_id":"usr-1","permissions":["channel_send","channel_receive"]}}`,
			), nil
		case "/v1/agent-identity":
			require.Equal(t, "Bearer tm_key_agent", req.Header.Get("Authorization"))
			return jsonResponse(
				`{"credential_id":"cred-agent","agent_id":"personal-codex","user_id":"usr-1","permissions":["channel_send","channel_receive"]}`,
			), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}
	})
	deviceFacade := NewDeviceFacade(client, opened.Store)

	for range 2 {
		provisioned, provisionErr := deviceFacade.Provision(ctx, &ProvisionDeviceAgentRequest{
			AgentID: "personal-codex", DisplayName: "Personal Codex", AgentType: "codex",
			Permissions: []model.AgentPermission{
				model.AgentPermissionChannelSend,
				model.AgentPermissionChannelReceive,
			},
		})
		require.NoError(t, provisionErr)
		require.Equal(t, "tm_key_agent", provisioned.APIKey)
		require.Equal(t, "cred-agent", provisioned.CredentialID)
		require.True(t, provisioned.IdentityVerified)
	}
	status, err := deviceFacade.Status(ctx, &DeviceStatusRequest{})
	require.NoError(t, err)
	require.Equal(t, 1, status.Device.ProvisionedCount)
}

func TestDeviceProvisionExplainsAgentOwnershipConflict(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.Store.Close()) }()
	_, err = opened.Store.SaveDeviceCredential(ctx, &store.SaveDeviceCredentialRequest{
		Credential: &model.DeviceCredential{
			URL: "https://memory.internal", APIKey: "tm_key_device",
			DeviceName: "todd-macbook-air", CredentialID: "cred-device",
			UserID: "usr-1", Status: model.DeviceStatusConnected,
		},
	})
	require.NoError(t, err)
	client := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusConflict,
			Body:       http.NoBody,
			Header:     make(http.Header),
		}, nil
	})

	_, err = NewDeviceFacade(client, opened.Store).Provision(
		ctx,
		&ProvisionDeviceAgentRequest{
			AgentID: "personal-codex", DisplayName: "Personal Codex", AgentType: "codex",
		},
	)

	require.ErrorContains(t, err, "belongs to another device or was registered manually")
	require.NotContains(t, err.Error(), "tm_key_device")
}

func TestDeviceProvisionReturnsOneTimeKeyWhenLocalCountCannotBeSaved(t *testing.T) {
	ctx := context.Background()
	opened, err := store.Open(
		ctx,
		&store.OpenRequest{Path: filepath.Join(t.TempDir(), "paxl.sqlite")},
	)
	require.NoError(t, err)
	_, err = opened.Store.SaveDeviceCredential(ctx, &store.SaveDeviceCredentialRequest{
		Credential: &model.DeviceCredential{
			URL: "https://memory.internal", APIKey: "tm_key_device",
			DeviceName: "todd-macbook-air", CredentialID: "cred-device",
			UserID: "usr-1", Status: model.DeviceStatusConnected,
		},
	})
	require.NoError(t, err)
	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/device/agent-provisions":
			require.NoError(t, opened.Store.Close())
			return jsonResponse(
				`{"api_key":"tm_key_rotated","credential":{"credential_id":"cred-agent","agent_id":"personal-codex","user_id":"usr-1"}}`,
			), nil
		case "/v1/agent-identity":
			return jsonResponse(
				`{"credential_id":"cred-agent","agent_id":"personal-codex","user_id":"usr-1","permissions":["observe"]}`,
			), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}
	})
	var verbose bytes.Buffer

	provisioned, err := NewDeviceFacade(client, opened.Store).Provision(
		ctx,
		&ProvisionDeviceAgentRequest{
			AgentID: "personal-codex", DisplayName: "Personal Codex",
			AgentType: model.AgentNameCodex,
		},
		WithVerboseWriter(&verbose),
	)

	require.NoError(t, err)
	require.Equal(t, "tm_key_rotated", provisioned.APIKey)
	require.Contains(t, verbose.String(), "local device state could not be saved")
	require.NotContains(t, verbose.String(), "tm_key_rotated")
}
