package cloudtasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

func TestCreateTaskUsesDeterministicIDOnlyOIDCRequest(t *testing.T) {
	client := &fakeClient{}
	queue, err := newWithClient(testConfig(), client)
	if err != nil {
		t.Fatal(err)
	}
	availableAt := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	name, err := queue.CreateTask(context.Background(), ports.ExecutionTask{
		DispatchID: "dsp_019b0a12-0000-7000-8000-000000000001", AvailableAt: availableAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantName := testConfig().Queue + "/tasks/dsp_019b0a12-0000-7000-8000-000000000001"
	if name != wantName || client.created == nil || client.created.Parent != testConfig().Queue {
		t.Fatalf("name/request = %q / %#v", name, client.created)
	}
	task := client.created.Task
	request := task.GetHttpRequest()
	if task.Name != wantName || request.GetUrl() != testConfig().ExecutorURL+"/internal/execution-dispatches/dsp_019b0a12-0000-7000-8000-000000000001/attempts" {
		t.Fatalf("task target = %#v", task)
	}
	if len(request.GetBody()) != 0 || request.GetOidcToken().GetServiceAccountEmail() != testConfig().OIDCServiceAccount || request.GetOidcToken().GetAudience() != testConfig().OIDCAudience {
		t.Fatalf("task request authority/body = %#v", request)
	}
	if !task.GetScheduleTime().AsTime().Equal(availableAt) || task.GetDispatchDeadline().AsDuration() != 30*time.Minute {
		t.Fatalf("task timing = %#v", task)
	}
}

func TestCreateTaskAlreadyExistsAndGetNotFoundConverge(t *testing.T) {
	client := &fakeClient{createErr: status.Error(codes.AlreadyExists, "duplicate")}
	queue, err := newWithClient(testConfig(), client)
	if err != nil {
		t.Fatal(err)
	}
	name, err := queue.CreateTask(context.Background(), ports.ExecutionTask{DispatchID: "dsp_test", AvailableAt: time.Now()})
	if !errors.Is(err, ports.ErrTaskAlreadyExists) || name == "" {
		t.Fatalf("CreateTask = %q, %v", name, err)
	}
	client.getErr = status.Error(codes.NotFound, "gone")
	exists, err := queue.TaskExists(context.Background(), name)
	if err != nil || exists {
		t.Fatalf("TaskExists = %v, %v", exists, err)
	}
}

func testConfig() Config {
	return Config{
		Queue:              "projects/test/locations/us-central1/queues/execution",
		ExecutorURL:        "https://nvoken-executor.example.run.app",
		OIDCServiceAccount: "invoker@test.iam.gserviceaccount.com",
		OIDCAudience:       "https://nvoken-executor.example.run.app",
		DispatchDeadline:   30 * time.Minute,
	}
}

type fakeClient struct {
	created   *cloudtaskspb.CreateTaskRequest
	createErr error
	getErr    error
}

func (c *fakeClient) CreateTask(_ context.Context, request *cloudtaskspb.CreateTaskRequest, _ ...gax.CallOption) (*cloudtaskspb.Task, error) {
	c.created = request
	if c.createErr != nil {
		return nil, c.createErr
	}
	return &cloudtaskspb.Task{Name: request.Task.Name}, nil
}

func (c *fakeClient) GetTask(_ context.Context, request *cloudtaskspb.GetTaskRequest, _ ...gax.CallOption) (*cloudtaskspb.Task, error) {
	if c.getErr != nil {
		return nil, c.getErr
	}
	return &cloudtaskspb.Task{Name: request.Name}, nil
}

func (c *fakeClient) Close() error { return nil }
