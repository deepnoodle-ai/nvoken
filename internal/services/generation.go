package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const credentialSourceInstallationBYOK = "installation_byok"

type generationStore interface {
	ports.ExecutionSpecSnapshotRepository
	ports.SessionMessageRepository
}

type generationInvocationReader interface {
	GetInvocation(context.Context, string) (domain.Invocation, error)
}

// GenerationExecutor reconstructs one tool-free turn from durable state. It
// returns a desired terminal result; InvocationExecutionService alone owns the
// fenced transaction that publishes that result.
type GenerationExecutor struct {
	store     generationStore
	generator ports.ModelGenerator
	events    ports.LiveEventPublisher
	clock     ports.Clock
	logger    *slog.Logger
}

type GenerationExecutorOption func(*GenerationExecutor)

func WithGenerationLiveEvents(events ports.LiveEventPublisher) GenerationExecutorOption {
	return func(executor *GenerationExecutor) { executor.events = events }
}

func WithGenerationClock(clock ports.Clock) GenerationExecutorOption {
	return func(executor *GenerationExecutor) {
		if clock != nil {
			executor.clock = clock
		}
	}
}

type generationClock struct{}

func (generationClock) Now() time.Time { return time.Now() }

func NewGenerationExecutor(
	store generationStore,
	generator ports.ModelGenerator,
	logger *slog.Logger,
	options ...GenerationExecutorOption,
) *GenerationExecutor {
	if logger == nil {
		logger = slog.Default()
	}
	executor := &GenerationExecutor{
		store:     store,
		generator: generator,
		clock:     generationClock{},
		logger:    logger,
	}
	for _, option := range options {
		if option != nil {
			option(executor)
		}
	}
	return executor
}

