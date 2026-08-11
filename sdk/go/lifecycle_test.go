package nvoken

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestArchiveLifecycleMethods(t *testing.T) {
	const (
		agentDefinitionID = "def_test"
		appID             = "app_test"
		orgID             = "org_test"
	)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/agent-definitions":
			if request.URL.Query().Get("include_archived") != "true" ||
				request.URL.Query().Get("cursor") != "page-2" ||
				request.URL.Query().Get("limit") != "10" {
				t.Errorf("Agent Definition list query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"items":[{"id":"` + agentDefinitionID + `","revision":2}],"has_more":false,"next_cursor":null}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/agent-definitions/"+agentDefinitionID:
			writer.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodPost && request.URL.Path == "/v1/agent-definitions/"+agentDefinitionID+"/restore":
			writer.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/apps":
			if request.URL.Query().Get("external_ref") != "customer-1" || request.URL.Query().Get("status") != "archived" {
				t.Errorf("App list query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"items":[{"id":"` + appID + `","name":"support"}]}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/apps/"+appID:
			writer.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodPost && request.URL.Path == "/v1/apps/"+appID+"/restore":
			writer.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/orgs":
			if request.URL.Query().Get("status") != "all" {
				t.Errorf("Org list query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"items":[{"id":"` + orgID + `","display_name":"Example"}]}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/orgs/"+orgID:
			writer.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodPost && request.URL.Path == "/v1/orgs/"+orgID+"/restore":
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "operator-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	includeArchived := true
	cursor := "page-2"
	limit := 10
	definitions, err := client.ListAgentDefinitions(context.Background(), ListAgentDefinitionsOptions{
		IncludeArchived: &includeArchived,
		Cursor:          &cursor,
		Limit:           &limit,
	})
	if err != nil || len(definitions.Items) != 1 || definitions.Items[0].ID != agentDefinitionID {
		t.Fatalf("ListAgentDefinitions = %#v, %v", definitions, err)
	}
	if err := client.ArchiveAgentDefinition(context.Background(), agentDefinitionID); err != nil {
		t.Fatalf("ArchiveAgentDefinition: %v", err)
	}
	if err := client.RestoreAgentDefinition(context.Background(), agentDefinitionID); err != nil {
		t.Fatalf("RestoreAgentDefinition: %v", err)
	}

	externalRef := "customer-1"
	archived := ArchiveStatusArchived
	apps, err := client.ListApps(context.Background(), ListAppsOptions{
		ExternalRef: &externalRef,
		Status:      &archived,
	})
	if err != nil || len(apps.Items) != 1 || apps.Items[0].ID != appID {
		t.Fatalf("ListApps = %#v, %v", apps, err)
	}
	if err := client.ArchiveApp(context.Background(), appID); err != nil {
		t.Fatalf("ArchiveApp: %v", err)
	}
	if err := client.RestoreApp(context.Background(), appID); err != nil {
		t.Fatalf("RestoreApp: %v", err)
	}

	all := ArchiveStatusAll
	orgs, err := client.ListOrgs(context.Background(), ListOrgsOptions{Status: &all})
	if err != nil || len(orgs.Items) != 1 || orgs.Items[0].ID != orgID {
		t.Fatalf("ListOrgs = %#v, %v", orgs, err)
	}
	if err := client.ArchiveOrg(context.Background(), orgID); err != nil {
		t.Fatalf("ArchiveOrg: %v", err)
	}
	if err := client.RestoreOrg(context.Background(), orgID); err != nil {
		t.Fatalf("RestoreOrg: %v", err)
	}
	if requests != 9 {
		t.Fatalf("requests = %d, want 9", requests)
	}
}
