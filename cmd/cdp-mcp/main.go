// Command mcp starts the CDP-backed Model Context Protocol server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.d7z.net/cdp"
	"gopkg.d7z.net/cdp/internal/mcpapp"
	"gopkg.d7z.net/cdp/mcpserver"
)

const (
	defaultServerPort      = 3000
	serverShutdownTimeout  = 3 * time.Second
	browserShutdownTimeout = 5 * time.Second
)

type serverConfig struct {
	diagnostics string
	host        string
	port        int
	noHeadless  bool
	browserPath string
	userDataDir string
	windowSize  cdp.WindowSize
	screen      cdp.ScreenOptions
	maxTabs     int
	httpOptions mcpapp.HTTPOptions
}

type stringListFlag []string

func (values *stringListFlag) String() string { return strings.Join(*values, ",") }

func (values *stringListFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func parseConfig(args []string) (serverConfig, error) {
	config := serverConfig{}
	var corsOrigins stringListFlag
	var windowSize, screenSize string
	flags := flag.NewFlagSet("cdp-mcp", flag.ContinueOnError)
	flags.StringVar(&config.diagnostics, "diagnostics", "off", "Runtime diagnostics: off or runtime")
	flags.StringVar(&config.host, "host", "127.0.0.1", "HTTP server listen address")
	flags.IntVar(&config.port, "port", defaultServerPort, "HTTP server listen port")
	flags.BoolVar(&config.noHeadless, "no-headless", false, "Show browser window (disable headless mode)")
	flags.StringVar(&config.browserPath, "browser-path", "", "Browser executable path (auto-detect if empty)")
	flags.StringVar(&config.userDataDir, "user-data-dir", "", "Browser user data directory (default ~/.config/browser-mcp)")
	flags.StringVar(&windowSize, "window-size", "1440,960", "Browser window size width,height")
	flags.StringVar(&screenSize, "screen-size", "", "Headless screen width,height (default at least 1920,1080)")
	flags.Float64Var(&config.screen.ScaleFactor, "screen-scale", 0, "Headless screen scale (default 1)")
	flags.IntVar(&config.maxTabs, "max-tabs", 10, "Maximum number of open tabs")
	flags.Var(&corsOrigins, "cors-origin", "Allowed browser Origin; repeat for multiple origins, or use '*' for insecure testing")
	if err := flags.Parse(args); err != nil {
		return serverConfig{}, err
	}
	for _, size := range []struct {
		raw           string
		width, height *int
	}{{windowSize, &config.windowSize.Width, &config.windowSize.Height}, {screenSize, &config.screen.Width, &config.screen.Height}} {
		if size.raw == "" {
			continue
		}
		w, h, ok := strings.Cut(size.raw, ",")
		width, e1 := strconv.Atoi(w)
		height, e2 := strconv.Atoi(h)
		if !ok || e1 != nil || e2 != nil || width <= 0 || height <= 0 {
			return serverConfig{}, fmt.Errorf("invalid size %q", size.raw)
		}
		*size.width = width
		*size.height = height
	}
	if config.diagnostics != "off" && config.diagnostics != "runtime" {
		return serverConfig{}, fmt.Errorf("diagnostics must be off or runtime")
	}
	if config.port < 1 || config.port > 65535 {
		return serverConfig{}, fmt.Errorf("port must be between 1 and 65535")
	}
	if config.maxTabs < 1 {
		return serverConfig{}, fmt.Errorf("max-tabs must be at least 1")
	}
	var err error
	config.httpOptions, err = mcpapp.NormalizeHTTPOptions(mcpapp.HTTPOptions{AllowedOrigins: corsOrigins})
	if err != nil {
		return serverConfig{}, err
	}
	return config, nil
}

func (config serverConfig) listenAddress() string {
	return net.JoinHostPort(config.host, strconv.Itoa(config.port))
}

func run(ctx context.Context, config serverConfig) error {
	listener, err := net.Listen("tcp", config.listenAddress())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", config.listenAddress(), err)
	}

	if config.userDataDir == "" {
		dir, dirErr := os.UserConfigDir()
		if dirErr != nil {
			_ = listener.Close()
			return dirErr
		}
		config.userDataDir = filepath.Join(dir, "browser-mcp")
	}
	browser, err := cdp.Launch(ctx, cdp.LaunchOptions{Logger: slog.Default(), Diagnostics: cdp.DiagnosticsMode(config.diagnostics), Headful: config.noHeadless, ExecutablePath: config.browserPath, UserDataDir: config.userDataDir, WindowSize: config.windowSize, Screen: config.screen})
	if err != nil {
		_ = listener.Close()
		return err
	}

	mcp, err := mcpserver.New(browser, mcpserver.Options{AllowedOrigins: config.httpOptions.AllowedOrigins, MaxTabs: config.maxTabs, EnableDebug: true})
	if err != nil {
		_ = listener.Close()
		browserCtx, browserCancel := context.WithTimeout(context.WithoutCancel(ctx), browserShutdownTimeout)
		_ = browser.Shutdown(browserCtx)
		browserCancel()
		return fmt.Errorf("configure MCP HTTP server: %w", err)
	}
	defer mcp.Close()
	server := &http.Server{Handler: mcp.Handler()}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	log.Printf("MCP server started on http://%s/mcp (debug: http://%s/debug)", listener.Addr(), listener.Addr())
	if len(config.httpOptions.AllowedOrigins) > 0 {
		log.Printf("MCP CORS origins: %s", strings.Join(config.httpOptions.AllowedOrigins, ", "))
	}
	if len(config.httpOptions.AllowedOrigins) == 1 && config.httpOptions.AllowedOrigins[0] == "*" {
		log.Printf("WARNING: MCP CORS wildcard is enabled; any browser origin can control the shared browser")
	}
	host := strings.Trim(config.host, "[]")
	if ip := net.ParseIP(host); !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		log.Printf("WARNING: MCP server is listening on a non-loopback address without authentication; CORS is not access control")
	}

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serveDone:
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), serverShutdownTimeout)
	shutdownErr := server.Shutdown(shutdownCtx)
	shutdownCancel()

	browserCtx, browserCancel := context.WithTimeout(context.WithoutCancel(ctx), browserShutdownTimeout)
	mcpErr := mcp.Close()
	browserErr := browser.Shutdown(browserCtx)
	browserCancel()

	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", serveErr)
	}
	return errors.Join(shutdownErr, mcpErr, browserErr)
}

func main() {
	config, err := parseConfig(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Printf("mcp server stopped: %v", err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, config); err != nil {
		log.Printf("mcp server stopped: %v", err)
		os.Exit(1)
	}
}