func (e *GenerationExecutor) Execute(
	ctx context.Context,
	claim domain.InvocationClaim,
) (domain.InvocationExecutionResult, error) {
	if e == nil || e.store == nil || e.generator == nil {
		return domain.InvocationExecutionResult{}, fmt.Errorf("generation executor is not configured")
	}
	snapshot, err := e.store.GetExecutionSpecSnapshot(ctx, claim.Invocation.SpecSnapshotID)
	if err != nil {
		return domain.InvocationExecutionResult{}, fmt.Errorf("load execution spec snapshot: %w", err)
	}
	if snapshot.AccountID != claim.Invocation.AccountID {
		e.logFailure(claim, "invalid_spec_scope", "", "")
		return internalGenerationFailure(), nil
	}
	spec, err := decodeInlineSpec(snapshot.Spec)
	if err != nil {
		e.logFailure(claim, "invalid_spec", "", "")
		return internalGenerationFailure(), nil
	}

	stored, err := e.store.ListSessionMessages(ctx, claim.Invocation.SessionID)
	if err != nil {
		return domain.InvocationExecutionResult{}, fmt.Errorf("load Session transcript: %w", err)
	}
	request := domain.GenerationRequest{
		Instructions:    spec.Instructions,
		Provider:        strings.ToLower(spec.Model.Provider),
		Model:           spec.Model.Name,
		MaxOutputTokens: claim.Invocation.MaxOutputTokens,
		MaxIterations:   claim.Invocation.MaxIterations,
		Claim:           &claim,
	}
	for _, tool := range spec.Tools {
		callbackURL := ""
		if tool.Callback != nil {
			callbackURL = tool.Callback.URL
		}
		request.ClientTools = append(request.ClientTools, domain.ClientToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...),
			Mode:        domain.ToolCallMode(tool.Mode),
			CallbackURL: callbackURL,
		})
	}
	if spec.Output != nil {
		digest, err := structuredOutputSchemaDigest(spec.Output.Schema)
		if err != nil || !bytes.Equal(digest, claim.Invocation.OutputSchemaDigest) {
			e.logFailure(claim, "invalid_output_contract", spec.Model.Provider, spec.Model.Name)
			return internalGenerationFailure(), nil
		}
		request.StructuredOutput = &domain.StructuredOutputRequest{
			Schema:       append(json.RawMessage(nil), spec.Output.Schema...),
			SchemaDigest: append([]byte(nil), digest...),
		}
	} else if len(claim.Invocation.OutputSchemaDigest) != 0 {
		e.logFailure(claim, "invalid_output_contract", spec.Model.Provider, spec.Model.Name)
		return internalGenerationFailure(), nil
	}
	var recovery generationRecovery
	if claim.Invocation.CurrentCheckpointSequence > 0 {
		durable, ok := e.store.(generationRecoveryStore)
		if !ok {
			if e.claimLost(ctx, claim) {
				return domain.InvocationExecutionResult{}, ports.ErrLeaseLost
			}
			e.logFailure(claim, "recovery_invalid", spec.Model.Provider, spec.Model.Name)
			return internalGenerationFailure(), nil
		}
		recovery, err = loadGenerationRecovery(
			ctx,
			durable,
			claim.Invocation,
			stored,
			request.StructuredOutput,
		)
		if err != nil {
			if errors.Is(err, errRecoveryInvalid) {
				if e.claimLost(ctx, claim) {
					return domain.InvocationExecutionResult{}, ports.ErrLeaseLost
				}
				e.logFailure(claim, "recovery_invalid", spec.Model.Provider, spec.Model.Name)
				return internalGenerationFailure(), nil
			}
			return domain.InvocationExecutionResult{}, fmt.Errorf("load generation recovery: %w", err)
		}
		request.Resume = recovery.Resume
		e.logger.Info(
			"Invocation recovery prefix loaded",
			"invocation_id",
			claim.Invocation.ID,
			"lease_attempt",
			claim.Attempt,
			"checkpoint_sequence",
			claim.Invocation.CurrentCheckpointSequence,
			"iteration",
			claim.Invocation.CurrentIteration,
			"terminal_replay",
			recovery.Final,
		)
	}
	messages, err := transcriptForClaim(claim, stored, recovery.Latest)
	if err != nil {
		if e.claimLost(ctx, claim) {
			return domain.InvocationExecutionResult{}, ports.ErrLeaseLost
		}
		class := "invalid_transcript"
		if claim.Invocation.CurrentCheckpointSequence > 0 {
			class = "recovery_invalid"
		}
		e.logFailure(claim, class, spec.Model.Provider, spec.Model.Name)
		return internalGenerationFailure(), nil
	}
	request.Messages = messages
	if err := ctx.Err(); err != nil {
		return domain.InvocationExecutionResult{}, err
	}
	var providerStarted time.Time
	providerCalled := false
	var response domain.GenerationResponse
	if recovery.ExternalToolsPending {
		response = domain.GenerationResponse{
			Usage:                recovery.Resume.Usage,
			ServedModel:          recovery.Provenance.ServedModel,
			MessagesCheckpointed: true,
			ExternalToolsPending: true,
			CredentialSource:     domain.ProviderCredentialSource(recovery.Provenance.CredentialSource),
			ProviderCredentialID: recovery.Provenance.ProviderCredentialID,
			CredentialVersionID:  recovery.Provenance.CredentialVersionID,
		}
	} else if recovery.Final {
		response = domain.GenerationResponse{
			Usage:                   recovery.Resume.Usage,
			ServedModel:             recovery.Provenance.ServedModel,
			MessagesCheckpointed:    true,
			StructuredOutput:        recovery.Resume.StructuredOutput,
			StructuredOutputFailure: recovery.Resume.StructuredOutputFailure,
			CredentialSource:        domain.ProviderCredentialSource(recovery.Provenance.CredentialSource),
			ProviderCredentialID:    recovery.Provenance.ProviderCredentialID,
			CredentialVersionID:     recovery.Provenance.CredentialVersionID,
		}
	} else {
		if request.Resume != nil {
			if kind := resumeBudgetExceeded(claim.Invocation, *request.Resume); kind != "" {
				failed := budgetGenerationFailure(kind, request.Resume.Usage, recovery.Provenance)
				failed.MessagesCheckpointed = true
				return failed, nil
			}
		}
		providerStarted = time.Now()
		providerCalled = true
		response, err = e.generate(ctx, claim, request)
	}
	if err != nil {
		class := generationErrorClass(err)
		interrupted := false
		if ctxErr := ctx.Err(); ctxErr != nil {
			class = generationErrorClass(ctxErr)
			interrupted = true
		}
		if providerCalled &&
			!errors.Is(err, ports.ErrGenerationInputInvalid) &&
			!errors.Is(err, ports.ErrGenerationRecoveryInvalid) {
			e.logProviderFailure(
				claim,
				class,
				request.Provider,
				request.Model,
				time.Since(providerStarted),
				interrupted,
			)
		}
		if ctx.Err() != nil {
			return domain.InvocationExecutionResult{}, ctx.Err()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return domain.InvocationExecutionResult{}, err
		}
		if errors.Is(err, ports.ErrCredentialUnavailable) {
			e.logFailure(claim, class, request.Provider, request.Model)
			if !response.CredentialSource.Valid() && recovery.Provenance.CredentialSource != "" {
				response.CredentialSource = domain.ProviderCredentialSource(recovery.Provenance.CredentialSource)
				response.ProviderCredentialID = recovery.Provenance.ProviderCredentialID
				response.CredentialVersionID = recovery.Provenance.CredentialVersionID
			}
			return credentialUnavailableGenerationFailure(request, response), nil
		}
		if errors.Is(err, ports.ErrGenerationInputInvalid) ||
			errors.Is(err, ports.ErrGenerationRecoveryInvalid) {
			e.logFailure(claim, class, request.Provider, request.Model)
			return internalGenerationFailure(), nil
		}
		return providerGenerationFailure(), nil
	}
	servedModel := response.ServedModel
	if strings.TrimSpace(servedModel) == "" {
		servedModel = request.Model
	}
	logProviderSuccess := func() {
		if !providerCalled {
			return
		}
		e.logProviderSuccess(
			claim,
			request.Provider,
			request.Model,
			servedModel,
			response.Usage,
			time.Since(providerStarted),
		)
		providerCalled = false
	}
	provenance := generationProvenance(request, response, servedModel)
	if response.ExternalToolsPending {
		result := domain.InvocationExecutionResult{
			Status:               domain.InvocationWaiting,
			MessagesCheckpointed: true,
			Usage:                &response.Usage,
			Provenance:           provenance,
		}
		if err := validateExecutionResult(result); err != nil {
			if providerCalled {
				e.logProviderFailure(
					claim,
					"invalid_provider_response",
					request.Provider,
					request.Model,
					time.Since(providerStarted),
					false,
				)
			} else {
				e.logFailure(claim, "invalid_provider_response", request.Provider, request.Model)
			}
			return providerGenerationFailure(), nil
		}
		logProviderSuccess()
		e.logger.Info(
			"Model generation parked for external tools",
			"invocation_id",
			claim.Invocation.ID,
			"lease_attempt",
			claim.Attempt,
		)
		return result, nil
	}
	result := domain.InvocationExecutionResult{
		Status:               domain.InvocationCompleted,
		AssistantMessages:    response.Messages,
		MessagesCheckpointed: response.MessagesCheckpointed,
		Usage:                &response.Usage,
		Provenance:           provenance,
		StructuredOutput:     response.StructuredOutput,
	}
	if response.MessagesCheckpointed {
		result.AssistantMessages = nil
	}
	if response.BudgetExceeded != "" {
		logProviderSuccess()
		e.logger.Warn("Model generation budget exceeded",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt, "budget_kind", response.BudgetExceeded)
		failed := budgetGenerationFailure(response.BudgetExceeded, response.Usage, *result.Provenance)
		failed.MessagesCheckpointed = response.MessagesCheckpointed
		return failed, nil
	}
	if response.StructuredOutputFailure != "" {
		logProviderSuccess()
		failed := structuredOutputGenerationFailure(
			response.StructuredOutputFailure,
			response.Usage,
			*result.Provenance,
		)
		failed.MessagesCheckpointed = response.MessagesCheckpointed
		return failed, nil
	}
	if err := validateExecutionResult(result); err != nil {
		if providerCalled {
			e.logProviderFailure(
				claim,
				"invalid_provider_response",
				request.Provider,
				request.Model,
				time.Since(providerStarted),
				false,
			)
		} else {
			e.logFailure(claim, "invalid_provider_response", request.Provider, request.Model)
		}
		return providerGenerationFailure(), nil
	}
	logProviderSuccess()
	if ctx.Err() != nil {
		failed := deadlineGenerationFailure(claim, &response.Usage, result.Provenance)
		failed.MessagesCheckpointed = response.MessagesCheckpointed
		return failed, nil
	}
	if kind := exceededGenerationBudget(claim.Invocation, response.Usage); kind != "" {
		e.logger.Warn("Model generation budget exceeded",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt, "budget_kind", kind)
		failed := budgetGenerationFailure(kind, response.Usage, *result.Provenance)
		failed.MessagesCheckpointed = response.MessagesCheckpointed
		return failed, nil
	}
	return result, nil
}

