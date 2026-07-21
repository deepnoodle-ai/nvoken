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

// GenerationExecutor reconstructs one tool-free turn from durable state. It
// returns a desired terminal result; InvocationExecutionService alone owns the
// fenced transaction that publishes that result.
type GenerationExecutor struct {
	store     generationStore
	generator ports.ModelGenerator
	events    ports.LiveEventPublisher
	logger    *slog.Logger
}

type GenerationExecutorOption func(*GenerationExecutor)

func WithGenerationLiveEvents(events ports.LiveEventPublisher) GenerationExecutorOption {
	return func(executor *GenerationExecutor) { executor.events = events }
}

func NewGenerationExecutor(
	store generationStore,
	generator ports.ModelGenerator,
	logger *slog.Logger,
	options ...GenerationExecutorOption,
) *GenerationExecutor {
	if logger == nil {
		logger = slog.Default()
	}
	executor := &GenerationExecutor{store: store, generator: generator, logger: logger}
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
	messages, err := transcriptForClaim(claim, stored)
	if err != nil {
		e.logFailure(claim, "invalid_transcript", spec.Model.Provider, spec.Model.Name)
		return internalGenerationFailure(), nil
	}

	request := domain.GenerationRequest{
		Instructions:    spec.Instructions,
		Provider:        strings.ToLower(spec.Model.Provider),
		Model:           spec.Model.Name,
		Messages:        messages,
		MaxOutputTokens: claim.Invocation.MaxOutputTokens,
		MaxIterations:   claim.Invocation.MaxIterations,
		Claim:           &claim,
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
	if err := ctx.Err(); err != nil {
		return domain.InvocationExecutionResult{}, err
	}
	started := time.Now()
	response, err := e.generate(ctx, claim, request)
	if err != nil {
		if ctx.Err() != nil {
			return domain.InvocationExecutionResult{}, ctx.Err()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return domain.InvocationExecutionResult{}, err
		}
		class := generationErrorClass(err)
		e.logFailure(claim, class, request.Provider, request.Model)
		if errors.Is(err, ports.ErrGenerationInputInvalid) {
			return internalGenerationFailure(), nil
		}
		return providerGenerationFailure(), nil
	}
	servedModel := response.ServedModel
	if strings.TrimSpace(servedModel) == "" {
		servedModel = request.Model
	}
	result := domain.InvocationExecutionResult{
		Status:               domain.InvocationCompleted,
		AssistantMessages:    response.Messages,
		MessagesCheckpointed: response.MessagesCheckpointed,
		Usage:                &response.Usage,
		Provenance: &domain.ModelProvenance{
			Provider:         request.Provider,
			RequestedModel:   request.Model,
			ServedModel:      servedModel,
			CredentialSource: credentialSourceInstallationBYOK,
		},
		StructuredOutput: response.StructuredOutput,
	}
	if response.MessagesCheckpointed {
		result.AssistantMessages = nil
	}
	if response.BudgetExceeded != "" {
		e.logger.Warn("Model generation budget exceeded",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt, "budget_kind", response.BudgetExceeded)
		failed := budgetGenerationFailure(response.BudgetExceeded, response.Usage, *result.Provenance)
		failed.MessagesCheckpointed = response.MessagesCheckpointed
		return failed, nil
	}
	if response.StructuredOutputFailure != "" {
		failed := structuredOutputGenerationFailure(
			response.StructuredOutputFailure,
			response.Usage,
			*result.Provenance,
		)
		failed.MessagesCheckpointed = response.MessagesCheckpointed
		return failed, nil
	}
	if err := validateExecutionResult(result); err != nil {
		e.logFailure(claim, "invalid_provider_response", request.Provider, request.Model)
		return providerGenerationFailure(), nil
	}
	if ctx.Err() != nil {
		return deadlineGenerationFailure(claim, &response.Usage, result.Provenance), nil
	}
	if kind := exceededGenerationBudget(claim.Invocation, response.Usage); kind != "" {
		e.logger.Warn("Model generation budget exceeded",
			"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt, "budget_kind", kind)
		return budgetGenerationFailure(kind, response.Usage, *result.Provenance), nil
	}
	e.logger.Info("Model generation completed",
		"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt,
		"provider", request.Provider, "requested_model", request.Model,
		"served_model", servedModel, "input_tokens", response.Usage.InputTokens,
		"output_tokens", response.Usage.OutputTokens,
		"cache_creation_input_tokens", response.Usage.CacheCreationInputTokens,
		"cache_read_input_tokens", response.Usage.CacheReadInputTokens,
		"reasoning_tokens", response.Usage.ReasoningTokens,
		"generation_latency_ms", time.Since(started).Milliseconds())
	return result, nil
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
			EventType: domain.LiveEventGenerationDelta, SessionID: claim.Invocation.SessionID,
			InvocationID: claim.Invocation.ID, LeaseAttempt: claim.Attempt, DeltaSequence: sequence.Add(1),
			Delta: delta, EmittedAt: time.Now().UTC(),
		})
		if err != nil {
			e.logger.Warn("generation delta encode failed", "invocation_id", claim.Invocation.ID)
			return
		}
		e.events.Publish(ctx, ports.LiveEvent{
			Type: domain.LiveEventGenerationDelta, AccountID: claim.Invocation.AccountID,
			SessionID: claim.Invocation.SessionID, Payload: payload,
		})
	}
	return streaming.GenerateStream(ctx, request, emit)
}

func (e *GenerationExecutor) logFailure(claim domain.InvocationClaim, class, provider, model string) {
	e.logger.Warn("Model generation failed",
		"invocation_id", claim.Invocation.ID, "lease_attempt", claim.Attempt,
		"class", class, "provider", provider, "requested_model", model)
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

func transcriptForClaim(claim domain.InvocationClaim, stored []domain.SessionMessage) ([]domain.GenerationMessage, error) {
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
	last := stored[len(stored)-1]
	if last.InvocationID != claim.Invocation.ID || last.Role != domain.MessageRoleUser {
		return nil, fmt.Errorf("current Invocation input is not the final user message")
	}
	return messages, nil
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
	if provenance.CredentialSource != credentialSourceInstallationBYOK {
		return fmt.Errorf("model provenance credential source is invalid")
	}
	return nil
}

func generationErrorClass(err error) string {
	switch {
	case errors.Is(err, ports.ErrProviderUnsupported):
		return "provider_unsupported"
	case errors.Is(err, ports.ErrProviderKeyMissing):
		return "provider_key_missing"
	case errors.Is(err, ports.ErrModelResponseInvalid):
		return "invalid_provider_response"
	case errors.Is(err, ports.ErrGenerationInputInvalid):
		return "invalid_generation_input"
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
