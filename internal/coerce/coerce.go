// Package coerce provides MCP middleware that coerces tool argument types
// to match the tool's JSON schema. Some MCP clients send integer values as
// strings (e.g., "32" instead of 32), which fails strict schema validation.
// This middleware fixes that by converting string values to their expected
// types before validation occurs.
package coerce

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Registry maps tool names to their property type expectations.
// Each entry maps a JSON property name to its expected JSON Schema type
// (e.g., "integer", "number", "boolean").
type Registry struct {
	tools map[string]map[string]string
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]map[string]string)}
}

// AddTool wraps mcp.AddTool and also records the input struct's field types
// in the registry. Use this instead of mcp.AddTool to enable automatic type
// coercion for the tool's arguments.
func AddTool[T any](server *mcp.Server, registry *Registry, tool *mcp.Tool, handler func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, any, error)) {
	registry.registerType(tool.Name, reflect.TypeOf((*T)(nil)).Elem())
	mcp.AddTool(server, tool, handler)
}

// registerType extracts JSON field names and their Go kinds from a struct type,
// mapping them to JSON Schema type strings for coercion.
func (r *Registry) registerType(toolName string, t reflect.Type) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}

	types := make(map[string]string)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		jsonName := strings.Split(jsonTag, ",")[0]

		kind := field.Type.Kind()
		if kind == reflect.Ptr {
			kind = field.Type.Elem().Kind()
		}

		switch kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			types[jsonName] = "integer"
		case reflect.Float32, reflect.Float64:
			types[jsonName] = "number"
		case reflect.Bool:
			types[jsonName] = "boolean"
		}
	}
	if len(types) > 0 {
		r.tools[toolName] = types
	}
}

// Middleware returns MCP receiving middleware that coerces tool call arguments
// to match expected schema types. It only modifies tools/call requests and only
// coerces values where the schema expects a numeric or boolean type but the
// client sent a JSON string.
func (r *Registry) Middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}

			params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
			if !ok || params == nil || len(params.Arguments) == 0 {
				return next(ctx, method, req)
			}

			types, ok := r.tools[params.Name]
			if !ok {
				return next(ctx, method, req)
			}

			var args map[string]json.RawMessage
			if err := json.Unmarshal(params.Arguments, &args); err != nil {
				return next(ctx, method, req)
			}

			changed := false
			for name, raw := range args {
				expectedType, ok := types[name]
				if !ok {
					continue
				}

				// Only coerce if the value is currently a JSON string (starts with '"')
				if len(raw) < 2 || raw[0] != '"' {
					continue
				}

				var s string
				if err := json.Unmarshal(raw, &s); err != nil {
					continue
				}

				switch expectedType {
				case "integer":
					if _, err := strconv.ParseInt(s, 10, 64); err == nil {
						args[name] = json.RawMessage(s)
						changed = true
					}
				case "number":
					if _, err := strconv.ParseFloat(s, 64); err == nil {
						args[name] = json.RawMessage(s)
						changed = true
					}
				case "boolean":
					if s == "true" || s == "false" {
						args[name] = json.RawMessage(s)
						changed = true
					}
				}
			}

			if changed {
				if b, err := json.Marshal(args); err == nil {
					params.Arguments = b
				}
			}

			return next(ctx, method, req)
		}
	}
}
