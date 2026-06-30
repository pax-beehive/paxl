package model_test

import (
	"testing"

	"github.com/pax-oss/paxl/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestDaemonControlErrorFormatsCodeAndMessage(t *testing.T) {
	assert.Equal(t, "boom: failed", (&model.DaemonControlError{Code: "boom", Message: "failed"}).Error())
	assert.Equal(t, "failed", (&model.DaemonControlError{Message: "failed"}).Error())
	assert.Equal(t, "boom", (&model.DaemonControlError{Code: "boom"}).Error())
}
