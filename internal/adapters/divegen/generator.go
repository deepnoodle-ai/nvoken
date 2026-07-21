// Package divegen adapts nvoken's durable, provider-neutral generation request
// to one explicit Dive provider call.
package divegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/deepnoodle-ai/dive"
	"github.com/deepnoodle-ai/dive/llm"
	"github.com/deepnoodle-ai/dive/providers/anthropic"
	"github.com/deepnoodle-ai/dive/providers/openai"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type Config struct {
	AnthropicAPIKey string
	OpenAIAPIKey    string
}

type modelFactory func(provider, model, apiKey string) (llm.LLM, error)

type Generator struct {
	config          Config
	factory         modelFactory
	toolCoordinator ports.ToolCallCoordinator
	testBuiltin     bool
	clock           ports.Clock
}

type Option func(*Generator)

// WithDeterministicTestBuiltin enables the side-effect-free builtin used to
// prove durable ToolCall transitions. Production daemon wiring never applies
// this option.
func WithDeterministicTestBuiltin(coordinator ports.ToolCallCoordinator) Option {
	return func(generator *Generator) {
		generator.toolCoordinator = coordinator
		generator.testBuiltin = coordinator != nil
	}
}

// WithClock makes checkpoint budget boundaries deterministic in tests. The
// production adapter uses wall-clock UTC time.
func WithClock(clock ports.Clock) Option {
	return func(generator *Generator) {
		if clock != nil {
			generator.clock = clock
		}
	}
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func New(config Config, options ...Option) *Generator {
	generator := &Generator{
		config:  config,
		factory: newModel,
		clock:   systemClock{},
	}
	for _, option := range options {
		if option != nil {
			option(generator)
		}
	}
	return generator
}

func newModel(provider, model, apiKey string) (llm.LLM, error) {
	switch provider {
	case "anthropic":
		return anthropic.New(anthropic.WithModel(model), anthropic.WithAPIKey(apiKey)), nil
	case "openai":
		return openai.New(openai.WithModel(model), openai.WithAPIKey(apiKey)), nil
	default:
		return nil, ports.ErrProviderUnsupported
	}
}

func (g *Generator) Generate(ctx context.Context, request domain.GenerationRequest) (domain.GenerationResponse, error) {
	return g.generate(ctx, request, nil)
}

func (g *Generator) GenerateStream(
	ctx context.Context,
	request domain.GenerationRequest,
	emit ports.GenerationDeltaEmitter,
) (domain.GenerationResponse, error) {
	return g.generate(ctx, request, emit)
}

func (g *Generator) generate(
	ctx context.Context,
	request domain.GenerationRequest,
	emit ports.GenerationDeltaEmitter,
) (domain.GenerationResponse, error) {
	if g == nil || g.factory == nil {
		return domain.GenerationResponse{}, fmt.Errorf("dive generator is not configured")
	}
	provider := strings.ToLower(strings.TrimSpace(request.Provider))
	var apiKey string
	switch provider {
	case "anthropic":
		apiKey = g.config.AnthropicAPIKey
	case "openai":
		apiKey = g.config.OpenAIAPIKey
	default:
		return domain.GenerationResponse{}, ports.ErrProviderUnsupported
	}
	if apiKey == "" {
		return domain.GenerationResponse{}, ports.ErrProviderKeyMissing
	}

	model, err := g.factory(provider, request.Model, apiKey)
	if err != nil {
		return domain.GenerationResponse{}, err
	}
	messages := make([]*llm.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		converted, err := toDiveMessage(message)
		if err != nil {
			return domain.GenerationResponse{}, fmt.Errorf("%w: convert durable message", ports.ErrGenerationInputInvalid)
		}
		messages = append(messages, converted)
	}
	evidence := &modelEvidence{}
	checkpointState := &generationCheckpointState{}
	var agentModel llm.LLM = &evidenceModel{
		LLM:      model,
		evidence: evidence,
	}
	if streaming, ok := model.(llm.StreamingLLM); ok && emit != nil {
		agentModel = &streamingEvidenceModel{
			StreamingLLM: streaming,
			evidence:     evidence,
		}
	}
	var tools []dive.Tool
	if g.testBuiltin {
		if request.Claim == nil || g.toolCoordinator == nil {
			return domain.GenerationResponse{}, fmt.Errorf("durable test builtin requires an Invocation claim")
		}
		tools = []dive.Tool{newDeterministicEchoTool(g.toolCoordinator, *request.Claim, checkpointState)}
	}
	agent, err := dive.NewAgent(dive.AgentOptions{
		SystemPrompt:       request.Instructions,
		Model:              agentModel,
		Tools:              tools,
		ModelSettings:      &dive.ModelSettings{MaxTokens: request.MaxOutputTokens},
		ToolIterationLimit: request.MaxIterations,
	})
	if err != nil {
		return domain.GenerationResponse{}, fmt.Errorf("configure Dive agent: %w", err)
	}
	options := []dive.CreateResponseOption{dive.WithMessages(messages...)}
	if emit != nil || g.testBuiltin {
		options = append(options, dive.WithEventCallback(func(_ context.Context, item *dive.ResponseItem) error {
			if delta, ok := normalizedDelta(item); ok {
				if emit != nil {
					emit(delta)
				}
			}
			if g.testBuiltin && item != nil && item.Type == dive.ResponseItemTypeMessage && item.Message != nil && item.Usage != nil {
				iteration := int(checkpointState.iteration.Add(1))
				message, err := fromDiveMessage(item.Message)
				if err != nil {
					return err
				}
				usage := normalizeUsage(item.Usage)
				usage.Iterations = 1
				servedModel := evidence.servedModel()
				if servedModel == "" {
					servedModel = request.Model
				}
				requests, err := toolRequests(item.Message)
				if err != nil {
					return err
				}
				_, err = g.toolCoordinator.RecordModelCheckpoint(ctx, *request.Claim, domain.ModelCheckpointInput{
					Iteration: iteration,
					Message:   message,
					Usage:     usage,
					Provenance: domain.ModelProvenance{
						Provider:         strings.ToLower(strings.TrimSpace(request.Provider)),
						RequestedModel:   request.Model,
						ServedModel:      servedModel,
						CredentialSource: "installation_byok",
					},
					ToolCalls: requests,
				})
				if err != nil {
					return err
				}
				checkpointState.addUsage(usage)
				if kind := checkpointBudgetExceeded(
					request,
					checkpointState.usageSnapshot(),
					len(requests) != 0,
					g.clock.Now().UTC(),
				); kind != "" {
					checkpointState.setBudget(kind)
					return errCheckpointBudget
				}
			}
			return nil
		}))
	}
	response, err := agent.CreateResponse(ctx, options...)
	if err != nil {
		if errors.Is(err, errCheckpointBudget) {
			servedModel := evidence.servedModel()
			if servedModel == "" {
				servedModel = request.Model
			}
			return domain.GenerationResponse{
				Usage:                checkpointState.usageSnapshot(),
				ServedModel:          servedModel,
				MessagesCheckpointed: true,
				BudgetExceeded:       checkpointState.budgetValue(),
			}, nil
		}
		if ctx.Err() != nil {
			return domain.GenerationResponse{}, ctx.Err()
		}
		return domain.GenerationResponse{}, fmt.Errorf("%w: Dive provider call", ports.ErrGenerationFailed)
	}
	if response == nil || response.Status == dive.ResponseStatusSuspended || response.Suspension != nil {
		return domain.GenerationResponse{}, ports.ErrModelResponseInvalid
	}
	if len(response.OutputMessages) == 0 || response.Usage == nil {
		return domain.GenerationResponse{}, ports.ErrModelResponseInvalid
	}
	output := make([]domain.GenerationMessage, 0, len(response.OutputMessages))
	if g.testBuiltin {
		if strings.TrimSpace(response.OutputText()) == "" {
			return domain.GenerationResponse{}, ports.ErrModelResponseInvalid
		}
		servedModel := evidence.servedModel()
		if servedModel == "" {
			servedModel = request.Model
		}
		usage := checkpointState.usageSnapshot()
		return domain.GenerationResponse{
			Usage:                usage,
			ServedModel:          servedModel,
			MessagesCheckpointed: true,
		}, nil
	}
	for _, message := range response.OutputMessages {
		converted, err := fromDiveMessage(message)
		if err != nil {
			return domain.GenerationResponse{}, fmt.Errorf("%w: normalize output message", ports.ErrModelResponseInvalid)
		}
		output = append(output, converted)
	}
	servedModel := evidence.servedModel()
	if servedModel == "" {
		servedModel = request.Model
	}
	usage := normalizeUsage(response.Usage)
	usage.Iterations = evidence.iterations()
	return domain.GenerationResponse{
		Messages:    output,
		Usage:       usage,
		ServedModel: servedModel,
	}, nil
}

const deterministicEchoToolName = "nvoken_test_echo"

var errCheckpointBudget = errors.New("checkpoint budget exceeded")

type generationCheckpointState struct {
	iteration atomic.Int64
	mu        sync.Mutex
	usage     domain.ModelUsage
	budget    string
}

func (s *generationCheckpointState) addUsage(usage domain.ModelUsage) {
	s.mu.Lock()
	s.usage.InputTokens += usage.InputTokens
	s.usage.OutputTokens += usage.OutputTokens
	s.usage.CacheCreationInputTokens += usage.CacheCreationInputTokens
	s.usage.CacheReadInputTokens += usage.CacheReadInputTokens
	s.usage.ReasoningTokens += usage.ReasoningTokens
	s.usage.Iterations++
	if usage.EstimatedCost != nil {
		if s.usage.EstimatedCost == nil {
			copy := *usage.EstimatedCost
			s.usage.EstimatedCost = &copy
		} else {
			s.usage.EstimatedCost.Input += usage.EstimatedCost.Input
			s.usage.EstimatedCost.Output += usage.EstimatedCost.Output
			s.usage.EstimatedCost.CacheRead += usage.EstimatedCost.CacheRead
			s.usage.EstimatedCost.CacheWrite += usage.EstimatedCost.CacheWrite
			s.usage.EstimatedCost.Total += usage.EstimatedCost.Total
		}
	}
	s.mu.Unlock()
}

func (s *generationCheckpointState) usageSnapshot() domain.ModelUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := s.usage
	if s.usage.EstimatedCost != nil {
		cost := *s.usage.EstimatedCost
		copy.EstimatedCost = &cost
	}
	return copy
}

