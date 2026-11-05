package facade

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/pax-oss/paxl/pkg/adaptor"
	"github.com/stretchr/testify/require"
)

func TestAgentConfigurationFacadePassesReadOptionsWithoutPersistence(t *testing.T) {
	var verbose bytes.Buffer
	expected := &model.AgentConfiguration{SchemaVersion: 1}
	f := NewAgentConfigurationFacade()
	f.read = func(ctx context.Context, req *adaptor.ReadConfigurationRequest, opts ...func(*adaptor.Option)) (*model.AgentConfiguration, error) {
		require.Equal(t, model.AgentNameCodex, req.Agent)
		require.Equal(t, "/fixture", req.CWD)
		require.Equal(t, model.AgentConfigurationKindSkills, req.Kind)
		require.True(t, req.IncludeContent)
		require.True(t, req.IncludeSensitive)
		option := &adaptor.Option{}
		for _, apply := range opts {
			apply(option)
		}
		require.Same(t, &verbose, option.VerboseWriter)
		return expected, ctx.Err()
	}
	actual, err := f.Read(context.Background(), &ReadAgentConfigurationRequest{
		Agent: model.AgentNameCodex, CWD: "/fixture", Kind: model.AgentConfigurationKindSkills,
		IncludeContent: true, IncludeSensitive: true,
	}, WithVerboseWriter(&verbose))
	require.NoError(t, err)
	require.Same(t, expected, actual)
}

func TestAgentConfigurationFacadeValidatesBeforeReading(t *testing.T) {
	f := NewAgentConfigurationFacade()
	f.read = func(context.Context, *adaptor.ReadConfigurationRequest, ...func(*adaptor.Option)) (*model.AgentConfiguration, error) {
		t.Fatal("invalid request reached the configuration reader")
		return nil, errors.New("unexpected reader invocation")
	}
	for _, req := range []*ReadAgentConfigurationRequest{
		nil,
		{Agent: model.AgentNameUnknown, Kind: model.AgentConfigurationKindAll},
		{Agent: model.AgentNameCodex, Kind: model.AgentConfigurationKindUnknown},
	} {
		_, err := f.Read(context.Background(), req)
		require.Error(t, err)
	}
}

func TestAgentConfigurationFacadePreservesReaderErrors(t *testing.T) {
	expected := errors.New("fixture read failed")
	f := NewAgentConfigurationFacade()
	f.read = func(context.Context, *adaptor.ReadConfigurationRequest, ...func(*adaptor.Option)) (*model.AgentConfiguration, error) {
		return nil, expected
	}
	_, err := f.Read(
		context.Background(),
		&ReadAgentConfigurationRequest{
			Agent: model.AgentNamePi,
			Kind:  model.AgentConfigurationKindMCP,
		},
	)
	require.ErrorIs(t, err, expected)
	require.JSONEq(t, string(model.AgentConfigurationSchema()), string(AgentConfigurationSchema()))
}