func (e *GenerationExecutor) claimLost(ctx context.Context, claim domain.InvocationClaim) bool {
	reader, ok := e.store.(generationInvocationReader)
	if !ok {
		return false
	}
	current, err := reader.GetInvocation(ctx, claim.Invocation.ID)
	if err != nil {
		return false
	}
	return !claimOwns(current, claim, e.clock.Now().UTC())
}

func (e *GenerationExecutor) generate(
	ctx context.Context,
	claim domain.InvocationClaim,
	request domain.GenerationRequest,
) (domain.GenerationResponse, error) {
	streaming, ok := e.generator.(ports.StreamingModelGenerator)
	if !ok {
		return e.generator.Generate(ctx, request)
	}
	var sequence atomic.Int64
	emit := func(delta domain.GenerationDelta) {
		if e.events == nil {
			return
		}
		payload, err := json.Marshal(domain.GenerationDeltaEvent{
			EventType:     domain.LiveEventGenerationDelta,
			SessionID:     claim.Invocation.SessionID,
			InvocationID:  claim.Invocation.ID,
			LeaseAttempt:  claim.Attempt,
			DeltaSequence: sequence.Add(1),
			Delta:         delta,
			EmittedAt:     time.Now().UTC(),
		})
		if err != nil {
			e.logger.Warn("generation delta encode failed", "invocation_id", claim.Invocation.ID)
			return
		}
		e.events.Publish(ctx, ports.LiveEvent{
			Type:      domain.LiveEventGenerationDelta,
			AccountID: claim.Invocation.AccountID,
			SessionID: claim.Invocation.SessionID,
			Payload:   payload,
		})
	}
	return streaming.GenerateStream(ctx, request, emit)
}

