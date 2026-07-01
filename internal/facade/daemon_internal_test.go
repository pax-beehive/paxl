package facade

import (
	"errors"
	"io"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAckOKReportsNilAndFailedStatuses(t *testing.T) {
	err := ackOK(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no command ack")

	err = ackOK(&model.DaemonCommandAck{Status: model.DaemonCommandStatusFailed})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed")

	err = ackOK(&model.DaemonCommandAck{
		Status: model.DaemonCommandStatusFailed,
		Error:  &model.DaemonControlError{Code: "boom", Message: "failed"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestDaemonAPIGuidanceAllowsNilAndWrapsErrors(t *testing.T) {
	assert.NoError(t, daemonAPIGuidance(nil))

	err := daemonAPIGuidance(errors.New("dial unix missing"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is paxd running")
}

func TestJSONReaderHandlesNilBodyAndMarshalErrors(t *testing.T) {
	reader, err := jsonReader(nil)
	require.NoError(t, err)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Empty(t, raw)

	_, err = jsonReader(make(chan string))
	require.Error(t, err)
}
