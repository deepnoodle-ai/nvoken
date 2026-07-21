package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	dispatchruntime "github.com/deepnoodle-ai/nvoken/internal/dispatch"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
	"github.com/deepnoodle-ai/nvoken/internal/structuredoutput"
)

type structuredCheckpointGenerator struct {
	coordinator ports.ToolCallCoordinator
}

func (g structuredCheckpointGenerator) Generate(
	ctx context.Context,
	request domain.GenerationRequest,
) (domain.GenerationResponse, error) {
	claim := *request.Claim
	value := json.RawMessage(`{"answer":"yes"}`)
	first, err := g.coordinator.RecordModelCheckpoint(ctx, claim, domain.ModelCheckpointInput{
		Iteration: 1,
		Message: domain.GenerationMessage{
			Role: domain.MessageRoleAssistant,
			Content: json.RawMessage(
				`[{"type":"tool_use","id":"structured-provider-call","name":"nvoken_submit_output","input":{"answer":"yes"}}]`,
			),
		},
		Usage: domain.ModelUsage{
			InputTokens:  3,
			OutputTokens: 1,
			Iterations:   1,
		},
		Provenance: testModelProvenance(),
		ToolCalls: []domain.ToolCallRequest{
			{
				ProviderCallID: "structured-provider-call",
				Name:           structuredoutput.ReservedToolName,
				Mode:           domain.ToolCallModeBuiltin,
				Input:          value,
			},
		},
	})
	if err != nil {
		return domain.GenerationResponse{}, err
	}
	running, err := g.coordinator.StartBuiltinToolCall(ctx, claim, 1, "structured-provider-call")
	if err != nil {
		return domain.GenerationResponse{}, err
	}
	accepted, err := g.coordinator.AcceptBuiltinToolResult(
		ctx,
		claim,
		running,
		json.RawMessage(`"Output accepted."`),
		false,
	)
	if err != nil {
		return domain.GenerationResponse{}, err
	}
	if accepted.ID != first.ToolCalls[0].ID {
		return domain.GenerationResponse{}, ports.ErrToolCallConflict
	}
	if _, err := g.coordinator.RecordModelCheckpoint(ctx, claim, domain.ModelCheckpointInput{
		Iteration: 2,
		Message: domain.GenerationMessage{
			Role:    domain.MessageRoleAssistant,
			Content: json.RawMessage(`[{"type":"text","text":"done"}]`),
		},
		Usage: domain.ModelUsage{
			InputTokens:  5,
			OutputTokens: 2,
			Iterations:   1,
		},
		Provenance: testModelProvenance(),
	}); err != nil {
		return domain.GenerationResponse{}, err
	}
	return domain.GenerationResponse{
		Usage: domain.ModelUsage{
			InputTokens:  8,
			OutputTokens: 3,
			Iterations:   2,
		},
		ServedModel:          "test-model-served",
		MessagesCheckpointed: true,
		StructuredOutput: &domain.StructuredOutput{
			Value: value,
			Provenance: domain.StructuredOutputProvenance{
				Source:       structuredoutput.ProvenanceSource,
				ToolCallID:   accepted.ID,
				SchemaSHA256: hex.EncodeToString(request.StructuredOutput.SchemaDigest),
			},
		},
	}, nil
}

