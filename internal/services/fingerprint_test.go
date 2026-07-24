package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/deepnoodle-ai/nvoken/internal/structuredoutput"
)

type fingerprintVector struct {
	Name     string `json:"name"`
	Selector struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"selector"`
	Spec                InlineExecutionSpec           `json:"spec"`
	Input               InvocationInput               `json:"input"`
	ProviderCredentials []ProviderCredentialSelection `json:"provider_credentials,omitempty"`
	Canonical           string                        `json:"canonical"`
	SHA256              string                        `json:"sha256"`
}

func TestInvocationFingerprintV1DesignVectors(t *testing.T) {
	testFingerprintDesignVectors(t, "admission-fingerprint-v1.json", 1)
}

func TestInvocationFingerprintV2DesignVectors(t *testing.T) {
	testFingerprintDesignVectors(t, "admission-fingerprint-v2.json", 2)
}

func TestInvocationFingerprintV3DesignVectors(t *testing.T) {
	testFingerprintDesignVectors(t, "admission-fingerprint-v3.json", 3)
}

func TestInvocationFingerprintV4DesignVectors(t *testing.T) {
	testFingerprintDesignVectors(t, "admission-fingerprint-v4.json", 4)
}

func TestInvocationFingerprintV5DesignVectors(t *testing.T) {
	testFingerprintDesignVectors(t, "admission-fingerprint-v5.json", 5)
}

func TestInvocationFingerprintV6DesignVectors(t *testing.T) {
	testFingerprintDesignVectors(t, "admission-fingerprint-v6.json", 6)
}

func TestInvocationFingerprintV7DesignVectors(t *testing.T) {
	testFingerprintDesignVectors(t, "admission-fingerprint-v7.json", 7)
}

func TestInvocationFingerprintV8DesignVectors(t *testing.T) {
	testFingerprintDesignVectors(t, "admission-fingerprint-v8.json", 8)
}

func TestInvocationFingerprintV6PreservesLiteralSourceWithoutSecretMaterial(t *testing.T) {
	input := validServiceInput()
	omitted, err := InvocationFingerprintV6(input)
	if err != nil {
		t.Fatalf("omitted fingerprint: %v", err)
	}
	input.ProviderCredentials = []ProviderCredentialSelection{
		{
			Provider: "anthropic",
			Source:   "caller_ephemeral",
			Credential: &ProviderStaticCredentialInput{
				APIKey: "first-secret",
			},
		},
	}
	first, err := InvocationFingerprintV6(input)
	if err != nil {
		t.Fatalf("first explicit fingerprint: %v", err)
	}
	input.ProviderCredentials[0].Credential.APIKey = "different-secret"
	second, err := InvocationFingerprintV6(input)
	if err != nil {
		t.Fatalf("second explicit fingerprint: %v", err)
	}
	if first != second {
		t.Fatal("caller secret changed fingerprint v6")
	}
	if omitted == first {
		t.Fatal("literal omission matched explicit caller source")
	}
	input.ProviderCredentials[0].Source = "account_byok"
	input.ProviderCredentials[0].Credential = nil
	account, err := InvocationFingerprintV6(input)
	if err != nil {
		t.Fatalf("account fingerprint: %v", err)
	}
	if account == first {
		t.Fatal("changed explicit source did not change fingerprint v6")
	}
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
			input := CreateInvocationInput{
				Spec:                vector.Spec,
				Input:               vector.Input,
				ProviderCredentials: vector.ProviderCredentials,
			}
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
			} else if version == 2 {
				canonical, err = invocationFingerprintBytesV2(input)
				fingerprint, _ = InvocationFingerprintV2(input)
			} else if version == 3 {
				canonical, err = invocationFingerprintBytesV3(input)
				fingerprint, _ = InvocationFingerprintV3(input)
			} else if version == 4 {
				canonical, err = invocationFingerprintBytesV4(input)
				fingerprint, _ = InvocationFingerprintV4(input)
			} else if version == 5 {
				canonical, err = invocationFingerprintBytesV5(input)
				fingerprint, _ = InvocationFingerprintV5(input)
			} else if version == 6 {
				canonical, err = invocationFingerprintBytesV6(input)
				fingerprint, _ = InvocationFingerprintV6(input)
			} else if version == 7 {
				canonical, err = invocationFingerprintBytesV7(input)
				fingerprint, _ = InvocationFingerprintV7(input)
			} else {
				canonical, err = invocationFingerprintBytesV8(input)
				fingerprint, _ = InvocationFingerprintV8(input)
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
		"agent key too long": func(input *CreateInvocationInput) { input.AgentKey = strings.Repeat("a", MaxReferenceCharacters+1) },
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
		"deferred block":              func(input *CreateInvocationInput) { input.Input.Content[0].Type = "tool" },
		"blank text":                  func(input *CreateInvocationInput) { input.Input.Content[0].Text = "\t" },
		"blank supplied instructions": func(input *CreateInvocationInput) { input.Spec.Instructions = "   " },
		"model too long": func(input *CreateInvocationInput) {
			input.Spec.Model.ID = strings.Repeat("m", MaxReferenceCharacters+1)
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
	withoutInstructions := validServiceInput()
	withoutInstructions.Spec.Instructions = ""
	if err := ValidateCreateInvocation(withoutInstructions); err != nil {
		t.Fatalf("instruction-free request: %v", err)
	}
}

func TestValidateCreateInvocationRejectsUninstalledExtensibleProvider(t *testing.T) {
	input := validServiceInput()
	input.Spec.Model.Provider = "future_provider"
	if err := ValidateCreateInvocation(input); err == nil {
		t.Fatal("syntactically valid uninstalled provider was admitted")
	}
}

func TestValidateCreateInvocationUnicodeAndBlockBoundaries(t *testing.T) {
	setters := map[string]func(*CreateInvocationInput, string){
		"agent_key":       func(input *CreateInvocationInput, value string) { input.AgentKey = value },
		"tenant_key":      func(input *CreateInvocationInput, value string) { input.TenantKey = stringPointer(value) },
		"session_key":     func(input *CreateInvocationInput, value string) { input.SessionKey = stringPointer(value) },
		"idempotency_key": func(input *CreateInvocationInput, value string) { input.IdempotencyKey = value },
		"spec.model.id":   func(input *CreateInvocationInput, value string) { input.Spec.Model.ID = value },
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

func TestValidateCreateInvocationStructuredSchemaSizeBoundary(t *testing.T) {
	const prefix = `{"type":"object","description":"`
	const suffix = `"}`
	input := validServiceInput()
	exact := prefix + strings.Repeat("x", structuredoutput.MaxSchemaBytes-len(prefix)-len(suffix)) + suffix
	input.Spec.Output = &StructuredOutputSpec{
		Schema: json.RawMessage(exact),
	}
	if err := ValidateCreateInvocation(input); err != nil {
		t.Fatalf("schema at compact size limit: %v", err)
	}
	input.Spec.Output.Schema = json.RawMessage(prefix + strings.Repeat("x", structuredoutput.MaxSchemaBytes-len(prefix)-len(suffix)+1) + suffix)
	if err := ValidateCreateInvocation(input); err == nil {
		t.Fatal("schema above compact size limit was accepted")
	}
}

func TestValidateCreateInvocationHostToolBoundaries(t *testing.T) {
	validTool := HostToolSpec{
		Name:        "lookup_order",
		Description: "Look up an order",
		Mode:        "host",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}},"additionalProperties":false}`),
	}
	input := validServiceInput()
	input.Spec.Tools = make([]HostToolSpec, MaxHostTools)
	for index := range input.Spec.Tools {
		input.Spec.Tools[index] = validTool
		input.Spec.Tools[index].Name = fmt.Sprintf("tool_%d", index)
	}
	if err := ValidateCreateInvocation(input); err != nil {
		t.Fatalf("maximum host tools rejected: %v", err)
	}

	tests := map[string]func(*CreateInvocationInput){
		"too many": func(input *CreateInvocationInput) {
			input.Spec.Tools = append(input.Spec.Tools, validTool)
		},
		"duplicate name": func(input *CreateInvocationInput) {
			input.Spec.Tools[1].Name = input.Spec.Tools[0].Name
		},
		"reserved name": func(input *CreateInvocationInput) {
			input.Spec.Tools[0].Name = "nvoken_private"
		},
		"unsupported mode": func(input *CreateInvocationInput) {
			input.Spec.Tools[0].Mode = "callback"
		},
		"blank description": func(input *CreateInvocationInput) {
			input.Spec.Tools[0].Description = " "
		},
		"invalid schema": func(input *CreateInvocationInput) {
			input.Spec.Tools[0].InputSchema = json.RawMessage(`{"type":"string"}`)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := input
			candidate.Spec.Tools = append([]HostToolSpec(nil), input.Spec.Tools...)
			mutate(&candidate)
			if err := ValidateCreateInvocation(candidate); err == nil {
				t.Fatal("invalid host tools were accepted")
			}
		})
	}
}

func TestValidateCreateInvocationCallbackToolBoundaries(t *testing.T) {
	callbackURLPrefix := "https://callbacks.example.test/"
	callback := HostToolSpec{
		Name:        "lookup_callback",
		Description: "Look up a value through a callback",
		Mode:        "callback",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Callback: &CallbackTarget{
			URL: "https://callbacks.example.test/tools/lookup?version=1",
		},
	}
	input := validServiceInput()
	input.Spec.Tools = []HostToolSpec{callback}
	if err := ValidateCreateInvocation(input); err != nil {
		t.Fatalf("valid callback tool rejected: %v", err)
	}
	maximum := input
	maximum.Spec.Tools = append([]HostToolSpec(nil), input.Spec.Tools...)
	maximumTarget := *callback.Callback
	maximumTarget.URL = callbackURLPrefix + strings.Repeat("x", MaxCallbackURLBytes-len(callbackURLPrefix))
	maximum.Spec.Tools[0].Callback = &maximumTarget
	if err := ValidateCreateInvocation(maximum); err != nil {
		t.Fatalf("maximum callback URL rejected: %v", err)
	}
	for name, url := range map[string]string{
		"http":         "http://callbacks.example.test/tool",
		"userinfo":     "https://secret@callbacks.example.test/tool",
		"fragment":     "https://callbacks.example.test/tool#secret",
		"missing host": "https:///tool",
		"too long":     maximumTarget.URL + "x",
	} {
		t.Run(name, func(t *testing.T) {
			candidate := input
			candidate.Spec.Tools = append([]HostToolSpec(nil), input.Spec.Tools...)
			target := *callback.Callback
			target.URL = url
			candidate.Spec.Tools[0].Callback = &target
			if err := ValidateCreateInvocation(candidate); err == nil {
				t.Fatal("invalid callback URL accepted")
			}
		})
	}
	missing := input
	missing.Spec.Tools = append([]HostToolSpec(nil), input.Spec.Tools...)
	missing.Spec.Tools[0].Callback = nil
	if err := ValidateCreateInvocation(missing); err == nil {
		t.Fatal("callback mode without callback target accepted")
	}
	hostWithCallback := input
	hostWithCallback.Spec.Tools = append([]HostToolSpec(nil), input.Spec.Tools...)
	hostWithCallback.Spec.Tools[0].Mode = "host"
	if err := ValidateCreateInvocation(hostWithCallback); err == nil {
		t.Fatal("host mode with callback target accepted")
	}
	var hostWithNull HostToolSpec
	if err := json.Unmarshal([]byte(`{
		"name":"lookup_host",
		"description":"Look up a value through the host",
		"mode":"host",
		"input_schema":{"type":"object"},
		"callback":null
	}`), &hostWithNull); err != nil {
		t.Fatalf("decode host callback null: %v", err)
	}
	nullInput := validServiceInput()
	nullInput.Spec.Tools = []HostToolSpec{hostWithNull}
	if err := ValidateCreateInvocation(nullInput); err == nil {
		t.Fatal("host mode with explicit null callback accepted")
	}
}

func validServiceInput() CreateInvocationInput {
	return CreateInvocationInput{
		AgentKey:       "support",
		SessionKey:     stringPointer("ticket-1"),
		IdempotencyKey: "request-1",
		Input:          InvocationInput{Content: []TextInputBlock{{Type: "text", Text: "first"}, {Type: "text", Text: "second"}}},
		Spec: InlineExecutionSpec{
			Instructions: "help",
			Model: ModelSelection{
				Provider: "anthropic",
				ID:       "test-model",
			},
		},
	}
}

func stringPointer(value string) *string { return &value }
