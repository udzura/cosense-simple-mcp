package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/udzura/cosense-simple-mcp/internal/cosense"
	"github.com/udzura/cosense-simple-mcp/internal/server"
)

func main() {
	client, err := cosense.New(os.Getenv("COSENSE_PROJECT_NAME"), os.Getenv("COSENSE_SID"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.New(client).Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		// Protocol errors may include user input. Keep diagnostics content-free.
		fmt.Fprintln(os.Stderr, "MCP stdio session failed")
		os.Exit(1)
	}
}