func (e *GenerationExecutor) logFailure(claim domain.InvocationClaim, class, provider, model string) {
	e.logger.Warn("Model generation failed",
		"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt,
		"class", class, "provider", provider, "requested_model", model)
}

func (e *GenerationExecutor) logProviderSuccess(
	claim domain.InvocationClaim,
	provider string,
	requestedModel string,
	servedModel string,
	usage domain.ModelUsage,
	latency time.Duration,
) {
	e.logger.Info(
		"Provider generation completed",
		"event",
		"provider_generation",
		"outcome",
		"success",
		"invocation_id",
		claim.Invocation.ID,
		"lease_attempt",
		claim.Attempt,
		"provider",
		provider,
		"requested_model",
		requestedModel,
		"served_model",
		servedModel,
		"iterations",
		usage.Iterations,
		"input_tokens",
		usage.InputTokens,
		"output_tokens",
		usage.OutputTokens,
		"cache_creation_input_tokens",
		usage.CacheCreationInputTokens,
		"cache_read_input_tokens",
		usage.CacheReadInputTokens,
		"reasoning_tokens",
		usage.ReasoningTokens,
		"generation_latency_ms",
		latency.Milliseconds(),
	)
}

func (e *GenerationExecutor) logProviderFailure(
	claim domain.InvocationClaim,
	class string,
	provider string,
	requestedModel string,
	latency time.Duration,
	interrupted bool,
) {
	message := "Provider generation failed"
	outcome := "failed"
	if interrupted {
		message = "Provider generation canceled"
		outcome = "canceled"
	}
	e.logger.Warn(
		message,
		"event",
		"provider_generation",
		"outcome",
		outcome,
		"invocation_id",
		claim.Invocation.ID,
		"lease_attempt",
		claim.Attempt,
		"class",
		class,
		"provider",
		provider,
		"requested_model",
		requestedModel,
		"generation_latency_ms",
		latency.Milliseconds(),
	)
}

