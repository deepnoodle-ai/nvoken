package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/structuredoutput"
)

var errRecoveryInvalid = errors.New("durable recovery evidence is invalid")

type generationRecoveryStore interface {
	ports.SessionMessageRepository
	ports.ToolCallRepository
}

type generationRecovery struct {
	Resume     *domain.GenerationResume
	Latest     *domain.InvocationCheckpoint
	Final      bool
	Provenance domain.ModelProvenance
}

func loadGenerationRecovery(
	ctx context.Context,
	store generationRecoveryStore,
	invocation domain.Invocation,
	messages []domain.SessionMessage,
	output *domain.StructuredOutputRequest,
) (generationRecovery, error) {
	if invocation.CurrentCheckpointSequence == 0 {
		if invocation.CurrentIteration != 0 {
			return generationRecovery{}, errRecoveryInvalid
		}
		return generationRecovery{}, nil
	}

	checkpoints, err := store.ListInvocationCheckpoints(ctx, invocation.ID)
	if err != nil {
		return generationRecovery{}, err
	}
	receipts, err := store.ListModelUsageReceipts(ctx, invocation.ID)
	if err != nil {
		return generationRecovery{}, err
	}
	calls, err := store.ListToolCallsByInvocation(ctx, invocation.ID)
	if err != nil {
		return generationRecovery{}, err
	}
	if len(checkpoints) == 0 || len(receipts) != invocation.CurrentIteration {
		return generationRecovery{}, errRecoveryInvalid
	}

	messageByID := make(map[string]domain.SessionMessage, len(messages))
	for _, message := range messages {
		messageByID[message.ID] = message
	}
	callByID := make(map[string]domain.ToolCall, len(calls))
	callsByIteration := make(map[int][]domain.ToolCall)
	for _, call := range calls {
		if !toolCallMatchesInvocation(call, invocation) || call.Iteration > invocation.CurrentIteration {
			return generationRecovery{}, errRecoveryInvalid
		}
		callByID[call.ID] = call
		callsByIteration[call.Iteration] = append(callsByIteration[call.Iteration], call)
	}
	for iteration := range callsByIteration {
		sort.Slice(callsByIteration[iteration], func(left, right int) bool {
			return callsByIteration[iteration][left].BatchOrdinal < callsByIteration[iteration][right].BatchOrdinal
		})
	}

	modelCheckpoints := make(map[int]domain.InvocationCheckpoint, len(receipts))
	var latest domain.InvocationCheckpoint
	var previousWatermark int64
	for index, checkpoint := range checkpoints {
		if !checkpointMatchesInvocation(checkpoint, invocation) ||
			checkpoint.Sequence != int64(index+1) ||
			checkpoint.ThroughMessageSequence < previousWatermark {
			return generationRecovery{}, errRecoveryInvalid
		}
		previousWatermark = checkpoint.ThroughMessageSequence
		switch checkpoint.Kind {
		case domain.InvocationCheckpointModel:
			if checkpoint.UsageReceiptID == nil || checkpoint.ToolCallID != nil {
				return generationRecovery{}, errRecoveryInvalid
			}
			modelCheckpoints[checkpoint.Iteration] = checkpoint
		case domain.InvocationCheckpointTool:
			if checkpoint.ToolCallID == nil || checkpoint.UsageReceiptID != nil {
				return generationRecovery{}, errRecoveryInvalid
			}
			call, ok := callByID[*checkpoint.ToolCallID]
			if !ok || !call.Status.Terminal() || call.ResultMessageSequence == nil ||
				*call.ResultMessageSequence != checkpoint.ThroughMessageSequence {
				return generationRecovery{}, errRecoveryInvalid
			}
		default:
			return generationRecovery{}, errRecoveryInvalid
		}
		latest = checkpoint
	}
	if latest.Sequence != invocation.CurrentCheckpointSequence ||
		latest.Iteration != invocation.CurrentIteration {
		return generationRecovery{}, errRecoveryInvalid
	}

	var usage domain.ModelUsage
	var provenance domain.ModelProvenance
	for index, receipt := range receipts {
		iteration := index + 1
		checkpoint, ok := modelCheckpoints[iteration]
		message, messageOK := messageByID[receipt.MessageID]
		if !ok || !messageOK || !receiptMatchesInvocation(receipt, invocation) ||
			receipt.Iteration != iteration || checkpoint.UsageReceiptID == nil ||
			*checkpoint.UsageReceiptID != receipt.ID ||
			checkpoint.ThroughMessageSequence != receipt.MessageSequence ||
			message.Sequence != receipt.MessageSequence ||
			message.Role != domain.MessageRoleAssistant ||
			message.InvocationID != invocation.ID {
			return generationRecovery{}, errRecoveryInvalid
		}
		var currentUsage domain.ModelUsage
		var currentProvenance domain.ModelProvenance
		if json.Unmarshal(receipt.Usage, &currentUsage) != nil ||
			json.Unmarshal(receipt.Provenance, &currentProvenance) != nil ||
			validateModelUsage(currentUsage) != nil ||
			validateModelProvenance(currentProvenance) != nil ||
			len(receipt.EvidenceDigest) != sha256.Size {
			return generationRecovery{}, errRecoveryInvalid
		}
		if err := validateStoredToolCallBatch(message.Content, callsByIteration[iteration]); err != nil {
			return generationRecovery{}, errRecoveryInvalid
		}
		addModelUsage(&usage, currentUsage)
		provenance = currentProvenance
	}

	resume := &domain.GenerationResume{
		Iteration: invocation.CurrentIteration,
		Usage:     usage,
	}
	if output != nil {
		compiled, err := structuredoutput.CompileSchema(output.Schema)
		if err != nil || !bytes.Equal(output.SchemaDigest, invocation.OutputSchemaDigest) {
			return generationRecovery{}, errRecoveryInvalid
		}
		resume.StructuredOutput, resume.StructuredOutputFailure, err = recoveredStructuredOutput(
			ctx,
			store,
			compiled,
			invocation,
			calls,
		)
		if err != nil {
			return generationRecovery{}, err
		}
	}

	for _, call := range calls {
		if call.Status.Terminal() {
			if err := validateStoredToolResult(messageByID, call); err != nil {
				return generationRecovery{}, errRecoveryInvalid
			}
			continue
		}
		if call.Iteration != invocation.CurrentIteration ||
			call.Mode != domain.ToolCallModeBuiltin {
			return generationRecovery{}, errRecoveryInvalid
		}
		input, err := storedToolCallInput(ctx, store, call)
		if err != nil {
			return generationRecovery{}, errRecoveryInvalid
		}
		resume.OpenToolCalls = append(resume.OpenToolCalls, domain.ResumableToolCall{
			Call:  call,
			Input: input,
		})
	}

	final := latest.Kind == domain.InvocationCheckpointModel &&
		len(callsByIteration[latest.Iteration]) == 0
	if final {
		receipt := receipts[len(receipts)-1]
		message := messageByID[receipt.MessageID]
		if validateGenerationMessage(domain.GenerationMessage{
			Role:    message.Role,
			Content: message.Content,
		}, true) != nil {
			return generationRecovery{}, errRecoveryInvalid
		}
	}
	return generationRecovery{
		Resume:     resume,
		Latest:     &latest,
		Final:      final,
		Provenance: provenance,
	}, nil
}

