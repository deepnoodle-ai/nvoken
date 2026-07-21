package daemon

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type componentFunc func(context.Context) error

func (f componentFunc) Run(ctx context.Context) error { return f(ctx) }

func TestRunComponentsCancelsAndJoinsSibling(t *testing.T) {
	wantErr := errors.New("server failed")
	siblingJoined := make(chan struct{})
	var cancelled atomic.Bool
	allJoined, err := runComponents(context.Background(), time.Second,
		componentFunc(func(context.Context) error { return wantErr }),
		componentFunc(func(ctx context.Context) error {
			<-ctx.Done()
			cancelled.Store(true)
			close(siblingJoined)
			return nil
		}),
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runComponents error = %v", err)
	}
	if !allJoined {
		t.Fatal("components were not joined")
	}
	if !cancelled.Load() {
		t.Fatal("sibling was not cancelled and joined")
	}
	select {
	case <-siblingJoined:
	default:
		t.Fatal("sibling did not finish before return")
	}
}

func TestRunComponentsTreatsParentCancellationAsCleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	component := componentFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	joined, err := runComponents(ctx, time.Second, component, component)
	if err != nil {
		t.Fatalf("runComponents cancellation error = %v", err)
	}
	if !joined {
		t.Fatal("cancelled components were not joined")
	}
}

func TestRunComponentsBoundsUncooperativeShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	done := make(chan struct{})
	component := componentFunc(func(context.Context) error {
		defer close(done)
		<-release
		return nil
	})
	cancel()

	started := time.Now()
	joined, err := runComponents(ctx, 20*time.Millisecond, component)
	if err != nil {
		t.Fatalf("cancelled timeout error = %v", err)
	}
	if joined {
		t.Fatal("uncooperative component reported joined")
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > time.Second {
		t.Fatalf("shutdown elapsed = %s", elapsed)
	}
	close(release)
	<-done
}

func TestExecutionOwnerIsUniqueAndBounded(t *testing.T) {
	first, err := executionOwner()
	if err != nil {
		t.Fatal(err)
	}
	second, err := executionOwner()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || strings.TrimSpace(first) == "" {
		t.Fatalf("owners are not unique: %q and %q", first, second)
	}
	if len(first) > 255 {
		t.Fatalf("owner is %d bytes, want at most 255", len(first))
	}
}

func TestRuntimeTopologySeparatesSchedulersAndSurfaces(t *testing.T) {
	tests := []struct {
		name       string
		role       ProcessRole
		mode       services.InvocationExecutionMode
		cloudTasks bool
		want       runtimeTopology
	}{
		{
			name: "embedded combined", role: ProcessRoleCombined, mode: services.InvocationExecutionEmbedded,
			want: runtimeTopology{publicAPI: true, embeddedRunner: true},
		},
		{
			name: "embedded with synthetic dispatch control", role: ProcessRoleCombined, mode: services.InvocationExecutionEmbedded, cloudTasks: true,
			want: runtimeTopology{publicAPI: true, embeddedRunner: true, dispatchControl: true},
		},
		{
			name: "cloud tasks combined", role: ProcessRoleCombined, mode: services.InvocationExecutionCloudTasks, cloudTasks: true,
			want: runtimeTopology{publicAPI: true, reaper: true, dispatchControl: true},
		},
		{
			name: "private executor", role: ProcessRoleExecutor, mode: services.InvocationExecutionCloudTasks,
			want: runtimeTopology{privateExecutor: true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveRuntimeTopology(test.role, test.mode, test.cloudTasks)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("topology = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestRuntimeTopologyRejectsCloudModeWithoutDispatchControl(t *testing.T) {
	_, err := resolveRuntimeTopology(ProcessRoleCombined, services.InvocationExecutionCloudTasks, false)
	if err == nil || !strings.Contains(err.Error(), "requires Cloud Tasks") {
		t.Fatalf("topology error = %v", err)
	}
}
