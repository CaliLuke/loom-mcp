package mcp

import (
	"encoding/json/v2"
	"errors"
	"testing"

	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/require"
)

func TestNewErrorDataOmitsServiceErrorID(t *testing.T) {
	err := loom.NewServiceError(errors.New("internal detail"), "resource_not_found", true, true, true)

	encoded, marshalErr := json.Marshal(NewErrorData(err))
	require.NoError(t, marshalErr)
	require.JSONEq(t, `{"name":"resource_not_found","temporary":true,"timeout":true,"fault":true}`, string(encoded))
	require.NotContains(t, string(encoded), err.ID)
}

func TestNewErrorDataReturnsNilWithoutClientMetadata(t *testing.T) {
	require.Nil(t, NewErrorData(nil))
	require.Nil(t, NewErrorData(errors.New("plain failure")))
}
