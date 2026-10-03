// Package mcpserver is the MCP front door onto the agent service (A4): the
// same tool catalogue, served over MCP with the official Go SDK. Over stdio
// (orderecho mcp) every call is forwarded to the running service's HTTP
// API; over HTTP (serve --mcp-http) it goes straight to the service's
// Dispatch. Either way the handlers, validation and result shapes are the
// service's own.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/danielgavin-code/OrderEcho/internal/service"
	"github.com/danielgavin-code/OrderEcho/internal/version"
)

// Caller runs one tool call somewhere: in-process or over HTTP.
type Caller interface {
	Call(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, string, *service.APIError)
}

// InProcess calls a Service directly (MCP over HTTP inside the service).
type InProcess struct {
	S      *service.Service
	Client string
}

// Call implements Caller.
func (p InProcess) Call(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, string, *service.APIError) {
	resp := p.S.Dispatch(ctx, p.Client, tool, args)
	if resp.Error != nil {
		return nil, "", resp.Error
	}
	data, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, "", &service.APIError{Code: "internal", Detail: err.Error()}
	}
	return data, resp.Summary, nil
}

// Instructions tell the model how to operate the agent.
const Instructions = `OrderEcho is a FIX 4.2/4.4 trading agent (the buy side) with a certification runner.
You operate it; its code decides what happened and whether it passed.
- Start with list_sessions; connect_session before sending orders. Sessions stay connected between calls.
- Order tools return the agent's shadow state and the verdict of its 11 order checks. Report verdicts as given; never claim a PASS the tools did not return.
- Cert runs are asynchronous: start_cert_run returns a run_id; poll cert_run_status, then read cert_run_results.
- Ask the human before attest_cert_case (and pass user_confirmed only with their explicit answer), and before any order on a session tagged external (confirm_external).
- emulator_* tools act on the counterparty emulator (a test venue), never on a real venue.`

// Annotations for a catalogue tool.
func Annotations(t *service.Tool) *mcp.ToolAnnotations {
	a := &mcp.ToolAnnotations{Title: t.Title, ReadOnlyHint: t.ReadOnly, IdempotentHint: t.Idempotent || t.ReadOnly}
	if !t.ReadOnly {
		d := t.Destructive
		a.DestructiveHint = &d
	}
	ow := t.OpenWorld
	a.OpenWorldHint = &ow
	return a
}

// New builds an MCP server offering tools, each forwarded to caller.
func New(tools []*service.Tool, caller Caller) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "orderecho", Title: "OrderEcho FIX agent", Version: version.Version + " (" + version.Build + ")"},
		&mcp.ServerOptions{Instructions: Instructions})
	for _, t := range tools {
		tool := t
		srv.AddTool(&mcp.Tool{Name: tool.Name, Title: tool.Title, Description: tool.Description, InputSchema: tool.Schema,
			Annotations: Annotations(tool)},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args json.RawMessage
				if req.Params != nil {
					args = req.Params.Arguments
				}
				result, summary, aerr := caller.Call(ctx, tool.Name, args)
				if aerr != nil {
					text := fmt.Sprintf("ERROR %s: %s", aerr.Code, aerr.Detail)
					if aerr.Hint != "" {
						text += "\nHint: " + aerr.Hint
					}
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}},
						StructuredContent: map[string]string{"error": aerr.Code, "detail": aerr.Detail, "hint": aerr.Hint}}, nil
				}
				if len(result) == 0 || string(result) == "null" {
					result = json.RawMessage("{}")
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}, StructuredContent: result}, nil
			})
	}
	return srv
}

// ForService is the MCP server a Service offers under its config.
func ForService(s *service.Service, client string) *mcp.Server {
	return New(s.EnabledTools(), InProcess{S: s, Client: client})
}

// HTTPHandler serves MCP over streamable HTTP, requiring
// "Authorization: Bearer <token>". An empty token is refused by the caller
// before this is built.
func HTTPHandler(s *service.Service, token string) http.Handler {
	srv := ForService(s, service.ClientMCPHTTP)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{})
	verify := func(_ context.Context, got string, _ *http.Request) (*auth.TokenInfo, error) {
		if !constantTimeEqual(got, token) {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{Expiration: time.Now().Add(time.Hour)}, nil
	}
	return auth.RequireBearerToken(verify, nil)(h)
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0 && strings.TrimSpace(b) != ""
}
