package mcpapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var errUnknownTool = errors.New("unknown tool")

type catalogInvocation struct {
	Result *mcp.CallToolResult
	Output any
}

type catalogInvoker func(context.Context, json.RawMessage) (catalogInvocation, error)

type toolCatalog struct {
	invokers map[string]catalogInvoker
}

func newToolCatalog() *toolCatalog {
	return &toolCatalog{invokers: map[string]catalogInvoker{}}
}

func registerTypedTool[Input, Output any](
	catalog *toolCatalog,
	server *mcp.Server,
	tool *mcp.Tool,
	handler func(context.Context, *mcp.CallToolRequest, Input) (*mcp.CallToolResult, Output, error),
) {
	mcp.AddTool(server, tool, handler)
	catalog.invokers[tool.Name] = func(ctx context.Context, raw json.RawMessage) (catalogInvocation, error) {
		var input Input
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return catalogInvocation{}, fmt.Errorf("decode %s input: %w", tool.Name, err)
		}
		result, output, err := handler(ctx, nil, input)
		return catalogInvocation{Result: result, Output: output}, err
	}
}

func (catalog *toolCatalog) invoke(ctx context.Context, name string, input json.RawMessage) (catalogInvocation, error) {
	invoker := catalog.invokers[name]
	if invoker == nil {
		return catalogInvocation{}, fmt.Errorf("%w %q", errUnknownTool, name)
	}
	return invoker(ctx, input)
}

func (catalog *toolCatalog) names() []string {
	names := make([]string, 0, len(catalog.invokers))
	for name := range catalog.invokers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