func TestStructuredOutputRunsThroughEmbeddedAndExactDispatchExecution(t *testing.T) {
	t.Run("embedded", func(t *testing.T) {
		pool, runtime, store, auth := newRuntimeFixture(t)
		ctx := context.Background()
		ack, err := runtime.Admit(ctx, auth, structuredRuntimeInput())
		if err != nil {
			t.Fatalf("admit: %v", err)
		}
		clock := identity.SystemClock{}
		ids := identity.NewUUIDv7Generator(clock)
		txm := NewTransactionManager(pool)
		ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
		claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "structured-embedded-owner", time.Minute)
		if err != nil || disposition != services.Claimed {
			t.Fatalf("claim disposition = %q, error = %v", disposition, err)
		}
		coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
		executor := services.NewGenerationExecutor(
			store,
			structuredCheckpointGenerator{
				coordinator: coordinator,
			},
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)
		result, err := executor.Execute(ctx, claim)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if err := ownership.Settle(ctx, claim, result); err != nil {
			t.Fatalf("settle: %v", err)
		}
		assertStructuredOutputTrace(t, pool, runtime, store, auth, ack)
	})

	t.Run("exact dispatch", func(t *testing.T) {
		pool, _ := testDatabase(t, true)
		ctx := context.Background()
		store := NewStore(pool)
		txm := NewTransactionManager(pool)
		clock := identity.SystemClock{}
		ids := identity.NewUUIDv7Generator(clock)
		account, err := services.BootstrapInstallation(ctx, store, txm, clock, ids)
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		runtime := services.NewRuntimeService(
			store,
			txm,
			clock,
			ids,
			services.WithInvocationExecutionMode(
				services.InvocationExecutionCloudTasks,
				services.DefaultExecutionDispatchQueue,
			),
		)
		auth := runtimeAuth(account.ID)
		ack, err := runtime.Admit(ctx, auth, structuredRuntimeInput())
		if err != nil {
			t.Fatalf("admit: %v", err)
		}
		dispatches := newDispatchServiceForInvocationTest(t, store, txm, clock, ids)
		dispatch := activeInvocationDispatch(t, store, ack.InvocationID)
		ownership := services.NewInvocationExecutionService(
			store,
			txm,
			clock,
			ids,
			services.WithExecutionSegmentCeiling(time.Second),
		)
		coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
		executor := services.NewGenerationExecutor(
			store,
			structuredCheckpointGenerator{
				coordinator: coordinator,
			},
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)
		attempts, err := dispatchruntime.NewAttemptService(
			dispatches,
			ownership,
			executor,
			store,
			txm,
			clock,
			"structured-exact-owner",
			dispatchEngineConfig(),
			nil,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)
		if err != nil {
			t.Fatalf("configure attempt service: %v", err)
		}
		outcome, err := attempts.Attempt(ctx, dispatch.ID)
		if err != nil || outcome != services.DispatchAttemptSettled {
			t.Fatalf("attempt outcome = %q, error = %v", outcome, err)
		}
		assertStructuredOutputTrace(t, pool, runtime, store, auth, ack)
	})
}

func TestStructuredOutputSettlementFaultRollsBackProjection(t *testing.T) {
	for _, stage := range []string{"invocation_update", "lifecycle_append"} {
		t.Run(stage, func(t *testing.T) {
			pool, runtime, store, auth := newRuntimeFixture(t)
			ctx := context.Background()
			ack, err := runtime.Admit(ctx, auth, structuredRuntimeInput())
			if err != nil {
				t.Fatalf("admit: %v", err)
			}
			clock := identity.SystemClock{}
			ids := identity.NewUUIDv7Generator(clock)
			txm := NewTransactionManager(pool)
			ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
			claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "structured-fault-owner", time.Minute)
			if err != nil || disposition != services.Claimed {
				t.Fatalf("claim disposition = %q, error = %v", disposition, err)
			}
			coordinator := services.NewToolCheckpointService(store, txm, clock, ids)
			executor := services.NewGenerationExecutor(
				store,
				structuredCheckpointGenerator{
					coordinator: coordinator,
				},
				slog.New(slog.NewTextHandler(io.Discard, nil)),
			)
			result, err := executor.Execute(ctx, claim)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			faults := &faultingExecutionStore{
				Store: store,
			}
			if stage == "invocation_update" {
				faults.failSettlement = true
			} else {
				faults.failStatus = domain.InvocationCompleted
			}
			faulty := services.NewInvocationExecutionService(faults, txm, clock, ids)
			if err := faulty.Settle(ctx, claim, result); err == nil {
				t.Fatal("faulted structured settlement succeeded")
			}
			invocation, err := store.GetInvocation(ctx, ack.InvocationID)
			if err != nil {
				t.Fatalf("read faulted Invocation: %v", err)
			}
			if invocation.Status != domain.InvocationRunning ||
				len(invocation.Output) != 0 ||
				len(invocation.OutputProvenance) != 0 {
				t.Fatalf("faulted Invocation = %#v", invocation)
			}
		})
	}
}

