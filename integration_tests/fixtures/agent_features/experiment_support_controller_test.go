package agentfeatures_test

import (
	"context"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type supportCommand struct {
	Operation control.Operation `json:"operation"`
	Request   jsontext.Value    `json:"request"`
}

type supportReply struct {
	Data    jsontext.Value `json:"data,omitempty"`
	Failure *control.Error `json:"failure,omitempty"`
}

type supportControllerModel struct{}

// Complete turns a scripted controller command into a real native tool call, then retains its actual result.
func (supportControllerModel) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	var turn agenttest.Turn
	part, err := latestExperimentResult(request)
	if err == nil {
		reply := supportReply{}
		if part.IsError {
			text, ok := part.Content.(string)
			if !ok {
				return nil, errors.New("controller error is not safe text")
			}
			if err := json.Unmarshal([]byte(text), &reply.Failure); err != nil {
				return nil, fmt.Errorf("decode native failure %q: %w", text, err)
			}
		} else {
			data, err := json.Marshal(part.Content)
			if err != nil {
				return nil, err
			}
			reply.Data = data
		}
		data, err := json.Marshal(reply)
		if err != nil {
			return nil, err
		}
		turn = agenttest.Respond(agenttest.Text(string(data)))
	} else {
		var command supportCommand
		last := request.Messages[len(request.Messages)-1]
		if err := json.Unmarshal([]byte(experimentMessageText(last)), &command); err != nil {
			return nil, fmt.Errorf("decode controller command %q: %w", experimentMessageText(last), err)
		}
		turn = agenttest.Respond(agenttest.RawToolCall(tools.Ident("experiment."+string(command.Operation)), rawjson.Message(command.Request)), agenttest.CallID("support-native"))
	}
	script, err := agenttest.NewScript(turn)
	if err != nil {
		return nil, err
	}
	return script.Client().Complete(ctx, request)
}

// Stream is unused because the native controller uses complete requests.
func (supportControllerModel) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, errors.New("support controller uses complete requests")
}

// supportCall uses only a real controller run and its returned native DTO.
func supportCall[T any](t *testing.T, env *supportEnvironment, operation control.Operation, input any) (T, error) {
	t.Helper()
	var output T
	request, err := json.Marshal(input, jsonv1.OmitEmptyWithLegacySemantics(true))
	if err != nil {
		return output, err
	}
	command, err := json.Marshal(supportCommand{Operation: operation, Request: request})
	if err != nil {
		return output, err
	}
	result, err := env.controller.Run(t.Context(), "support.controller", agenttest.User(string(command)))
	if err != nil {
		return output, err
	}
	if result.RunErr != nil {
		return output, fmt.Errorf("controller %s run: %w", operation, result.RunErr)
	}
	var reply supportReply
	if err := json.Unmarshal([]byte(experimentMessageText(result.Output.Final)), &reply); err != nil {
		return output, fmt.Errorf("decode controller final %q: %w", experimentMessageText(result.Output.Final), err)
	}
	if reply.Failure != nil {
		return output, reply.Failure
	}
	err = json.Unmarshal(reply.Data, &output)
	if err != nil {
		return output, fmt.Errorf("decode %s DTO %q: %w", operation, reply.Data, err)
	}
	return output, nil
}

func supportMust[T any](t *testing.T, env *supportEnvironment, operation control.Operation, input any) T {
	t.Helper()
	output, err := supportCall[T](t, env, operation, input)
	require.NoError(t, err)
	return output
}