func toolCallMatchesInvocation(call domain.ToolCall, invocation domain.Invocation) bool {
	return call.InvocationID == invocation.ID &&
		call.SessionID == invocation.SessionID &&
		call.AccountID == invocation.AccountID &&
		call.TenantPartitionID == invocation.TenantPartitionID &&
		call.AgentID == invocation.AgentID
}

func checkpointMatchesInvocation(checkpoint domain.InvocationCheckpoint, invocation domain.Invocation) bool {
	return checkpoint.InvocationID == invocation.ID &&
		checkpoint.SessionID == invocation.SessionID &&
		checkpoint.AccountID == invocation.AccountID &&
		checkpoint.TenantPartitionID == invocation.TenantPartitionID &&
		checkpoint.AgentID == invocation.AgentID &&
		checkpoint.Iteration > 0 &&
		checkpoint.Iteration <= invocation.CurrentIteration
}

func receiptMatchesInvocation(receipt domain.ModelUsageReceipt, invocation domain.Invocation) bool {
	return receipt.InvocationID == invocation.ID &&
		receipt.SessionID == invocation.SessionID &&
		receipt.AccountID == invocation.AccountID &&
		receipt.TenantPartitionID == invocation.TenantPartitionID &&
		receipt.AgentID == invocation.AgentID
}

