package localrun

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type dockerResponse struct {
	Output string
	Err    error
}

type fakeDocker struct {
	Responses []dockerResponse
	Calls     [][]string
}

func (f *fakeDocker) Run(_ context.Context, arguments ...string) (string, error) {
	f.Calls = append(f.Calls, append([]string(nil), arguments...))
	if len(f.Responses) == 0 {
		return "", errors.New("unexpected Docker call")
	}
	response := f.Responses[0]
	f.Responses = f.Responses[1:]
	return response.Output, response.Err
}

func TestPrepareCreatesDisposableEnvironmentAndPostgres(t *testing.T) {
	docker := &fakeDocker{Responses: []dockerResponse{
		{Output: "27.0.0"},
		{Err: errors.New("Error: No such object: " + containerName)},
		{Output: "container-id"},
		{Output: "healthy"},
	}}
	path := filepath.Join(t.TempDir(), ".env")
	var output bytes.Buffer
	result, err := Prepare(context.Background(), Options{
		Provider:    " OpenAI ",
		Model:       " gpt-test ",
		OutputPath:  path,
		Environment: map[string]string{"OPENAI_API_KEY": "provider-secret"},
		Docker:      docker.Run,
		Random:      bytes.NewReader(bytes.Repeat([]byte{0x42}, 96)),
		Output:      &output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "openai" || result.Environment["OPENAI_API_KEY"] != "provider-secret" {
		t.Fatalf("result = %#v", result)
	}
	if result.Environment["ANTHROPIC_API_KEY"] != "" {
		t.Fatalf("non-selected provider = %q", result.Environment["ANTHROPIC_API_KEY"])
	}
	for name, value := range map[string]string{
		"NVOKEN_API_KEY":  result.Environment["RUNTIME_API_KEY"],
		"NVOKEN_BASE_URL": "http://localhost:8080",
		"NVOKEN_MODEL":    "gpt-test",
		"NVOKEN_PROVIDER": "openai",
	} {
		if result.Environment[name] != value {
			t.Fatalf("%s = %q, want %q", name, result.Environment[name], value)
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(content), fileMarker+"\n") || !strings.Contains(string(content), "OPENAI_API_KEY=provider-secret\n") {
		t.Fatalf("environment content = %s", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("environment mode = %o", info.Mode().Perm())
	}
	if strings.Contains(output.String(), "provider-secret") {
		t.Fatalf("output exposed provider key: %s", output.String())
	}
	wantPrefix := []string{"run", "--detach", "--name", containerName}
	if len(docker.Calls) < 3 || !reflect.DeepEqual(docker.Calls[2][:len(wantPrefix)], wantPrefix) {
		t.Fatalf("Docker calls = %#v", docker.Calls)
	}
}

func TestPrepareReusesOnlyMarkedEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("OPENAI_API_KEY=provider-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	docker := &fakeDocker{}
	_, err := Prepare(context.Background(), Options{
		Provider:   "openai",
		Model:      "gpt-test",
		OutputPath: path,
		Docker:     docker.Run,
	})
	if err == nil || !strings.Contains(err.Error(), "did not create it") {
		t.Fatalf("error = %v", err)
	}
	if len(docker.Calls) != 0 {
		t.Fatalf("Docker was called before environment validation: %#v", docker.Calls)
	}
}

func TestPrepareReusesMatchingEnvironmentWithoutFlags(t *testing.T) {
	values, err := generatedEnvironment(
		"openai",
		"gpt-test",
		"provider-secret",
		bytes.NewReader(bytes.Repeat([]byte{0x42}, 96)),
	)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(renderEnvironment(values)), 0o600); err != nil {
		t.Fatal(err)
	}
	docker := &fakeDocker{Responses: []dockerResponse{
		{Output: "27.0.0"},
		{Output: "true|running"},
		{Output: "healthy"},
	}}
	result, err := Prepare(context.Background(), Options{
		OutputPath:  path,
		Environment: map[string]string{},
		Docker:      docker.Run,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "openai" || result.Environment["NVOKEN_MODEL"] != "gpt-test" {
		t.Fatalf("result = %#v", result)
	}
}

func TestPrepareRefusesChangedProviderKeyBeforeDocker(t *testing.T) {
	values, err := generatedEnvironment(
		"openai",
		"gpt-test",
		"saved-provider-secret",
		bytes.NewReader(bytes.Repeat([]byte{0x42}, 96)),
	)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(renderEnvironment(values)), 0o600); err != nil {
		t.Fatal(err)
	}
	docker := &fakeDocker{}
	_, err = Prepare(context.Background(), Options{
		Provider:    "openai",
		Model:       "gpt-test",
		OutputPath:  path,
		Environment: map[string]string{"OPENAI_API_KEY": "rotated-provider-secret"},
		Docker:      docker.Run,
	})
	if err == nil || !strings.Contains(err.Error(), "different OPENAI_API_KEY") || !strings.Contains(err.Error(), "remove the file") {
		t.Fatalf("error = %v", err)
	}
	if len(docker.Calls) != 0 {
		t.Fatalf("Docker was called before provider-key validation: %#v", docker.Calls)
	}
}

func TestParseExistingRejectsDesynchronizedRuntimeKey(t *testing.T) {
	values, err := generatedEnvironment(
		"openai",
		"gpt-test",
		"provider-secret",
		bytes.NewReader(bytes.Repeat([]byte{0x42}, 96)),
	)
	if err != nil {
		t.Fatal(err)
	}
	values["NVOKEN_API_KEY"] = "different-runtime-secret"
	_, _, err = parseExisting([]byte(renderEnvironment(values)))
	if err == nil || !strings.Contains(err.Error(), "NVOKEN_API_KEY must match RUNTIME_API_KEY") {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareExplainsPostgresPortConflict(t *testing.T) {
	docker := &fakeDocker{Responses: []dockerResponse{
		{Output: "27.0.0"},
		{Err: errors.New("Error: No such object: " + containerName)},
		{Err: errors.New("Bind for 127.0.0.1:55432 failed: port is already allocated")},
	}}
	path := filepath.Join(t.TempDir(), ".env")
	_, err := Prepare(context.Background(), Options{
		Provider:    "openai",
		Model:       "gpt-test",
		OutputPath:  path,
		Environment: map[string]string{"OPENAI_API_KEY": "provider-secret"},
		Docker:      docker.Run,
		Random:      bytes.NewReader(bytes.Repeat([]byte{0x42}, 96)),
	})
	if err == nil || err.Error() != "localhost port 55432 is already in use; stop the process or container using it and run nvokend quickstart again" {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("environment file exists after port conflict: %v", statErr)
	}
}

func TestPrepareRefusesBrokenEnvironmentSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".env")
	if err := os.Symlink(filepath.Join(root, "missing"), path); err != nil {
		t.Fatal(err)
	}
	_, err := Prepare(context.Background(), Options{
		Provider:    "openai",
		Model:       "gpt-test",
		OutputPath:  path,
		Environment: map[string]string{"OPENAI_API_KEY": "provider-secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareRequiresSelectedProviderKey(t *testing.T) {
	_, err := Prepare(context.Background(), Options{
		Provider:    "anthropic",
		Model:       "claude-test",
		OutputPath:  filepath.Join(t.TempDir(), ".env"),
		Environment: map[string]string{},
	})
	if err == nil || !strings.Contains(err.Error(), "export ANTHROPIC_API_KEY") {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareRequiresModel(t *testing.T) {
	_, err := Prepare(context.Background(), Options{
		Provider:    "openai",
		OutputPath:  filepath.Join(t.TempDir(), ".env"),
		Environment: map[string]string{"OPENAI_API_KEY": "provider-secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "--model is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestCleanupRemovesOnlyOwnedContainer(t *testing.T) {
	docker := &fakeDocker{Responses: []dockerResponse{
		{Output: "27.0.0"},
		{Err: errors.New("Error: No such object: " + containerName)},
	}}
	var output bytes.Buffer
	if err := Cleanup(context.Background(), docker.Run, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "already absent") {
		t.Fatalf("absent cleanup output = %q", output.String())
	}

	docker = &fakeDocker{Responses: []dockerResponse{
		{Output: "27.0.0"},
		{Output: "false"},
	}}
	if err := Cleanup(context.Background(), docker.Run, nil); err == nil || !strings.Contains(err.Error(), "did not create it") {
		t.Fatalf("unowned cleanup error = %v", err)
	}

	docker = &fakeDocker{Responses: []dockerResponse{
		{Output: "27.0.0"},
		{Output: "true"},
		{Output: containerName},
	}}
	output.Reset()
	if err := Cleanup(context.Background(), docker.Run, &output); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(docker.Calls[2], []string{"rm", "--force", containerName}) {
		t.Fatalf("cleanup calls = %#v", docker.Calls)
	}
	if !strings.Contains(output.String(), "disposable database") {
		t.Fatalf("cleanup output = %q", output.String())
	}
}
