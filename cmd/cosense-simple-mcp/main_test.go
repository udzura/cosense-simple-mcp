package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run the real main function in a subprocess. CommandTransport verifies newline
// JSON-RPC initialization, discovery, calls, stdout purity, and shutdown.
func TestStdioProcess(t *testing.T) {
	if os.Getenv("COSENSE_TEST_PROCESS") == "1" {
		main()
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStdioProcess$")
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "COSENSE_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "COSENSE_TEST_PROCESS=1", "COSENSE_PROJECT_NAME=test-project")
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 6 {
		t.Fatalf("%+v %v", list, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "convert_to_cosense", Arguments: map[string]any{"content": "**日本語**", "format": "markdown"}})
	if err != nil || result.IsError {
		t.Fatalf("%+v %v", result, err)
	}
	if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "[* 日本語]") {
		t.Fatalf("%+v", result)
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
}
