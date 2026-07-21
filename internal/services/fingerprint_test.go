package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fingerprintVector struct {
	Name     string `json:"name"`
	Selector struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"selector"`
	Spec      InlineExecutionSpec `json:"spec"`
	Input     InvocationInput     `json:"input"`
	Canonical string              `json:"canonical"`
	SHA256    string              `json:"sha256"`
}

func TestInvocationFingerprintV1DesignVectors(t *testing.T) {
	testFingerprintDesignVectors(t, "admission-fingerprint-v1.json", 1)
}

func TestInvocationFingerprintV2DesignVectors(t *testing.T) {
	testFingerprintDesignVectors(t, "admission-fingerprint-v2.json", 2)
}

func testFingerprintDesignVectors(t *testing.T, filename string, version int) {
	t.Helper()
	_, callerFile, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(callerFile), "..", "..", "docs", "design", filename)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fingerprint vectors: %v", err)
	}
	var vectors []fingerprintVector
	if err := json.Unmarshal(payload, &vectors); err != nil {
		t.Fatalf("decode fingerprint vectors: %v", err)
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			input := CreateInvocationInput{Spec: vector.Spec, Input: vector.Input}
			switch vector.Selector.Kind {
			case "none":
			case "id":
				input.SessionID = &vector.Selector.Value
			case "key":
				input.SessionKey = &vector.Selector.Value
			default:
				t.Fatalf("unknown selector kind %q", vector.Selector.Kind)
			}
			var canonical []byte
			var fingerprint [sha256.Size]byte
			var err error
			if version == 1 {
				canonical, err = invocationFingerprintBytesV1(input)
				fingerprint, _ = InvocationFingerprintV1(input)
			} else {
				canonical, err = invocationFingerprintBytesV2(input)
				fingerprint, _ = InvocationFingerprintV2(input)
			}
			if err != nil {
				t.Fatalf("canonicalize: %v", err)
			}
			if string(canonical) != vector.Canonical {
				t.Fatalf("canonical = %q, want %q", canonical, vector.Canonical)
			}
			if hex.EncodeToString(fingerprint[:]) != vector.SHA256 {
				t.Fatalf("sha256 = %x, want %s", fingerprint, vector.SHA256)
			}
		})
	}
}

func TestFingerprintMaterialOrdering(t *testing.T) {
	input := validServiceInput()
	base, _ := InvocationFingerprintV1(input)

	reordered := input
	reordered.Input.Content = []TextInputBlock{input.Input.Content[1], input.Input.Content[0]}
	changed, _ := InvocationFingerprintV1(reordered)
	if base == changed {
		t.Fatal("array order did not change fingerprint")
	}

	reselected := input
	reselected.SessionKey = nil
	reselected.SessionID = stringPointer("sesn_019b0a12-0000-7000-8000-000000000003")
	changed, _ = InvocationFingerprintV1(reselected)
	if base == changed {
		t.Fatal("selector kind did not change fingerprint")
	}
	if len(base) != sha256.Size {
		t.Fatalf("fingerprint length = %d", len(base))
	}
}

func TestFingerprintUsesMinimalUTF8JSONStringEncoding(t *testing.T) {
	input := validServiceInput()
	input.Spec.Instructions = "line\u2028café 😀\x01"
	canonical, err := invocationFingerprintBytesV1(input)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	encoded := string(canonical)
	if !strings.Contains(encoded, "line\u2028café 😀\\u0001") {
		t.Fatalf("canonical JSON did not preserve UTF-8 and minimally escape controls: %q", encoded)
	}
	if strings.Contains(encoded, "\\u2028") {
		t.Fatalf("canonical JSON escaped a non-control Unicode rune: %q", encoded)
	}
}

func TestValidateCreateInvocationLimits(t *testing.T) {
	tests := map[string]func(*CreateInvocationInput){
		"agent ref too long": func(input *CreateInvocationInput) { input.AgentRef = strings.Repeat("a", MaxReferenceCharacters+1) },
		"blank key":          func(input *CreateInvocationInput) { input.IdempotencyKey = "   " },
		"two selectors": func(input *CreateInvocationInput) {
			input.SessionID = stringPointer("sesn_019b0a12-0000-7000-8000-000000000003")
		},
		"too many blocks": func(input *CreateInvocationInput) {
			input.Input.Content = make([]TextInputBlock, MaxInputBlocks+1)
			for index := range input.Input.Content {
				input.Input.Content[index] = TextInputBlock{Type: "text", Text: "x"}
			}
		},
		"deferred block":     func(input *CreateInvocationInput) { input.Input.Content[0].Type = "tool" },
		"blank text":         func(input *CreateInvocationInput) { input.Input.Content[0].Text = "\t" },
		"blank instructions": func(input *CreateInvocationInput) { input.Spec.Instructions = "" },
		"model too long": func(input *CreateInvocationInput) {
			input.Spec.Model.Name = strings.Repeat("m", MaxReferenceCharacters+1)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := validServiceInput()
			mutate(&input)
			if err := ValidateCreateInvocation(input); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}
	if err := ValidateCreateInvocation(validServiceInput()); err != nil {
		t.Fatalf("valid request: %v", err)
	}
}

func TestValidateCreateInvocationUnicodeAndBlockBoundaries(t *testing.T) {
	setters := map[string]func(*CreateInvocationInput, string){
		"agent_ref":           func(input *CreateInvocationInput, value string) { input.AgentRef = value },
		"tenant_ref":          func(input *CreateInvocationInput, value string) { input.TenantRef = stringPointer(value) },
		"session_key":         func(input *CreateInvocationInput, value string) { input.SessionKey = stringPointer(value) },
		"idempotency_key":     func(input *CreateInvocationInput, value string) { input.IdempotencyKey = value },
		"spec.model.provider": func(input *CreateInvocationInput, value string) { input.Spec.Model.Provider = value },
		"spec.model.name":     func(input *CreateInvocationInput, value string) { input.Spec.Model.Name = value },
	}
	for field, set := range setters {
		t.Run(field, func(t *testing.T) {
			input := validServiceInput()
			set(&input, strings.Repeat("界", MaxReferenceCharacters))
			if err := ValidateCreateInvocation(input); err != nil {
				t.Fatalf("255 Unicode characters rejected: %v", err)
			}
			set(&input, strings.Repeat("界", MaxReferenceCharacters+1))
			if err := ValidateCreateInvocation(input); err == nil {
				t.Fatal("256 Unicode characters accepted")
			}
		})
	}

	input := validServiceInput()
	input.Input.Content = make([]TextInputBlock, MaxInputBlocks)
	for index := range input.Input.Content {
		input.Input.Content[index] = TextInputBlock{Type: "text", Text: "x"}
	}
	if err := ValidateCreateInvocation(input); err != nil {
		t.Fatalf("64 input blocks rejected: %v", err)
	}
	input.Input.Content = append(input.Input.Content, TextInputBlock{Type: "text", Text: "x"})
	if err := ValidateCreateInvocation(input); err == nil {
		t.Fatal("65 input blocks accepted")
	}
}

func validServiceInput() CreateInvocationInput {
	return CreateInvocationInput{
		AgentRef: "support", SessionKey: stringPointer("ticket-1"), IdempotencyKey: "request-1",
		Input: InvocationInput{Content: []TextInputBlock{{Type: "text", Text: "first"}, {Type: "text", Text: "second"}}},
		Spec:  InlineExecutionSpec{Instructions: "help", Model: ModelSelection{Provider: "anthropic", Name: "test-model"}},
	}
}

func stringPointer(value string) *string { return &value }
