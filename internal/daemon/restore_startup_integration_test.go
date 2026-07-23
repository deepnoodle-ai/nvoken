package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/engine"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

func TestTerminalOnlyRestoreStartsDaemonAndReadsWithoutExecution(t *testing.T) {
	databaseURL := diagnosticTestDatabase(t, true)
	invocationID := seedTerminalRestoreFixture(t, databaseURL)
	port := reserveRestoreTestPort(t)
	runtimeKey := "restore-runtime-key-0123456789abcdef"

	engineConfig := engine.DefaultConfig()
	engineConfig.Concurrency = 1
	engineConfig.PollInterval = 10 * time.Millisecond
	engineConfig.DrainGrace = time.Second
	cfg := Config{
		BuildVersion:            "restore-test",
		Port:                    port,
		DatabaseURL:             databaseURL,
		DatabaseMaxConns:        4,
		RuntimeAPIKey:           runtimeKey,
		BootstrapOwnerSecret:    "restore-owner-secret-0123456789abcdef",
		CredentialDeliveryKey:   make([]byte, 32),
		PublicBaseURL:           "http://127.0.0.1:" + port,
		ShutdownTimeout:         3 * time.Second,
		ProcessRole:             ProcessRoleCombined,
		InvocationExecutionMode: services.InvocationExecutionEmbedded,
		Engine:                  engineConfig,
		Limits:                  services.DefaultLimitPolicy(),
		LiveEventBuffer:         8,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	stopped := false
	go func() { done <- Run(ctx, cfg) }()
	t.Cleanup(func() {
		if stopped {
			return
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("stop restore test daemon: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("restore test daemon did not stop")
		}
	})

	baseURL := "http://127.0.0.1:" + port
	waitForRestoreDaemon(t, baseURL+"/health")
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/invocations/"+invocationID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+runtimeKey)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("read terminal Invocation from restored daemon: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("terminal restore read status = %d, body = %s", response.StatusCode, body)
	}
	var result struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode terminal restore read: %v", err)
	}
	if result.ID != invocationID || result.Status != string(domain.InvocationCompleted) {
		t.Fatalf("terminal restore read = %#v", result)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("restore test daemon returned: %v", err)
	}
	stopped = true
}

func seedTerminalRestoreFixture(t *testing.T, databaseURL string) string {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.OpenPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open terminal restore fixture: %v", err)
	}
	defer pool.Close()
	store := postgres.NewStore(pool)
	txm := postgres.NewTransactionManager(pool)
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(ctx, store, txm, clock, ids)
	if err != nil {
		t.Fatalf("bootstrap terminal restore fixture: %v", err)
	}
	runtime := services.NewRuntimeService(store, txm, clock, ids)
	auth := domain.RuntimeAuthContext{
		AccountID: account.ID,
		Operations: map[domain.RuntimeOperation]struct{}{
			domain.OperationCreateInvocation: {},
			domain.OperationGetInvocation:    {},
		},
	}
	ack, err := runtime.Admit(ctx, auth, services.CreateInvocationInput{
		AgentKey:       "restore-test",
		SessionKey:     restoreStringPointer("terminal-only"),
		IdempotencyKey: "terminal-only",
		Input: services.InvocationInput{
			Content: []services.TextInputBlock{
				{Type: "text", Text: "terminal restore fixture"},
			},
		},
		Spec: services.InlineExecutionSpec{
			Instructions: "terminal restore fixture",
			Model: services.ModelSelection{
				Provider: "anthropic",
				ID:       "test-model",
			},
		},
	})
	if err != nil {
		t.Fatalf("admit terminal restore fixture: %v", err)
	}
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	claim, disposition, err := ownership.ClaimExact(ctx, ack.InvocationID, "terminal-restore", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim terminal restore fixture: disposition = %s, error = %v", disposition, err)
	}
	usage := domain.ModelUsage{InputTokens: 1, OutputTokens: 1, Iterations: 1}
	provenance := domain.ModelProvenance{
		Provider:         "anthropic",
		RequestedModel:   "test-model",
		ServedModel:      "test-model",
		CredentialSource: "installation_byok",
	}
	if err := ownership.Settle(ctx, claim, domain.InvocationExecutionResult{
		Status: domain.InvocationCompleted,
		AssistantMessages: []domain.GenerationMessage{
			{
				Role:    domain.MessageRoleAssistant,
				Content: json.RawMessage(`[{"type":"text","text":"complete"}]`),
			},
		},
		Usage:      &usage,
		Provenance: &provenance,
	}); err != nil {
		t.Fatalf("settle terminal restore fixture: %v", err)
	}
	return ack.InvocationID
}

func reserveRestoreTestPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(port)
}

func waitForRestoreDaemon(t *testing.T, healthURL string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(healthURL)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("restore test daemon did not become healthy at %s", healthURL)
}

func restoreStringPointer(value string) *string { return &value }