func (s *generationCheckpointState) setBudget(kind string) {
	s.mu.Lock()
	s.budget = kind
	s.mu.Unlock()
}

func (s *generationCheckpointState) budgetValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.budget
}

func checkpointBudgetExceeded(request domain.GenerationRequest, usage domain.ModelUsage, needsContinuation bool, now time.Time) string {
	if usage.Iterations > request.MaxIterations || (needsContinuation && usage.Iterations >= request.MaxIterations) {
		return "iterations"
	}
	if request.MaxOutputTokens != nil && usage.OutputTokens > *request.MaxOutputTokens {
		return "output_tokens"
	}
	if request.Claim != nil && request.Claim.Invocation.MaxEstimatedCostMicros != nil {
		if usage.EstimatedCost == nil || (usage.EstimatedCost.Currency != "" && !strings.EqualFold(usage.EstimatedCost.Currency, "USD")) {
			return "estimated_cost_unavailable"
		}
		if int64(math.Ceil(usage.EstimatedCost.Total*1_000_000)) > *request.Claim.Invocation.MaxEstimatedCostMicros {
			return "estimated_cost"
		}
	}
	if request.Claim != nil {
		if !request.Claim.Invocation.WallClockDeadlineAt.After(now) {
			return "wall_clock"
		}
		if request.Claim.Invocation.ExecutionDeadlineAt != nil && !request.Claim.Invocation.ExecutionDeadlineAt.After(now) {
			if request.Claim.Invocation.ExecutionDeadlineScope != nil {
				return *request.Claim.Invocation.ExecutionDeadlineScope
			}
			return "execution_segment"
		}
	}
	return ""
}

