package plugin

import (
	"encoding/json"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/errs"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

type rpcEnvelope struct {
	OK     bool             `json:"ok"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *pluginabi.Error `json:"error,omitempty"`
}

func dispatchRPC(method string, request []byte) []byte {
	var result any
	var err error

	switch method {
	case pluginabi.MethodPluginRegister:
		result, err = runtimeState.register(request)
	case pluginabi.MethodPluginReconfigure:
		result, err = runtimeState.reconfigure(request)
	case pluginabi.MethodUsageHandle:
		result, err = runtimeState.handleUsage(request)
	case pluginabi.MethodManagementRegister:
		result, err = runtimeState.registerManagement(request)
	case pluginabi.MethodManagementHandle:
		result, err = runtimeState.handleManagement(request)
	case pluginabi.MethodPluginShutdown:
		err = runtimeState.shutdown()
		result = map[string]any{}
	default:
		return marshalError("unknown_method", "unknown method: "+method, false, 404)
	}

	if err != nil {
		return marshalError("plugin_error", err.Error(), false, errs.HTTPStatus(err))
	}
	return marshalOK(result)
}

func marshalOK(value any) []byte {
	result, err := json.Marshal(value)
	if err != nil {
		return marshalError("marshal_error", err.Error(), false, 500)
	}
	raw, err := json.Marshal(rpcEnvelope{OK: true, Result: result})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"marshal_error","message":"failed to encode response"}}`)
	}
	return raw
}

func marshalError(code, message string, retryable bool, status int) []byte {
	raw, err := json.Marshal(rpcEnvelope{
		OK: false,
		Error: &pluginabi.Error{
			Code:       code,
			Message:    message,
			Retryable:  retryable,
			HTTPStatus: status,
		},
	})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"marshal_error","message":"failed to encode error"}}`)
	}
	return raw
}

// Public facade for the small cgo entry point in the repository root.
var RuntimeState = runtimeState

type AuthRuntimeMetadata = authRuntimeMetadata
type RPCEnvelope = rpcEnvelope

func DispatchRPC(method string, request []byte) []byte {
	return dispatchRPC(method, request)
}

func MarshalError(code, message string, retryable bool, status int) []byte {
	return marshalError(code, message, retryable, status)
}

func (r *pluginRuntime) SetAuthRuntimeLookup(lookup authRuntimeLookup) {
	r.setAuthRuntimeLookup(lookup)
}

func (r *pluginRuntime) Shutdown() error {
	return r.shutdown()
}