func decodeInlineSpec(payload []byte) (InlineExecutionSpec, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var spec InlineExecutionSpec
	if err := decoder.Decode(&spec); err != nil {
		return InlineExecutionSpec{}, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return InlineExecutionSpec{}, err
	}
	if err := ValidateCreateInvocation(CreateInvocationInput{
		AgentRef: "validation", IdempotencyKey: "validation",
		Input: InvocationInput{Content: []TextInputBlock{{Type: "text", Text: "validation"}}},
		Spec:  spec,
	}); err != nil {
		return InlineExecutionSpec{}, err
	}
	if spec.Model.Provider != strings.TrimSpace(spec.Model.Provider) ||
		spec.Model.Name != strings.TrimSpace(spec.Model.Name) {
		return InlineExecutionSpec{}, fmt.Errorf("model provider and name cannot have surrounding whitespace")
	}
	return spec, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("unexpected trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func transcriptForClaim(
	claim domain.InvocationClaim,
	stored []domain.SessionMessage,
	latest *domain.InvocationCheckpoint,
) ([]domain.GenerationMessage, error) {
	if len(stored) == 0 {
		return nil, fmt.Errorf("session transcript is empty")
	}
	messages := make([]domain.GenerationMessage, 0, len(stored))
	var previousSequence int64
	for index, message := range stored {
		if message.SessionID != claim.Invocation.SessionID ||
			message.AccountID != claim.Invocation.AccountID ||
			message.TenantPartitionID != claim.Invocation.TenantPartitionID ||
			message.AgentID != claim.Invocation.AgentID {
			return nil, fmt.Errorf("session message scope does not match Invocation")
		}
		if index > 0 && message.Sequence <= previousSequence {
			return nil, fmt.Errorf("session message sequence is not strictly increasing")
		}
		previousSequence = message.Sequence
		generationMessage := domain.GenerationMessage{Role: message.Role, Content: message.Content}
		if err := validateGenerationMessage(generationMessage, false); err != nil {
			return nil, err
		}
		messages = append(messages, generationMessage)
	}
	firstCurrent := -1
	for index, message := range stored {
		if message.InvocationID == claim.Invocation.ID {
			firstCurrent = index
			break
		}
	}
	if firstCurrent < 0 || stored[firstCurrent].Role != domain.MessageRoleUser {
		return nil, fmt.Errorf("current Invocation input is missing")
	}
	for _, message := range stored[firstCurrent:] {
		if message.InvocationID != claim.Invocation.ID {
			return nil, fmt.Errorf("current Invocation transcript is not contiguous")
		}
	}
	last := stored[len(stored)-1]
	if latest == nil {
		if firstCurrent != len(stored)-1 || last.Role != domain.MessageRoleUser {
			return nil, fmt.Errorf("uncheckpointed Invocation has durable output")
		}
	} else if latest.Sequence != claim.Invocation.CurrentCheckpointSequence ||
		latest.ThroughMessageSequence != last.Sequence || last.Role == domain.MessageRoleUser {
		return nil, fmt.Errorf("checkpoint does not cover the current transcript")
	}
	return normalizeToolResultMessages(messages)
}

func normalizeToolResultMessages(messages []domain.GenerationMessage) ([]domain.GenerationMessage, error) {
	normalized := make([]domain.GenerationMessage, 0, len(messages))
	toolOrder := map[string]int{}
	for index := 0; index < len(messages); {
		message := messages[index]
		if message.Role == domain.MessageRoleAssistant {
			order, err := assistantToolCallOrder(message.Content)
			if err != nil {
				return nil, err
			}
			toolOrder = order
			normalized = append(normalized, message)
			index++
			continue
		}
		if message.Role != domain.MessageRoleTool {
			normalized = append(normalized, message)
			index++
			continue
		}

		var blocks []json.RawMessage
		for index < len(messages) && messages[index].Role == domain.MessageRoleTool {
			var current []json.RawMessage
			if err := json.Unmarshal(messages[index].Content, &current); err != nil {
				return nil, fmt.Errorf("decode tool result content: %w", err)
			}
			blocks = append(blocks, current...)
			index++
		}
		if len(blocks) > MaxInputBlocks {
			return nil, fmt.Errorf("tool result batch exceeds %d blocks", MaxInputBlocks)
		}
		type orderedBlock struct {
			raw      json.RawMessage
			ordinal  int
			original int
		}
		ordered := make([]orderedBlock, len(blocks))
		seen := make(map[string]struct{}, len(blocks))
		for blockIndex, raw := range blocks {
			var block struct {
				ToolUseID string `json:"tool_use_id"`
			}
			if err := json.Unmarshal(raw, &block); err != nil || block.ToolUseID == "" {
				return nil, fmt.Errorf("decode tool result identity")
			}
			if _, duplicate := seen[block.ToolUseID]; duplicate {
				return nil, fmt.Errorf("duplicate tool result %q", block.ToolUseID)
			}
			seen[block.ToolUseID] = struct{}{}
			ordinal, known := toolOrder[block.ToolUseID]
			if !known {
				return nil, fmt.Errorf("tool result %q has no preceding request", block.ToolUseID)
			}
			ordered[blockIndex] = orderedBlock{
				raw:      raw,
				ordinal:  ordinal,
				original: blockIndex,
			}
		}
		sort.SliceStable(ordered, func(left, right int) bool {
			if ordered[left].ordinal == ordered[right].ordinal {
				return ordered[left].original < ordered[right].original
			}
			return ordered[left].ordinal < ordered[right].ordinal
		})
		blocks = blocks[:0]
		for _, block := range ordered {
			blocks = append(blocks, block.raw)
		}
		content, err := json.Marshal(blocks)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, domain.GenerationMessage{
			Role:    domain.MessageRoleTool,
			Content: content,
		})
		toolOrder = map[string]int{}
	}
	return normalized, nil
}