func toolRequests(message *llm.Message) ([]domain.ToolCallRequest, error) {
	var requests []domain.ToolCallRequest
	if message == nil {
		return requests, nil
	}
	for _, content := range message.Content {
		call, ok := content.(*llm.ToolUseContent)
		if !ok {
			continue
		}
		if call.Name != deterministicEchoToolName {
			return nil, fmt.Errorf("%w: unsupported builtin tool %q", ports.ErrGenerationInputInvalid, call.Name)
		}
		requests = append(requests, domain.ToolCallRequest{
			ProviderCallID: call.ID,
			Name:           call.Name,
			Mode:           domain.ToolCallModeBuiltin,
			Input:          append([]byte(nil), call.Input...),
		})
	}
	return requests, nil
}

type deterministicEchoTool struct {
	coordinator ports.ToolCallCoordinator
	claim       domain.InvocationClaim
	state       *generationCheckpointState
}

func newDeterministicEchoTool(coordinator ports.ToolCallCoordinator, claim domain.InvocationClaim, state *generationCheckpointState) dive.Tool {
	return &deterministicEchoTool{
		coordinator: coordinator,
		claim:       claim,
		state:       state,
	}
}

func (*deterministicEchoTool) Name() string { return deterministicEchoToolName }
func (*deterministicEchoTool) Description() string {
	return "Echo deterministic JSON for durable ToolCall tests."
}
func (*deterministicEchoTool) Schema() *dive.Schema {
	allowAdditional := true
	return &dive.Schema{
		Type:                 dive.Object,
		AdditionalProperties: &allowAdditional,
	}
}
func (*deterministicEchoTool) Annotations() *dive.ToolAnnotations { return nil }