func TestInvalidStructuredOutputAdmissionCreatesNoRuntimeRows(t *testing.T) {
	pool, runtime, _, auth := newRuntimeFixture(t)
	input := structuredRuntimeInput()
	input.Spec.Output.Schema = json.RawMessage(`{"type":"object","$ref":"#/$defs/result"}`)
	if _, err := runtime.Admit(context.Background(), auth, input); err == nil {
		t.Fatal("invalid structured-output schema was admitted")
	}
	for _, table := range []string{
		"agents",
		"sessions",
		"execution_spec_snapshots",
		"session_messages",
		"invocations",
		"invocation_states",
	} {
		var count int
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows after invalid admission = %d", table, count)
		}
	}
}

func TestStructuredOutputDatabaseConstraintMatrix(t *testing.T) {
	for _, test := range []struct {
		name   string
		update func(domain.Invocation) (string, []any)
	}{
		{
			name: "output on queued Invocation",
			update: func(invocation domain.Invocation) (string, []any) {
				provenance := validOutputProvenance(invocation.OutputSchemaDigest)
				return `UPDATE invocations
SET output = $2::jsonb, output_provenance = $3::jsonb
WHERE id = $1`, []any{
						invocation.ID,
						json.RawMessage(`{"answer":"yes"}`),
						provenance,
					}
			},
		},
		{
			name: "completed schema Invocation without output",
			update: func(invocation domain.Invocation) (string, []any) {
				return `UPDATE invocations
SET status = 'completed', completed_at = now(), updated_at = now()
WHERE id = $1`, []any{
						invocation.ID,
					}
			},
		},
		{
			name: "mismatched output provenance",
			update: func(invocation domain.Invocation) (string, []any) {
				mismatched := validOutputProvenance(make([]byte, 32))
				return `UPDATE invocations
SET status = 'completed', completed_at = now(), updated_at = now(),
    output = $2::jsonb, output_provenance = $3::jsonb
WHERE id = $1`, []any{
						invocation.ID,
						json.RawMessage(`{"answer":"yes"}`),
						mismatched,
					}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool, runtime, store, auth := newRuntimeFixture(t)
			ack, err := runtime.Admit(context.Background(), auth, structuredRuntimeInput())
			if err != nil {
				t.Fatalf("admit: %v", err)
			}
			invocation, err := store.GetInvocation(context.Background(), ack.InvocationID)
			if err != nil {
				t.Fatalf("read Invocation: %v", err)
			}
			query, args := test.update(invocation)
			if _, err := pool.Exec(context.Background(), query, args...); err == nil {
				t.Fatal("invalid output database shape was accepted")
			}
		})
	}
}

func validOutputProvenance(schemaDigest []byte) json.RawMessage {
	payload, _ := json.Marshal(domain.StructuredOutputProvenance{
		Source:       structuredoutput.ProvenanceSource,
		ToolCallID:   "tcal_019f84a5-7838-7b57-a180-000000000001",
		SchemaSHA256: hex.EncodeToString(schemaDigest),
	})
	return payload
}

func structuredRuntimeInput() services.CreateInvocationInput {
	input := runtimeInput()
	input.Spec.Output = &services.StructuredOutputSpec{
		Schema: json.RawMessage(
			`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`,
		),
	}
	return input
}