func validateStoredToolCallBatch(content json.RawMessage, calls []domain.ToolCall) error {
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(content, &blocks) != nil {
		return errRecoveryInvalid
	}
	seen := 0
	for _, block := range blocks {
		var kind string
		_ = json.Unmarshal(block["type"], &kind)
		if kind != "tool_use" {
			continue
		}
		if seen >= len(calls) {
			return errRecoveryInvalid
		}
		call := calls[seen]
		var id string
		var name string
		var input json.RawMessage
		if json.Unmarshal(block["id"], &id) != nil ||
			json.Unmarshal(block["name"], &name) != nil ||
			json.Unmarshal(block["input"], &input) != nil ||
			id != call.ID || name != call.Name {
			return errRecoveryInvalid
		}
		digest, err := toolRequestDigest(name, call.Mode, input)
		if err != nil || !bytes.Equal(digest, call.RequestDigest) {
			return errRecoveryInvalid
		}
		seen++
	}
	if seen != len(calls) {
		return errRecoveryInvalid
	}
	return nil
}

func validateStoredToolResult(messages map[string]domain.SessionMessage, call domain.ToolCall) error {
	if call.ResultMessageID == nil || call.ResultMessageSequence == nil {
		return errRecoveryInvalid
	}
	message, ok := messages[*call.ResultMessageID]
	if !ok || message.Sequence != *call.ResultMessageSequence ||
		message.Role != domain.MessageRoleTool || message.InvocationID != call.InvocationID {
		return errRecoveryInvalid
	}
	var blocks []struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
	}
	if json.Unmarshal(message.Content, &blocks) != nil {
		return errRecoveryInvalid
	}
	for _, block := range blocks {
		if block.Type == "tool_result" && block.ToolUseID == call.ID {
			return nil
		}
	}
	return errRecoveryInvalid
}

func recoveredStructuredOutput(
	ctx context.Context,
	store generationRecoveryStore,
	compiled *structuredoutput.Compiled,
	invocation domain.Invocation,
	calls []domain.ToolCall,
) (*domain.StructuredOutput, string, error) {
	failure := "missing"
	for _, call := range calls {
		if call.Name != structuredoutput.ReservedToolName || call.Mode != domain.ToolCallModeBuiltin {
			continue
		}
		input, err := storedToolCallInput(ctx, store, call)
		if err != nil {
			return nil, "", err
		}
		if call.Status == domain.ToolCallFailed {
			failure = "invalid"
			if len(input) > structuredoutput.MaxValueBytes {
				failure = "oversized"
			}
			continue
		}
		if call.Status != domain.ToolCallCompleted || compiled.ValidateValue(input) != nil {
			continue
		}
		return &domain.StructuredOutput{
			Value: append(json.RawMessage(nil), input...),
			Provenance: domain.StructuredOutputProvenance{
				Source:       structuredoutput.ProvenanceSource,
				ToolCallID:   call.ID,
				SchemaSHA256: hex.EncodeToString(invocation.OutputSchemaDigest),
			},
		}, "", nil
	}
	return nil, failure, nil
}
