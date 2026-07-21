// Package cloudtasks adapts Google Cloud Tasks to execution dispatch delivery.
package cloudtasks

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	cloudtaskslib "cloud.google.com/go/cloudtasks/apiv2"
	"cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type Config struct {
	Queue              string
	ExecutorURL        string
	OIDCServiceAccount string
	OIDCAudience       string
	DispatchDeadline   time.Duration
}

func ValidateConfig(cfg Config) error {
	if !strings.HasPrefix(cfg.Queue, "projects/") || !strings.Contains(cfg.Queue, "/locations/") || !strings.Contains(cfg.Queue, "/queues/") {
		return fmt.Errorf("Cloud Tasks queue must be a fully qualified queue name")
	}
	target, err := url.Parse(cfg.ExecutorURL)
	if err != nil || target.Scheme != "https" || target.Host == "" || (target.Path != "" && target.Path != "/") || target.RawQuery != "" || target.Fragment != "" {
		return fmt.Errorf("Cloud Tasks executor URL must be an HTTPS service root without path, query, or fragment")
	}
	if strings.TrimSpace(cfg.OIDCServiceAccount) == "" || strings.TrimSpace(cfg.OIDCAudience) == "" {
		return fmt.Errorf("Cloud Tasks OIDC service account and audience are required")
	}
	if cfg.DispatchDeadline <= 0 || cfg.DispatchDeadline > 30*time.Minute {
		return fmt.Errorf("Cloud Tasks dispatch deadline must be positive and at most 30 minutes")
	}
	return nil
}

type client interface {
	CreateTask(context.Context, *cloudtaskspb.CreateTaskRequest, ...gax.CallOption) (*cloudtaskspb.Task, error)
	GetTask(context.Context, *cloudtaskspb.GetTaskRequest, ...gax.CallOption) (*cloudtaskspb.Task, error)
	Close() error
}

type Queue struct {
	client client
	config Config
}

var _ ports.ExecutionTaskQueue = (*Queue)(nil)

func New(ctx context.Context, cfg Config, opts ...option.ClientOption) (*Queue, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	client, err := cloudtaskslib.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create Cloud Tasks client: %w", err)
	}
	return &Queue{client: client, config: cfg}, nil
}

func newWithClient(cfg Config, client client) (*Queue, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("Cloud Tasks client is required")
	}
	return &Queue{client: client, config: cfg}, nil
}

func (q *Queue) CreateTask(ctx context.Context, task ports.ExecutionTask) (string, error) {
	if task.DispatchID == "" {
		return "", fmt.Errorf("dispatch ID is required")
	}
	taskName := q.config.Queue + "/tasks/" + task.DispatchID
	target := strings.TrimRight(q.config.ExecutorURL, "/") + "/internal/execution-dispatches/" + url.PathEscape(task.DispatchID) + "/attempts"
	created, err := q.client.CreateTask(ctx, &cloudtaskspb.CreateTaskRequest{
		Parent: q.config.Queue,
		Task: &cloudtaskspb.Task{
			Name: taskName,
			MessageType: &cloudtaskspb.Task_HttpRequest{HttpRequest: &cloudtaskspb.HttpRequest{
				HttpMethod: cloudtaskspb.HttpMethod_POST,
				Url:        target,
				Headers:    map[string]string{"Content-Type": "application/octet-stream"},
				AuthorizationHeader: &cloudtaskspb.HttpRequest_OidcToken{OidcToken: &cloudtaskspb.OidcToken{
					ServiceAccountEmail: q.config.OIDCServiceAccount,
					Audience:            q.config.OIDCAudience,
				}},
			}},
			ScheduleTime:     timestamppb.New(task.AvailableAt),
			DispatchDeadline: durationpb.New(q.config.DispatchDeadline),
		},
	})
	if status.Code(err) == codes.AlreadyExists {
		return taskName, fmt.Errorf("%w: %v", ports.ErrTaskAlreadyExists, err)
	}
	if err != nil {
		return "", fmt.Errorf("create Cloud Task: %w", err)
	}
	if created.GetName() == "" {
		return "", fmt.Errorf("Cloud Tasks returned an empty task name")
	}
	return created.GetName(), nil
}

func (q *Queue) TaskExists(ctx context.Context, taskName string) (bool, error) {
	if !strings.HasPrefix(taskName, q.config.Queue+"/tasks/") {
		return false, fmt.Errorf("task name is outside the configured queue")
	}
	_, err := q.client.GetTask(ctx, &cloudtaskspb.GetTaskRequest{Name: taskName})
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get Cloud Task: %w", err)
	}
	return true, nil
}

func (q *Queue) Close() error {
	return q.client.Close()
}