func assistantToolCallOrder(content json.RawMessage) (map[string]int, error) {
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil, err
	}
	order := make(map[string]int)
	for _, block := range blocks {
		var kind string
		if err := json.Unmarshal(block["type"], &kind); err != nil {
			return nil, err
		}
		if kind != "tool_use" {
			continue
		}
		var id string
		if err := json.Unmarshal(block["id"], &id); err != nil || id == "" {
			return nil, fmt.Errorf("assistant tool call identity is invalid")
		}
		if _, duplicate := order[id]; duplicate {
			return nil, fmt.Errorf("assistant tool call identity is duplicated")
		}
		order[id] = len(order)
	}
	return order, nil
}

func resumeBudgetExceeded(invocation domain.Invocation, resume domain.GenerationResume) string {
	if resume.Usage.Iterations >= invocation.MaxIterations {
		return "iterations"
	}
	return exceededGenerationBudget(invocation, resume.Usage)
}

func validateGenerationMessage(message domain.GenerationMessage, output bool) error {
	if message.Role != domain.MessageRoleUser && message.Role != domain.MessageRoleAssistant && message.Role != domain.MessageRoleTool {
		return fmt.Errorf("generation message role is unsupported")
	}
	if output && message.Role != domain.MessageRoleAssistant {
		return fmt.Errorf("generation output role must be assistant")
	}
	decoder := json.NewDecoder(bytes.NewReader(message.Content))
	var blocks []json.RawMessage
	if err := decoder.Decode(&blocks); err != nil {
		return fmt.Errorf("decode generation content: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode generation content: %w", err)
	}
	if len(blocks) == 0 || len(blocks) > MaxInputBlocks {
		return fmt.Errorf("generation content must contain between 1 and %d blocks", MaxInputBlocks)
	}
	visible := false
	for _, raw := range blocks {
		var block map[string]json.RawMessage
		if err := json.Unmarshal(raw, &block); err != nil || block == nil {
			return fmt.Errorf("generation content block is not an object")
		}
		var blockType string
		if err := json.Unmarshal(block["type"], &blockType); err != nil ||
			!utf8.ValidString(blockType) || strings.TrimSpace(blockType) == "" {
			return fmt.Errorf("generation content block type is invalid")
		}
		switch message.Role {
		case domain.MessageRoleUser:
			if blockType != "text" {
				return fmt.Errorf("user generation content must be text")
			}
		case domain.MessageRoleAssistant:
			switch blockType {
			case "text", "refusal":
				visible = true
			case "thinking", "redacted_thinking", "summary":
			case "tool_use":
				var id, name string
				if json.Unmarshal(block["id"], &id) != nil || strings.TrimSpace(id) == "" ||
					json.Unmarshal(block["name"], &name) != nil || strings.TrimSpace(name) == "" ||
					len(block["input"]) == 0 || !json.Valid(block["input"]) {
					return fmt.Errorf("assistant tool_use content is invalid")
				}
			default:
				return fmt.Errorf("assistant generation content type %q is unsupported", blockType)
			}
		case domain.MessageRoleTool:
			if blockType != "tool_result" {
				return fmt.Errorf("tool generation content must be tool_result")
			}
			var toolUseID string
			if json.Unmarshal(block["tool_use_id"], &toolUseID) != nil || strings.TrimSpace(toolUseID) == "" || len(block["content"]) == 0 {
				return fmt.Errorf("tool_result content is invalid")
			}
		}
		if blockType == "text" || blockType == "refusal" {
			var blockText string
			if err := json.Unmarshal(block["text"], &blockText); err != nil ||
				!utf8.ValidString(blockText) || strings.TrimSpace(blockText) == "" {
				return fmt.Errorf("visible generation content must not be blank")
			}
		}
	}
	if output && !visible {
		return fmt.Errorf("generation output contains no visible assistant content")
	}
	return nil
}