func (t *deterministicEchoTool) Call(ctx context.Context, input any) (*dive.ToolResult, error) {
	providerCallID := dive.ToolCallID(ctx)
	iteration := int(t.state.iteration.Load())
	execution, err := t.coordinator.StartBuiltinToolCall(ctx, t.claim, iteration, providerCallID)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	text := string(canonical)
	encodedText, _ := json.Marshal(text)
	if _, err := t.coordinator.AcceptBuiltinToolResult(ctx, t.claim, execution, encodedText, false); err != nil {
		return nil, err
	}
	return dive.NewToolResultText(text), nil
}

// evidenceModel captures blocking-provider evidence without exposing the raw
// response outside this adapter.
type evidenceModel struct {
	llm.LLM
	evidence *modelEvidence
}

type modelEvidence struct {
	mu     sync.Mutex
	served string
	calls  int
}

func (m *evidenceModel) Generate(ctx context.Context, options ...llm.Option) (*llm.Response, error) {
	m.evidence.recordCall()
	response, err := m.LLM.Generate(ctx, options...)
	if response != nil && strings.TrimSpace(response.Model) != "" {
		m.evidence.recordModel(response.Model)
	}
	return response, err
}

type streamingEvidenceModel struct {
	llm.StreamingLLM
	evidence *modelEvidence
}

func (m *streamingEvidenceModel) Stream(ctx context.Context, options ...llm.Option) (llm.StreamIterator, error) {
	m.evidence.recordCall()
	iterator, err := m.StreamingLLM.Stream(ctx, options...)
	if err != nil {
		return nil, err
	}
	return &evidenceIterator{
		StreamIterator: iterator,
		evidence:       m.evidence,
	}, nil
}

type evidenceIterator struct {
	llm.StreamIterator
	evidence *modelEvidence
}

func (i *evidenceIterator) Next() bool {
	if !i.StreamIterator.Next() {
		return false
	}
	event := i.StreamIterator.Event()
	if event != nil && event.Message != nil {
		i.evidence.recordModel(event.Message.Model)
	}
	return true
}

