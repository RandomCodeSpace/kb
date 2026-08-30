package ai

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	plasmidopenai "github.com/RandomCodeSpace/plasmid/openai"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolutils"
	"google.golang.org/genai"
)

const modelToolErrorPrefix = "error: "

type toolResultKind uint8

const (
	toolResultObject toolResultKind = iota
	toolResultText
)

// kbTool is a native ADK function tool. The declaration is the exact schema
// kb advertises, while run keeps the domain validation and side effects in kb.
// Text-only tools mark their result for Chat's scalar response encoding.
type kbTool struct {
	name        string
	description string
	inputSchema map[string]any
	resultKind  toolResultKind
	run         func(context.Context, json.RawMessage) (string, error)
}

func newKBTool(
	name, description string,
	inputSchema map[string]any,
	resultKind toolResultKind,
	run func(context.Context, json.RawMessage) (string, error),
) *kbTool {
	return &kbTool{
		name: name, description: description, inputSchema: inputSchema,
		resultKind: resultKind, run: run,
	}
}

func (t *kbTool) Name() string        { return t.name }
func (t *kbTool) Description() string { return t.description }
func (*kbTool) IsLongRunning() bool   { return false }

func (t *kbTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name: t.name, Description: t.description, ParametersJsonSchema: t.inputSchema,
	}
}

func (t *kbTool) ProcessRequest(_ agent.Context, request *model.LLMRequest) error {
	return toolutils.PackTool(request, t)
}

// Run converts ADK's object arguments into the raw JSON the kb-owned domain
// handlers validate. Handler failures are successful function responses with
// an error field, so the model gets a chance to correct one bad call.
func (t *kbTool) Run(ctx agent.Context, args any) (map[string]any, error) {
	input, err := adkToolInput(args)
	if err != nil {
		return modelToolError(err), nil
	}
	output, err := t.run(ctx, input)
	if err != nil {
		return modelToolError(err), nil
	}
	if t.resultKind == toolResultText {
		return plasmidopenai.RawChatToolResult(output)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		log.Printf("tools: decode native result: %v", err)
		return modelToolError(errors.New("could not encode the result")), nil
	}
	if result == nil {
		log.Printf("tools: decode native result: expected an object")
		return modelToolError(errors.New("could not encode the result")), nil
	}
	return result, nil
}

func adkToolInput(args any) (json.RawMessage, error) {
	if args == nil {
		args = map[string]any{}
	}
	if _, ok := args.(map[string]any); !ok {
		return nil, errors.New("invalid input: expected a JSON object matching the tool schema")
	}
	input, err := json.Marshal(args)
	if err != nil {
		return nil, errors.New("invalid input: expected a JSON object matching the tool schema")
	}
	return input, nil
}

func modelToolError(err error) map[string]any {
	return map[string]any{"error": modelToolErrorPrefix + err.Error()}
}

var (
	_ tool.Tool = (*kbTool)(nil)
	_ interface {
		Declaration() *genai.FunctionDeclaration
		ProcessRequest(agent.Context, *model.LLMRequest) error
		Run(agent.Context, any) (map[string]any, error)
	} = (*kbTool)(nil)
)