func validateModelUsage(usage domain.ModelUsage) error {
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheCreationInputTokens < 0 ||
		usage.CacheReadInputTokens < 0 || usage.ReasoningTokens < 0 || usage.Iterations < 0 {
		return fmt.Errorf("model usage token counts cannot be negative")
	}
	if usage.EstimatedCost == nil {
		return nil
	}
	cost := usage.EstimatedCost
	for _, value := range []float64{cost.Input, cost.Output, cost.CacheRead, cost.CacheWrite, cost.Total} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("model cost values must be finite and nonnegative")
		}
	}
	if utf8.RuneCountInString(cost.Currency) > MaxReferenceCharacters || utf8.RuneCountInString(cost.Model) > MaxReferenceCharacters {
		return fmt.Errorf("model cost metadata is too long")
	}
	return nil
}

func exceededGenerationBudget(invocation domain.Invocation, usage domain.ModelUsage) string {
	if usage.Iterations > invocation.MaxIterations {
		return "iterations"
	}
	if invocation.MaxOutputTokens != nil && usage.OutputTokens > *invocation.MaxOutputTokens {
		return "output_tokens"
	}
	if invocation.MaxEstimatedCostMicros != nil {
		if usage.EstimatedCost == nil || (usage.EstimatedCost.Currency != "" && !strings.EqualFold(usage.EstimatedCost.Currency, "USD")) {
			return "estimated_cost_unavailable"
		}
		micros := int64(math.Ceil(usage.EstimatedCost.Total * 1_000_000))
		if micros > *invocation.MaxEstimatedCostMicros {
			return "estimated_cost"
		}
	}
	return ""
}

func budgetGenerationFailure(kind string, usage domain.ModelUsage, provenance domain.ModelProvenance) domain.InvocationExecutionResult {
	return domain.InvocationExecutionResult{
		Status:     domain.InvocationFailed,
		Error:      invocationFailureWithDetails("budget_exceeded", "The execution budget was exceeded.", map[string]string{"kind": kind}),
		Usage:      &usage,
		Provenance: &provenance,
	}
}

func structuredOutputGenerationFailure(reason string, usage domain.ModelUsage, provenance domain.ModelProvenance) domain.InvocationExecutionResult {
	if reason != "missing" && reason != "invalid" && reason != "oversized" {
		reason = "invalid"
	}
	return domain.InvocationExecutionResult{
		Status: domain.InvocationFailed,
		Error: invocationFailureWithDetails(
			"structured_output_unsatisfied",
			"The structured output contract was not satisfied.",
			map[string]string{"reason": reason},
		),
		Usage:      &usage,
		Provenance: &provenance,
	}
}

