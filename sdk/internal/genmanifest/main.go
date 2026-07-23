package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type document struct {
	Paths map[string]pathItem `yaml:"paths"`
}

type pathItem struct {
	Get    *operation `yaml:"get"`
	Post   *operation `yaml:"post"`
	Put    *operation `yaml:"put"`
	Patch  *operation `yaml:"patch"`
	Delete *operation `yaml:"delete"`
}

type operation struct {
	OperationID string `yaml:"operationId"`
}

type manifestOperation struct {
	OperationID string `json:"operation_id"`
	Method      string `json:"method"`
	Path        string `json:"path"`
}

func main() {
	contents, err := os.ReadFile("openapi/runtime.yaml")
	if err != nil {
		panic(err)
	}
	var spec document
	if err := yaml.Unmarshal(contents, &spec); err != nil {
		panic(err)
	}
	operations := make([]manifestOperation, 0)
	for path, item := range spec.Paths {
		methods := map[string]*operation{
			"get":    item.Get,
			"post":   item.Post,
			"put":    item.Put,
			"patch":  item.Patch,
			"delete": item.Delete,
		}
		for method, operation := range methods {
			if operation == nil || operation.OperationID == "" {
				continue
			}
			operations = append(operations, manifestOperation{
				OperationID: operation.OperationID,
				Method:      strings.ToUpper(method),
				Path:        path,
			})
		}
	}
	sort.Slice(operations, func(i, j int) bool {
		return operations[i].OperationID < operations[j].OperationID
	})
	encoded, err := json.MarshalIndent(struct {
		GeneratedBy string              `json:"generated_by"`
		Operations  []manifestOperation `json:"operations"`
	}{
		GeneratedBy: "openapi/runtime.yaml; do not edit",
		Operations:  operations,
	}, "", "  ")
	if err != nil {
		panic(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile("sdk/operations.json", encoded, 0o644); err != nil {
		panic(err)
	}
	sessionStreamPath := ""
	invocationStreamPath := ""
	for _, operation := range operations {
		if operation.OperationID == "streamSessionTranscript" {
			sessionStreamPath = operation.Path
		}
		if operation.OperationID == "streamInvocation" {
			invocationStreamPath = operation.Path
		}
	}
	if sessionStreamPath == "" || invocationStreamPath == "" {
		panic("stream operations are missing")
	}
	routes := fmt.Sprintf(
		"// Code generated from openapi/runtime.yaml; DO NOT EDIT.\n\npub const STREAM_SESSION_TRANSCRIPT: &str = %q;\npub const STREAM_INVOCATION: &str = %q;\n",
		sessionStreamPath,
		invocationStreamPath,
	)
	if err := os.WriteFile("sdk/rust/src/routes.rs", []byte(routes), 0o644); err != nil {
		panic(err)
	}
}