func assertStructuredOutputTrace(
	t *testing.T,
	pool *pgxpool.Pool,
	runtime *services.RuntimeService,
	store *Store,
	auth domain.RuntimeAuthContext,
	ack services.InvocationAcknowledgement,
) {
	t.Helper()
	ctx := context.Background()
	invocation, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil {
		t.Fatalf("read Invocation: %v", err)
	}
	if invocation.Status != domain.InvocationCompleted || invocation.FingerprintVersion != 3 ||
		len(invocation.OutputSchemaDigest) != 32 || invocation.MaxIterations != 3 ||
		!jsonObjectEqual(invocation.Output, json.RawMessage(`{"answer":"yes"}`)) {
		t.Fatalf("completed structured Invocation = %#v", invocation)
	}
	var provenance domain.StructuredOutputProvenance
	if err := json.Unmarshal(invocation.OutputProvenance, &provenance); err != nil {
		t.Fatalf("decode output provenance: %v", err)
	}
	if provenance.Source != structuredoutput.ProvenanceSource ||
		provenance.SchemaSHA256 != hex.EncodeToString(invocation.OutputSchemaDigest) {
		t.Fatalf("output provenance = %#v", provenance)
	}
	call, err := store.GetToolCall(ctx, provenance.ToolCallID)
	if err != nil || call.Status != domain.ToolCallCompleted || call.Name != structuredoutput.ReservedToolName {
		t.Fatalf("accepted output ToolCall = %#v, error = %v", call, err)
	}
	messages, err := store.ListSessionMessages(ctx, ack.SessionID)
	if err != nil || len(messages) != 4 ||
		messages[1].Role != domain.MessageRoleAssistant ||
		messages[2].Role != domain.MessageRoleTool ||
		messages[3].Role != domain.MessageRoleAssistant {
		t.Fatalf("structured transcript = %#v, error = %v", messages, err)
	}
	changes, err := store.ListInvocationLifecycleChanges(ctx, ack.SessionID, 0, 100, 100)
	if err != nil || len(changes) < 2 {
		t.Fatalf("structured lifecycle = %#v, error = %v", changes, err)
	}
	terminal := changes[len(changes)-1]
	if terminal.Status != domain.InvocationCompleted ||
		!jsonObjectEqual(terminal.Output, invocation.Output) ||
		!jsonObjectEqual(terminal.OutputProvenance, invocation.OutputProvenance) {
		t.Fatalf("structured lifecycle = %#v, error = %v", changes, err)
	}
	read, err := runtime.GetInvocation(ctx, auth, ack.InvocationID)
	if err != nil || !jsonObjectEqual(read.Output, invocation.Output) ||
		!jsonObjectEqual(read.OutputProvenance, invocation.OutputProvenance) {
		t.Fatalf("structured read = %#v, error = %v", read, err)
	}
	listed, err := runtime.ListInvocations(ctx, auth, services.InvocationListInput{
		Limit: 100,
	})
	if err != nil || len(listed.Items) != 1 ||
		!jsonObjectEqual(listed.Items[0].Output, invocation.Output) ||
		!jsonObjectEqual(listed.Items[0].OutputProvenance, invocation.OutputProvenance) {
		t.Fatalf("structured list = %#v, error = %v", listed, err)
	}
	snapshot, err := runtime.GetSessionTranscript(ctx, auth, ack.SessionID, services.TranscriptInput{
		Limit: 100,
	})
	if err != nil || len(snapshot.Messages) != 4 || !snapshot.HasMore || snapshot.NextPageToken == nil {
		t.Fatalf("structured transcript messages = %#v, error = %v", snapshot, err)
	}
	snapshot, err = runtime.GetSessionTranscript(ctx, auth, ack.SessionID, services.TranscriptInput{
		PageToken: *snapshot.NextPageToken,
		Limit:     100,
	})
	if err != nil || len(snapshot.InvocationChanges) == 0 {
		t.Fatalf("structured transcript lifecycle = %#v, error = %v", snapshot, err)
	}
	transcriptTerminal := snapshot.InvocationChanges[len(snapshot.InvocationChanges)-1]
	if !jsonObjectEqual(transcriptTerminal.Output, invocation.Output) ||
		!jsonObjectEqual(transcriptTerminal.OutputProvenance, invocation.OutputProvenance) {
		t.Fatalf("structured transcript terminal = %#v", transcriptTerminal)
	}
	restartClock := identity.SystemClock{}
	restarted := services.NewRuntimeService(
		store,
		NewTransactionManager(pool),
		restartClock,
		identity.NewUUIDv7Generator(restartClock),
	)
	restartedRead, err := restarted.GetInvocation(ctx, auth, ack.InvocationID)
	if err != nil || !jsonObjectEqual(restartedRead.Output, invocation.Output) ||
		!jsonObjectEqual(restartedRead.OutputProvenance, invocation.OutputProvenance) {
		t.Fatalf("restart structured read = %#v, error = %v", restartedRead, err)
	}
	if _, err := pool.Exec(
		ctx,
		"UPDATE invocations SET output = '{\"answer\":\"changed\"}'::jsonb WHERE id = $1",
		ack.InvocationID,
	); err == nil {
		t.Fatal("terminal structured output mutation succeeded")
	}
}

func jsonObjectEqual(left, right []byte) bool {
	var leftValue any
	var rightValue any
	return json.Unmarshal(left, &leftValue) == nil &&
		json.Unmarshal(right, &rightValue) == nil &&
		jsonEqualValues(leftValue, rightValue)
}

func jsonEqualValues(left, right any) bool {
	leftPayload, leftErr := json.Marshal(left)
	rightPayload, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftPayload) == string(rightPayload)
}