func deadlineGenerationFailure(claim domain.InvocationClaim, usage *domain.ModelUsage, provenance *domain.ModelProvenance) domain.InvocationExecutionResult {
	scope := "execution_segment"
	if claim.Invocation.ExecutionDeadlineScope != nil {
		scope = *claim.Invocation.ExecutionDeadlineScope
	}
	return domain.InvocationExecutionResult{
		Status:     domain.InvocationFailed,
		Error:      invocationFailureWithDetails("deadline_exceeded", "The execution deadline was exceeded.", map[string]string{"scope": scope}),
		Usage:      usage,
		Provenance: provenance,
	}
}

func validateModelProvenance(provenance domain.ModelProvenance) error {
	for name, value := range map[string]string{
		"provider": provenance.Provider, "requested model": provenance.RequestedModel,
		"served model": provenance.ServedModel,
	} {
		if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > MaxReferenceCharacters {
			return fmt.Errorf("model provenance %s is invalid", name)
		}
	}
	source := domain.ProviderCredentialSource(provenance.CredentialSource)
	if !source.Valid() {
		return fmt.Errorf("model provenance credential source is invalid")
	}
	if source == domain.ProviderCredentialSourceAccountBYOK || source == domain.ProviderCredentialSourceTenantBYOK {
		if strings.TrimSpace(provenance.ProviderCredentialID) == "" || strings.TrimSpace(provenance.CredentialVersionID) == "" {
			return fmt.Errorf("model provenance reusable credential identity is invalid")
		}
	} else if provenance.ProviderCredentialID != "" || provenance.CredentialVersionID != "" {
		return fmt.Errorf("model provenance credential identity is not allowed")
	}
	return nil
}

func generationErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "provider_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "provider_deadline_exceeded"
	case errors.Is(err, ports.ErrCredentialUnavailable):
		return "credential_unavailable"
	case errors.Is(err, ports.ErrProviderUnsupported):
		return "provider_unsupported"
	case errors.Is(err, ports.ErrProviderKeyMissing):
		return "provider_key_missing"
	case errors.Is(err, ports.ErrModelResponseInvalid):
		return "invalid_provider_response"
	case errors.Is(err, ports.ErrGenerationInputInvalid):
		return "invalid_generation_input"
	case errors.Is(err, ports.ErrGenerationRecoveryInvalid):
		return "recovery_invalid"
	default:
		return "provider_call_failed"
	}
}

func internalGenerationFailure() domain.InvocationExecutionResult {
	return domain.InvocationExecutionResult{
		Status: domain.InvocationFailed,
		Error:  invocationFailure("internal", "The execution failed."),
	}
}

func providerGenerationFailure() domain.InvocationExecutionResult {
	return domain.InvocationExecutionResult{
		Status: domain.InvocationFailed,
		Error:  invocationFailure("provider_error", "The model provider could not complete the execution."),
	}
}

func generationProvenance(
	request domain.GenerationRequest,
	response domain.GenerationResponse,
	servedModel string,
) *domain.ModelProvenance {
	source := response.CredentialSource
	if source == "" {
		source = domain.ProviderCredentialSourceInstallationBYOK
	}
	return &domain.ModelProvenance{
		Provider:             request.Provider,
		RequestedModel:       request.Model,
		ServedModel:          servedModel,
		CredentialSource:     string(source),
		ProviderCredentialID: response.ProviderCredentialID,
		CredentialVersionID:  response.CredentialVersionID,
	}
}

func credentialUnavailableGenerationFailure(
	request domain.GenerationRequest,
	response domain.GenerationResponse,
) domain.InvocationExecutionResult {
	result := domain.InvocationExecutionResult{
		Status:               domain.InvocationFailed,
		MessagesCheckpointed: response.MessagesCheckpointed,
		Error:                invocationFailure("credential_unavailable", "The selected model credential is unavailable."),
	}
	if response.MessagesCheckpointed || response.Usage.Iterations > 0 || response.CredentialSource.Valid() {
		usage := response.Usage
		result.Usage = &usage
	}
	if response.CredentialSource.Valid() {
		servedModel := response.ServedModel
		if servedModel == "" {
			servedModel = request.Model
		}
		result.Provenance = generationProvenance(request, response, servedModel)
	}
	return result
}