func (m *modelEvidence) recordCall() {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
}

func (m *modelEvidence) recordModel(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}
	m.mu.Lock()
	m.served = model
	m.mu.Unlock()
}

func (m *modelEvidence) iterations() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *modelEvidence) servedModel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.served
}

func normalizedDelta(item *dive.ResponseItem) (domain.GenerationDelta, bool) {
	if item == nil || item.Type != dive.ResponseItemTypeModelEvent || item.Event == nil {
		return domain.GenerationDelta{}, false
	}
	event := item.Event
	index := 0
	if event.Index != nil {
		index = *event.Index
	}
	switch event.Type {
	case llm.EventTypeContentBlockStart:
		if event.ContentBlock == nil {
			return domain.GenerationDelta{}, false
		}
		switch event.ContentBlock.Type {
		case llm.ContentTypeText:
			if event.ContentBlock.Text != "" {
				return domain.GenerationDelta{
					ContentIndex: index,
					Type:         "text",
					Text:         event.ContentBlock.Text,
				}, true
			}
		case llm.ContentTypeThinking:
			if event.ContentBlock.Thinking != "" {
				return domain.GenerationDelta{
					ContentIndex: index,
					Type:         "thinking",
					Thinking:     event.ContentBlock.Thinking,
				}, true
			}
		}
	case llm.EventTypeContentBlockDelta:
		if event.Delta == nil {
			return domain.GenerationDelta{}, false
		}
		switch event.Delta.Type {
		case llm.EventDeltaTypeText:
			if event.Delta.Text != "" {
				return domain.GenerationDelta{
					ContentIndex: index,
					Type:         "text",
					Text:         event.Delta.Text,
				}, true
			}
		case llm.EventDeltaTypeThinking:
			if event.Delta.Thinking != "" {
				return domain.GenerationDelta{
					ContentIndex: index,
					Type:         "thinking",
					Thinking:     event.Delta.Thinking,
				}, true
			}
		}
	}
	return domain.GenerationDelta{}, false
}

func toDiveMessage(message domain.GenerationMessage) (*llm.Message, error) {
	role, err := toDiveRole(message.Role)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(struct {
		Role    llm.Role        `json:"role"`
		Content json.RawMessage `json:"content"`
	}{
		Role:    role,
		Content: message.Content,
	})
	if err != nil {
		return nil, err
	}
	var converted llm.Message
	if err := json.Unmarshal(payload, &converted); err != nil {
		return nil, err
	}
	return &converted, nil
}

func fromDiveMessage(message *llm.Message) (domain.GenerationMessage, error) {
	if message == nil || message.Role != llm.Assistant || len(message.Content) == 0 {
		return domain.GenerationMessage{}, errors.New("dive output is not a nonempty assistant message")
	}
	content, err := json.Marshal(message.Content)
	if err != nil {
		return domain.GenerationMessage{}, err
	}
	return domain.GenerationMessage{
		Role:    domain.MessageRoleAssistant,
		Content: content,
	}, nil
}

func toDiveRole(role domain.MessageRole) (llm.Role, error) {
	switch role {
	case domain.MessageRoleUser:
		return llm.User, nil
	case domain.MessageRoleAssistant:
		return llm.Assistant, nil
	case domain.MessageRoleTool:
		return llm.User, nil
	default:
		return "", fmt.Errorf("unsupported message role %q", role)
	}
}

func normalizeUsage(usage *llm.Usage) domain.ModelUsage {
	normalized := domain.ModelUsage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		ReasoningTokens:          usage.ReasoningTokens,
	}
	if usage.Cost != nil {
		normalized.EstimatedCost = &domain.ModelCost{
			Input:      usage.Cost.Input,
			Output:     usage.Cost.Output,
			CacheRead:  usage.Cost.CacheRead,
			CacheWrite: usage.Cost.CacheWrite,
			Total:      usage.Cost.Total,
			Currency:   usage.Cost.Currency,
			Model:      usage.Cost.Model,
		}
	}
	return normalized
}
