// Command mcp-http embeds MCP in an application-owned HTTP server.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/mcpserver"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() (err error) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	browser, err := cdp.Launch(ctx, cdp.LaunchOptions{})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, browser.Close()) }()
	mcp, err := mcpserver.New(browser, mcpserver.Options{MaxTabs: 10})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, mcp.Close()) }()
	server := &http.Server{Addr: "127.0.0.1:3000", Handler: mcp.Handler(), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}
