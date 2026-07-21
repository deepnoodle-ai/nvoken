// Package divegen adapts nvoken's durable, provider-neutral generation request
// to one explicit Dive provider call.
package divegen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/dive"
	"github.com/deepnoodle-ai/dive/llm"
	"github.com/deepnoodle-ai/dive/providers/anthropic"
	"github.com/deepnoodle-ai/dive/providers/openai"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/structuredoutput"
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
	logger          *slog.Logger
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

// WithToolCoordinator enables production reserved builtins declared by the
// immutable execution spec. It does not enable host-defined tools.
func WithToolCoordinator(coordinator ports.ToolCallCoordinator) Option {
	return func(generator *Generator) {
		generator.toolCoordinator = coordinator
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

func WithLogger(logger *slog.Logger) Option {
	return func(generator *Generator) {
		if logger != nil {
			generator.logger = logger
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
		logger:  slog.Default(),
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
	if request.Resume != nil {
		checkpointState.seed(request.Resume.Iteration, request.Resume.Usage)
	}
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
	clientToolNames := make(map[string]struct{}, len(request.ClientTools))
	for _, definition := range request.ClientTools {
		var toolSchema dive.Schema
		if err := json.Unmarshal(definition.InputSchema, &toolSchema); err != nil {
			return domain.GenerationResponse{}, fmt.Errorf("%w: project client tool schema", ports.ErrGenerationInputInvalid)
		}
		clientToolNames[definition.Name] = struct{}{}
		tools = append(tools, &clientTool{
			name:        definition.Name,
			description: definition.Description,
			schema:      &toolSchema,
		})
	}
	var outputCapture *structuredOutputCapture
	if request.StructuredOutput != nil {
		if request.Claim == nil || g.toolCoordinator == nil {
			return domain.GenerationResponse{}, fmt.Errorf("structured output requires a durable Invocation claim")
		}
		compiled, err := structuredoutput.CompileSchema(request.StructuredOutput.Schema)
		if err != nil || len(request.StructuredOutput.SchemaDigest) != sha256.Size {
			return domain.GenerationResponse{}, fmt.Errorf("%w: invalid structured-output contract", ports.ErrGenerationInputInvalid)
		}
		var toolSchema dive.Schema
		if err := json.Unmarshal(request.StructuredOutput.Schema, &toolSchema); err != nil {
			return domain.GenerationResponse{}, fmt.Errorf("%w: project structured-output schema", ports.ErrGenerationInputInvalid)
		}
		outputCapture = newStructuredOutputCapture(request.Resume)
		tools = append(tools, newStructuredOutputTool(
			g.toolCoordinator,
			*request.Claim,
			checkpointState,
			&toolSchema,
			compiled,
			request.StructuredOutput.SchemaDigest,
			outputCapture,
		))
	}
	if g.testBuiltin {
		if request.Claim == nil || g.toolCoordinator == nil {
			return domain.GenerationResponse{}, fmt.Errorf("durable test builtin requires an Invocation claim")
		}
		tools = append(tools, newDeterministicEchoTool(g.toolCoordinator, *request.Claim, checkpointState))
	}
	durableExecution := request.Claim != nil
	if durableExecution && g.toolCoordinator == nil {
		return domain.GenerationResponse{}, fmt.Errorf("durable generation requires a ToolCall coordinator")
	}
	if request.Resume != nil && request.Claim == nil {
		return domain.GenerationResponse{}, fmt.Errorf("durable generation resume requires an Invocation claim")
	}
	if request.Resume != nil && len(request.Resume.OpenToolCalls) != 0 {
		replayed, err := replayOpenToolCalls(
			ctx,
			*request.Claim,
			request.Resume.OpenToolCalls,
			tools,
			g.logger,
		)
		if err != nil {
			if errors.Is(err, ports.ErrLeaseLost) {
				return domain.GenerationResponse{}, err
			}
			return domain.GenerationResponse{}, fmt.Errorf(
				"%w: replay durable builtin result",
				ports.ErrGenerationRecoveryInvalid,
			)
		}
		messages = append(messages, replayed...)
	}
	systemPrompt := request.Instructions
	if request.StructuredOutput != nil {
		systemPrompt += "\n\n# Output Contract\nWhen your work is finished, call nvoken_submit_output exactly once with the final object. Its input schema is the required shape. After the tool confirms acceptance, end with a brief status note. Prose and fenced JSON do not satisfy the output contract."
	}
	agent, err := dive.NewAgent(dive.AgentOptions{
		SystemPrompt:       systemPrompt,
		Model:              agentModel,
		Tools:              tools,
		ModelSettings:      &dive.ModelSettings{MaxTokens: request.MaxOutputTokens},
		ToolIterationLimit: request.MaxIterations,
	})
	if err != nil {
		return domain.GenerationResponse{}, fmt.Errorf("configure Dive agent: %w", err)
	}
	options := []dive.CreateResponseOption{dive.WithMessages(messages...)}
	if emit != nil || durableExecution {
		options = append(options, dive.WithEventCallback(func(_ context.Context, item *dive.ResponseItem) error {
			if delta, ok := normalizedDelta(item); ok {
				if emit != nil {
					emit(delta)
				}
			}
			if durableExecution && item != nil && item.Type == dive.ResponseItemTypeMessage && item.Message != nil && item.Usage != nil {
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
				requests, err := toolRequests(
					item.Message,
					request.StructuredOutput != nil,
					g.testBuiltin,
					clientToolNames,
				)
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
	if response != nil && response.Status == dive.ResponseStatusSuspended && response.Suspension != nil {
		if len(response.Suspension.PendingToolCalls) == 0 {
			return domain.GenerationResponse{}, ports.ErrModelResponseInvalid
		}
		for _, pending := range response.Suspension.PendingToolCalls {
			if pending == nil {
				return domain.GenerationResponse{}, ports.ErrModelResponseInvalid
			}
			if _, ok := clientToolNames[pending.Name]; !ok {
				return domain.GenerationResponse{}, ports.ErrModelResponseInvalid
			}
		}
		servedModel := evidence.servedModel()
		if servedModel == "" {
			servedModel = request.Model
		}
		return domain.GenerationResponse{
			Usage:                checkpointState.usageSnapshot(),
			ServedModel:          servedModel,
			MessagesCheckpointed: true,
			ClientToolsPending:   true,
		}, nil
	}
	if response == nil || response.Status == dive.ResponseStatusSuspended || response.Suspension != nil {
		return domain.GenerationResponse{}, ports.ErrModelResponseInvalid
	}
	if len(response.OutputMessages) == 0 || response.Usage == nil {
		return domain.GenerationResponse{}, ports.ErrModelResponseInvalid
	}
	output := make([]domain.GenerationMessage, 0, len(response.OutputMessages))
	if durableExecution {
		if strings.TrimSpace(response.OutputText()) == "" {
			if outputCapture == nil {
				return domain.GenerationResponse{}, ports.ErrModelResponseInvalid
			}
			servedModel := evidence.servedModel()
			if servedModel == "" {
				servedModel = request.Model
			}
			return domain.GenerationResponse{
				Usage:                   checkpointState.usageSnapshot(),
				ServedModel:             servedModel,
				MessagesCheckpointed:    true,
				StructuredOutputFailure: "invalid",
			}, nil
		}
		servedModel := evidence.servedModel()
		if servedModel == "" {
			servedModel = request.Model
		}
		usage := checkpointState.usageSnapshot()
		generated := domain.GenerationResponse{
			Usage:                usage,
			ServedModel:          servedModel,
			MessagesCheckpointed: true,
		}
		if outputCapture != nil {
			generated.StructuredOutput = outputCapture.output()
			if generated.StructuredOutput == nil {
				generated.StructuredOutputFailure = outputCapture.failure()
			}
		}
		return generated, nil
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

func (s *generationCheckpointState) seed(iteration int, usage domain.ModelUsage) {
	s.iteration.Store(int64(iteration))
	s.mu.Lock()
	s.usage = usage
	if usage.EstimatedCost != nil {
		cost := *usage.EstimatedCost
		s.usage.EstimatedCost = &cost
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

func toolRequests(
	message *llm.Message,
	structured bool,
	testBuiltin bool,
	clientToolNames map[string]struct{},
) ([]domain.ToolCallRequest, error) {
	var requests []domain.ToolCallRequest
	if message == nil {
		return requests, nil
	}
	for _, content := range message.Content {
		call, ok := content.(*llm.ToolUseContent)
		if !ok {
			continue
		}
		mode := domain.ToolCallModeBuiltin
		_, client := clientToolNames[call.Name]
		allowedBuiltin := (structured && call.Name == structuredoutput.ReservedToolName) ||
			(testBuiltin && call.Name == deterministicEchoToolName)
		if client {
			mode = domain.ToolCallModeClient
		} else if !allowedBuiltin {
			return nil, fmt.Errorf("%w: unsupported builtin tool %q", ports.ErrGenerationInputInvalid, call.Name)
		}
		requests = append(requests, domain.ToolCallRequest{
			ProviderCallID: call.ID,
			Name:           call.Name,
			Mode:           mode,
			Input:          append([]byte(nil), call.Input...),
		})
	}
	return requests, nil
}

type clientTool struct {
	name        string
	description string
	schema      *dive.Schema
}

func (t *clientTool) Name() string {
	return t.name
}

func (t *clientTool) Description() string {
	return t.description
}

func (t *clientTool) Schema() *dive.Schema {
	return t.schema
}

func (*clientTool) Annotations() *dive.ToolAnnotations {
	return &dive.ToolAnnotations{}
}

func (t *clientTool) Call(context.Context, any) (*dive.ToolResult, error) {
	return dive.NewSuspendResult("Waiting for the host to provide the client tool result.", map[string]any{
		"tool_name": t.name,
	}), nil
}

type deterministicEchoTool struct {
	coordinator ports.ToolCallCoordinator
	claim       domain.InvocationClaim
	state       *generationCheckpointState
}

type structuredOutputCapture struct {
	mu       sync.Mutex
	accepted *domain.StructuredOutput
	last     string
}

func newStructuredOutputCapture(resume *domain.GenerationResume) *structuredOutputCapture {
	capture := &structuredOutputCapture{
		last: "missing",
	}
	if resume == nil {
		return capture
	}
	capture.last = resume.StructuredOutputFailure
	if capture.last == "" && resume.StructuredOutput == nil {
		capture.last = "missing"
	}
	if resume.StructuredOutput != nil {
		output := *resume.StructuredOutput
		output.Value = append(json.RawMessage(nil), resume.StructuredOutput.Value...)
		capture.accepted = &output
	}
	return capture
}

func (c *structuredOutputCapture) output() *domain.StructuredOutput {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accepted == nil {
		return nil
	}
	copy := *c.accepted
	copy.Value = append(json.RawMessage(nil), c.accepted.Value...)
	return &copy
}

func (c *structuredOutputCapture) failure() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accepted != nil {
		return ""
	}
	return c.last
}

type structuredOutputTool struct {
	coordinator  ports.ToolCallCoordinator
	claim        domain.InvocationClaim
	state        *generationCheckpointState
	schema       *dive.Schema
	compiled     *structuredoutput.Compiled
	schemaDigest []byte
	capture      *structuredOutputCapture
}

func newStructuredOutputTool(
	coordinator ports.ToolCallCoordinator,
	claim domain.InvocationClaim,
	state *generationCheckpointState,
	schema *dive.Schema,
	compiled *structuredoutput.Compiled,
	schemaDigest []byte,
	capture *structuredOutputCapture,
) dive.Tool {
	return &structuredOutputTool{
		coordinator:  coordinator,
		claim:        claim,
		state:        state,
		schema:       schema,
		compiled:     compiled,
		schemaDigest: append([]byte(nil), schemaDigest...),
		capture:      capture,
	}
}

func (*structuredOutputTool) Name() string { return structuredoutput.ReservedToolName }

func (*structuredOutputTool) Description() string {
	return "Submit the final structured output object. After acceptance, finish with a brief status note."
}

func (t *structuredOutputTool) Schema() *dive.Schema { return t.schema }

func (*structuredOutputTool) Annotations() *dive.ToolAnnotations {
	return &dive.ToolAnnotations{
		Title:              "Submit output",
		ReadOnlyHint:       true,
		IdempotentHint:     true,
		SequentialOnlyHint: true,
	}
}

func (t *structuredOutputTool) Call(ctx context.Context, input any) (*dive.ToolResult, error) {
	providerCallID := dive.ToolCallID(ctx)
	iteration := int(t.state.iteration.Load())
	execution, err := t.coordinator.StartBuiltinToolCall(ctx, t.claim, iteration, providerCallID)
	if err != nil {
		return nil, err
	}
	raw, err := rawToolInput(input)
	if err != nil {
		return nil, err
	}

	t.capture.mu.Lock()
	defer t.capture.mu.Unlock()
	if t.capture.accepted != nil {
		message := "Output was already accepted. Do not submit it again; finish with a brief status note."
		if err := t.acceptResult(ctx, execution, message, false); err != nil {
			return nil, err
		}
		return dive.NewToolResultText(message), nil
	}
	reason := "invalid"
	if len(raw) > structuredoutput.MaxValueBytes {
		reason = "oversized"
	}
	var validationErr error
	if reason != "oversized" {
		validationErr = t.compiled.ValidateValue(raw)
	}
	if reason == "oversized" || validationErr != nil {
		t.capture.last = reason
		message := "Output rejected: " + boundedValidationFeedback(validationErr) + ". Correct it and call nvoken_submit_output again."
		if reason == "oversized" {
			message = "Output rejected because it is too large. Return a smaller object and call nvoken_submit_output again."
		}
		if err := t.acceptResult(ctx, execution, message, true); err != nil {
			return nil, err
		}
		return dive.NewToolResultError(message), nil
	}
	message := "Output accepted. Finish with a brief status note."
	settled, err := t.acceptResultCall(ctx, execution, message, false)
	if err != nil {
		return nil, err
	}
	t.capture.accepted = &domain.StructuredOutput{
		Value: append(json.RawMessage(nil), raw...),
		Provenance: domain.StructuredOutputProvenance{
			Source:       structuredoutput.ProvenanceSource,
			ToolCallID:   settled.ID,
			SchemaSHA256: hex.EncodeToString(t.schemaDigest),
		},
	}
	t.capture.last = ""
	return dive.NewToolResultText(message), nil
}

func boundedValidationFeedback(err error) string {
	if err == nil {
		return "it does not satisfy the bounded schema contract"
	}
	message := strings.Join(strings.Fields(err.Error()), " ")
	const maximumBytes = 512
	if len(message) <= maximumBytes {
		return message
	}
	cut := maximumBytes
	for cut > 0 && !utf8.RuneStart(message[cut]) {
		cut--
	}
	return message[:cut] + "…"
}

func (t *structuredOutputTool) acceptResult(
	ctx context.Context,
	execution domain.ToolCallExecution,
	message string,
	isError bool,
) error {
	_, err := t.acceptResultCall(ctx, execution, message, isError)
	return err
}

func (t *structuredOutputTool) acceptResultCall(
	ctx context.Context,
	execution domain.ToolCallExecution,
	message string,
	isError bool,
) (domain.ToolCall, error) {
	content, err := json.Marshal(message)
	if err != nil {
		return domain.ToolCall{}, err
	}
	return t.coordinator.AcceptBuiltinToolResult(ctx, t.claim, execution, content, isError)
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
	canonical, err := rawToolInput(input)
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

func rawToolInput(input any) (json.RawMessage, error) {
	var raw json.RawMessage
	switch value := input.(type) {
	case []byte:
		raw = append(json.RawMessage(nil), value...)
	case json.RawMessage:
		raw = append(json.RawMessage(nil), value...)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		raw = encoded
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("tool input is not valid JSON")
	}
	return raw, nil
}

func replayOpenToolCalls(
	ctx context.Context,
	claim domain.InvocationClaim,
	calls []domain.ResumableToolCall,
	tools []dive.Tool,
	logger *slog.Logger,
) ([]*llm.Message, error) {
	byName := make(map[string]dive.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name()] = tool
	}
	messages := make([]*llm.Message, 0, len(calls))
	for _, resumable := range calls {
		tool, ok := byName[resumable.Call.Name]
		if !ok || resumable.Call.ProviderCallID == "" || resumable.Call.ID == "" {
			return nil, errors.New("durable builtin is unavailable")
		}
		result, err := tool.Call(
			dive.WithToolCallID(ctx, resumable.Call.ProviderCallID),
			append(json.RawMessage(nil), resumable.Input...),
		)
		if err != nil {
			return nil, err
		}
		logger.Info(
			"Builtin ToolCall attempt resumed",
			"invocation_id",
			resumable.Call.InvocationID,
			"tool_call_id",
			resumable.Call.ID,
			"lease_attempt",
			claim.Attempt,
			"prior_tool_attempt",
			resumable.Call.CurrentAttempt,
		)
		text, err := replayedToolResultText(result)
		if err != nil {
			return nil, err
		}
		messages = append(messages, llm.NewToolResultMessage(&llm.ToolResultContent{
			ToolUseID: resumable.Call.ID,
			Content:   text,
			IsError:   result.IsError,
		}))
	}
	return messages, nil
}

func replayedToolResultText(result *dive.ToolResult) (string, error) {
	if result == nil || result.Suspend != nil || result.Background != nil || len(result.Content) != 1 {
		return "", errors.New("durable builtin returned an unsupported result")
	}
	content := result.Content[0]
	if content == nil || content.Type != dive.ToolResultContentTypeText || content.Text == "" {
		return "", errors.New("durable builtin did not return text")
	}
	return content.Text, nil
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
