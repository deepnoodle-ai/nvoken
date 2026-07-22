package callback

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type failingDeliveryService struct {
	cancel context.CancelFunc
}

func (s *failingDeliveryService) ClaimNext(
	context.Context,
	string,
) (domain.CallbackDeliveryClaim, bool, error) {
	s.cancel()
	return domain.CallbackDeliveryClaim{}, false, errors.New("secret callback failure")
}

func (*failingDeliveryService) ProcessClaim(
	context.Context,
	ports.CallbackTransport,
	domain.CallbackDeliveryClaim,
) error {
	return nil
}

func (*failingDeliveryService) RecoverExpired(context.Context) (int64, error) {
	return 0, nil
}

func (*failingDeliveryService) Prune(context.Context) (int64, error) {
	return 0, nil
}

type stubCallbackTransport struct{}

func (stubCallbackTransport) Send(
	context.Context,
	ports.CallbackTransportRequest,
) ports.CallbackTransportResult {
	return ports.CallbackTransportResult{}
}

func TestControllerLogsBoundedCallbackClaimFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	controller, err := NewController(
		"executor",
		&failingDeliveryService{cancel: cancel},
		stubCallbackTransport{},
		logger,
		Config{
			Concurrency:       1,
			PollInterval:      time.Hour,
			RecoveryInterval:  time.Hour,
			RetentionInterval: time.Hour,
			DrainGrace:        time.Second,
		},
	)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	if err := controller.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	logText := logs.String()
	if !strings.Contains(logText, `"event":"callback_claim_failed"`) {
		t.Fatalf("log = %s, want callback claim event", logText)
	}
	if !strings.Contains(logText, `"error_class":"internal"`) {
		t.Fatalf("log = %s, want bounded error class", logText)
	}
	if strings.Contains(logText, "secret callback failure") {
		t.Fatalf("log = %s, want raw error redacted", logText)
	}
}
