package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
	"github.com/deepnoodle-ai/wonton/cli"
)

func registerMCPCommands(app *cli.App) {
	mcp := app.Group("mcp").Description("Discover remote MCP tools")
	mcp.Command("list-tools").
		Description("Connect to one remote server and print the exact projected tool catalog").
		Flags(
			cli.String("name").Default("mcp").Help("Server name used as the projected tool prefix"),
			cli.String("url").Required().Help("Public HTTPS streamable-HTTP MCP endpoint"),
			cli.Strings("allowed-tool", "").Help("Remote tool to allow; repeat to preserve allowlist order"),
			cli.Strings("header", "").Help("Secret request header as NAME=VALUE; repeat as needed"),
			cli.String("header-env").Help("Environment variable containing a JSON object of secret headers"),
			cli.Int("discovery-timeout").Help("Discovery timeout in seconds"),
			cli.Int("call-timeout").Help("Tool-call timeout in seconds for the declaration"),
		).
		Run(runMCPListTools)
}

func runMCPListTools(command *cli.Context) error {
	headers, err := mcpHeaders(command.Strings("header"), command.String("header-env"))
	if err != nil {
		return err
	}
	server := nvoken.MCPServer{
		Name:         command.String("name"),
		URL:          command.String("url"),
		AllowedTools: append([]string(nil), command.Strings("allowed-tool")...),
		Headers:      headers,
	}
	discoveryTimeout := command.Int("discovery-timeout")
	callTimeout := command.Int("call-timeout")
	if discoveryTimeout < 0 || callTimeout < 0 {
		return errors.New("MCP timeouts cannot be negative")
	}
	if discoveryTimeout > 0 || callTimeout > 0 {
		server.Timeouts = &nvoken.MCPTimeouts{}
		if discoveryTimeout > 0 {
			server.Timeouts.DiscoverySeconds = &discoveryTimeout
		}
		if callTimeout > 0 {
			server.Timeouts.CallSeconds = &callTimeout
		}
	}
	client, err := runtimeClient(command)
	if err != nil {
		return err
	}
	catalog, err := client.ListMCPTools(command.Context(), server)
	if err != nil {
		return err
	}
	return writeOutput(command, catalog, func(writer io.Writer) error {
		for _, tool := range catalog.Tools {
			if _, err := fmt.Fprintf(
				writer,
				"tool\t%s\t%s\t%s\n",
				tool.ProjectedName,
				tool.RemoteName,
				tool.Description,
			); err != nil {
				return err
			}
		}
		for _, exclusion := range catalog.Exclusions {
			if _, err := fmt.Fprintf(
				writer,
				"excluded\t%s\t%s\t%s\n",
				exclusion.ServerName,
				exclusion.RemoteName,
				exclusion.Reason,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func mcpHeaders(values []string, environmentName string) (map[string]string, error) {
	headers := make(map[string]string, len(values))
	if environmentName != "" {
		encoded, present := os.LookupEnv(environmentName)
		if !present {
			return nil, fmt.Errorf("MCP header environment variable %q is not set", environmentName)
		}
		if err := json.Unmarshal([]byte(encoded), &headers); err != nil || headers == nil {
			return nil, fmt.Errorf(
				"MCP header environment variable %q must contain a JSON object of string values",
				environmentName,
			)
		}
	}
	for _, value := range values {
		name, headerValue, found := strings.Cut(value, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" || headerValue == "" {
			return nil, errors.New("each MCP header must use NAME=VALUE with a non-empty name and value")
		}
		if _, exists := headers[name]; exists {
			return nil, fmt.Errorf("MCP header %q was supplied more than once", name)
		}
		headers[name] = headerValue
	}
	if len(headers) == 0 {
		return nil, nil
	}
	return headers, nil
}
