// Package launcher starts and controls a Chrome process with a CDP endpoint.
package launcher

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"gopkg.d7z.net/cdp/internal/pageurl"
)

// Browser controls one Chrome profile and the process started for it.
type Browser struct {
	ctx     context.Context
	config  Config
	extPath string

	mu             sync.Mutex
	process        *os.Process
	processDone    chan error
	endpointWait   chan struct{}
	processExitErr error
	processExited  bool
}

type browserVersionInfo struct {
	Browser              string `json:"Browser"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type targetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
}

type cdpRequest struct {
	ID     int64          `json:"id,omitempty"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type cdpResponse struct {
	ID     int64          `json:"id,omitempty"`
	Error  map[string]any `json:"error,omitempty"`
	Result map[string]any `json:"result,omitempty"`
}

// NewBrowser 创建一个基于给定配置的浏览器实例控制器。
//
// 该函数只校验配置和浏览器路径，不会启动浏览器进程。
func NewBrowser(ctx context.Context, config Config) (*Browser, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if config.ChromeExecutable == "" {
		config.ChromeExecutable = findBrowserPath()
	}
	if _, err := os.Stat(config.ChromeExecutable); err != nil {
		return nil, err
	}

	browser := &Browser{
		ctx:     ctx,
		config:  config,
		extPath: filepath.Join(config.ChromeUserDir, "default-extensions"),
	}
	return browser, nil
}

// EnsureEndpoint 返回当前实例的 CDP HTTP 地址。
//
// 如果对应的浏览器实例尚未运行，EnsureEndpoint 会先启动浏览器，
// 然后轮询 DevToolsActivePort 和 /json/version，最多等待 20 秒。
func (b *Browser) EnsureEndpoint() (string, error) {
	return b.EnsureEndpointContext(b.ctx)
}

// EnsureEndpointContext serializes startup and bounds probing by the caller's
// context and a 20 second startup budget. A slow live endpoint is not relaunched.
func (b *Browser) EnsureEndpointContext(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if b.ctx != nil {
		stop := context.AfterFunc(b.ctx, cancel)
		defer stop()
	}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		b.mu.Lock()
		pending := b.endpointWait
		if pending == nil {
			b.endpointWait = make(chan struct{})
			b.mu.Unlock()
			break
		}
		b.mu.Unlock()
		select {
		case <-pending:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	defer func() {
		b.mu.Lock()
		close(b.endpointWait)
		b.endpointWait = nil
		b.mu.Unlock()
	}()

	b.mu.Lock()
	launched := b.process != nil && !b.processExited
	b.mu.Unlock()
	var lastErr error
	for {
		endpoint, err := b.EndpointContext(ctx)
		if err == nil {
			return endpoint, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return "", fmt.Errorf("wait for browser CDP endpoint: %w", errors.Join(ctx.Err(), lastErr))
		}
		if launched {
			b.mu.Lock()
			exitErr := b.processExitErr
			b.mu.Unlock()
			if exitErr != nil {
				return "", fmt.Errorf("browser exited before CDP became ready: %w", errors.Join(exitErr, lastErr))
			}
		}
		if !launched && (errors.Is(err, os.ErrNotExist) || errors.Is(err, connectionRefusedError)) {
			if err := b.launchDetached(); err != nil {
				return "", fmt.Errorf("launch browser: %w", err)
			}
			launched = true
		} else if !launched && !errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("probe browser CDP endpoint: %w", err)
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return "", fmt.Errorf("wait for browser CDP endpoint: %w", errors.Join(ctx.Err(), lastErr))
		}
	}
}

// Endpoint 返回当前已运行实例的 CDP HTTP 地址。
//
// 该方法只做探测，不会隐式启动浏览器。
func (b *Browser) Endpoint() (string, error) {
	return b.EndpointContext(b.ctx)
}

// MustEndpoint 与 Endpoint 类似，但在探测失败时直接 panic。
func (b *Browser) MustEndpoint() string {
	endpoint, err := b.Endpoint()
	if err != nil {
		panic(err)
	}
	return endpoint
}

// IsRunning 报告当前 user-data-dir 对应的浏览器实例是否已可通过 CDP 访问。
func (b *Browser) IsRunning() bool {
	_, err := b.Endpoint()
	return err == nil
}

// Activate 确保浏览器实例已启动，并将首个可用 page target 激活到前台。
func (b *Browser) Activate() error {
	endpoint, err := b.EnsureEndpoint()
	if err != nil {
		return err
	}

	targets, err := b.getTargets(endpoint)
	if err != nil {
		return err
	}

	var executableTargetID string
	var placeholderTargetID string
	for _, target := range targets {
		if target.Type != "page" {
			continue
		}
		targetID := strings.TrimSpace(target.TargetID)
		if targetID == "" {
			continue
		}
		switch pageurl.ClassifyPageURL(target.URL) {
		case pageurl.PageURLExecutable:
			executableTargetID = targetID
		case pageurl.PageURLPlaceholder:
			if placeholderTargetID == "" {
				placeholderTargetID = targetID
			}
		case pageurl.PageURLInternal:
			continue
		}
		if executableTargetID != "" {
			break
		}
	}
	targetID := executableTargetID
	if targetID == "" {
		targetID = placeholderTargetID
	}
	if targetID == "" {
		return errors.New("未找到可激活的页面目标")
	}

	_, err = b.sendBrowserCommand(endpoint, "Target.activateTarget", map[string]any{
		"targetId": targetID,
	})
	return err
}

// Kill 通过 browser-level CDP 的 Browser.close 命令关闭当前浏览器实例。
//
// 如果实例当前未运行，Kill 会直接返回 nil。
func (b *Browser) Kill() error {
	return b.KillContext(b.ctx)
}

// KillContext closes the browser and waits for its owned process to exit.
func (b *Browser) KillContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint, err := b.EndpointContext(ctx)
	if err != nil {
		return b.waitOwnedProcessExit(ctx, true)
	}

	closeErr := b.closeBrowserByCDP(ctx, endpoint)
	gracefulCtx, cancel := context.WithTimeout(ctx, time.Second)
	endpointDownErr := b.waitEndpointDown(gracefulCtx, endpoint)
	cancel()
	if endpointDownErr == nil {
		processCtx, processCancel := context.WithTimeout(ctx, time.Second)
		processErr := b.waitOwnedProcessExit(processCtx, false)
		processCancel()
		if processErr == nil {
			return closeErr
		}
		return errors.Join(closeErr, b.waitOwnedProcessExit(ctx, true))
	}

	if killErr := b.waitOwnedProcessExit(ctx, true); killErr != nil {
		return errors.Join(closeErr, killErr)
	}
	if err := b.waitEndpointDown(ctx, endpoint); err != nil {
		return errors.Join(closeErr, err)
	}
	return closeErr
}

func (b *Browser) closeBrowserByCDP(ctx context.Context, endpoint string) error {
	_, err := b.sendBrowserCommandContext(ctx, endpoint, "Browser.close", nil)
	if err == nil {
		return nil
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusAbnormalClosure:
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("close browser by CDP: %w", err)
}

func (b *Browser) waitEndpointDown(ctx context.Context, endpoint string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := b.probeEndpointContext(ctx, endpoint); err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait browser endpoint down: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (b *Browser) waitOwnedProcessExit(ctx context.Context, force bool) error {
	process, done := b.ownedProcess()
	if process == nil || done == nil {
		return nil
	}
	if force {
		select {
		case err := <-done:
			b.clearOwnedProcess(process)
			return normalizeProcessExitError(err, false)
		default:
		}
		if err := killBrowserProcess(process); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill browser process: %w", err)
		}
		select {
		case err := <-done:
			b.clearOwnedProcess(process)
			return normalizeProcessExitError(err, true)
		case <-ctx.Done():
			return fmt.Errorf("wait killed browser process exit: %w", ctx.Err())
		}
	}
	select {
	case err := <-done:
		b.clearOwnedProcess(process)
		return normalizeProcessExitError(err, false)
	case <-ctx.Done():
		return fmt.Errorf("wait browser process exit: %w", ctx.Err())
	}
}

func (b *Browser) ownedProcess() (*os.Process, <-chan error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.process, b.processDone
}

func (b *Browser) clearOwnedProcess(process *os.Process) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.process == process {
		b.process = nil
		b.processDone = nil
	}
}

func normalizeProcessExitError(err error, killed bool) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if killed && errors.As(err, &exitErr) {
		return nil
	}
	return err
}

// EndpointContext probes the existing endpoint without starting a browser.
func (b *Browser) EndpointContext(ctx context.Context) (string, error) {
	port, err := b.readDevToolsActivePort()
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := b.probeEndpointContext(ctx, endpoint); err != nil {
		return "", err
	}
	return endpoint, nil
}

func (b *Browser) readDevToolsActivePort() (int, error) {
	file, err := os.Open(filepath.Join(b.config.ChromeUserDir, "DevToolsActivePort"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()

	reader := bufio.NewReader(file)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return 0, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, errors.New("DevToolsActivePort 内容为空")
	}

	port, err := strconv.Atoi(line)
	if err != nil || port <= 0 {
		return 0, fmt.Errorf("无效的 DevToolsActivePort 端口: %q", line)
	}
	return port, nil
}

func (b *Browser) probeEndpointContext(ctx context.Context, endpoint string) error {
	_, err := b.browserVersionContext(ctx, endpoint)
	return err
}

func (b *Browser) browserVersionContext(ctx context.Context, endpoint string) (*browserVersionInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/json/version", nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("浏览器返回错误: %s", response.Status)
	}

	var info browserVersionInfo
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		return nil, err
	}
	if strings.TrimSpace(info.WebSocketDebuggerURL) == "" {
		return nil, errors.New("webSocketDebuggerUrl 为空")
	}
	return &info, nil
}

func (b *Browser) getTargets(endpoint string) ([]targetInfo, error) {
	result, err := b.sendBrowserCommand(endpoint, "Target.getTargets", nil)
	if err != nil {
		return nil, err
	}
	rawTargets, ok := result["targetInfos"]
	if !ok {
		return nil, errors.New("Target.getTargets 返回缺少 targetInfos")
	}
	rawJSON, err := json.Marshal(rawTargets)
	if err != nil {
		return nil, err
	}
	var targets []targetInfo
	if err := json.Unmarshal(rawJSON, &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (b *Browser) sendBrowserCommand(endpoint string, method string, params map[string]any) (map[string]any, error) {
	return b.sendBrowserCommandContext(b.ctx, endpoint, method, params)
}

func (b *Browser) sendBrowserCommandContext(ctx context.Context, endpoint string, method string, params map[string]any) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	info, err := b.browserVersionContext(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(info.WebSocketDebuggerURL) == "" {
		return nil, errors.New("未找到浏览器 websocket 调试地址")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, info.WebSocketDebuggerURL, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	if err := wsjson.Write(ctx, conn, cdpRequest{
		ID:     1,
		Method: method,
		Params: params,
	}); err != nil {
		return nil, err
	}

	for {
		var resp cdpResponse
		if err := wsjson.Read(ctx, conn, &resp); err != nil {
			return nil, err
		}
		if resp.ID != 1 {
			continue
		}
		if resp.Error != nil {
			raw, _ := json.Marshal(resp.Error)
			return nil, fmt.Errorf("CDP %s 失败: %s", method, strings.TrimSpace(string(raw)))
		}
		return resp.Result, nil
	}
}

func (b *Browser) launchDetached() error {
	if err := b.prepareExtensions(); err != nil {
		return err
	}

	args, err := b.buildLaunchArgs()
	if err != nil {
		return err
	}

	cmd := exec.Command(b.config.ChromeExecutable, args...)
	cmd.SysProcAttr = browserAttr()

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = devNull.Close() }()
	devNullRead, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer func() { _ = devNullRead.Close() }()
	cmd.Stdin = devNullRead
	cmd.Stdout = devNull
	cmd.Stderr = devNull

	if err := cmd.Start(); err != nil {
		return err
	}
	if cmd.Process != nil {
		done := make(chan error, 1)
		b.mu.Lock()
		b.process = cmd.Process
		b.processDone = done
		b.processExitErr = nil
		b.processExited = false
		b.mu.Unlock()
		go func() {
			err := cmd.Wait()
			b.mu.Lock()
			if b.process == cmd.Process {
				b.processExitErr = err
				b.processExited = true
			}
			b.mu.Unlock()
			done <- err
		}()
	}
	return nil
}

func (b *Browser) prepareExtensions() error {
	if err := os.MkdirAll(b.config.ChromeUserDir, os.ModePerm); err != nil {
		return err
	}
	if err := os.RemoveAll(b.extPath); err != nil {
		return err
	}
	if len(b.config.ChromeHooks) == 0 {
		return nil
	}
	for i, hook := range b.config.ChromeHooks {
		extPath := filepath.Join(b.extPath, "plugin_"+strconv.Itoa(i))
		if err := os.MkdirAll(extPath, os.ModePerm); err != nil {
			return err
		}
		ext := map[string]any{"manifest_version": 3, "name": "CDP extension", "version": "1.0.0"}
		if err := hook(ext, extPath); err != nil {
			return err
		}
		data, err := json.MarshalIndent(ext, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(extPath, "manifest.json"), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (b *Browser) buildLaunchArgs() ([]string, error) {
	customArgs := make([]string, 0, len(b.config.CustomArgs))
	disableBlinkFeatures := []string{"AutomationControlled"}
	for _, arg := range b.config.CustomArgs {
		if strings.HasPrefix(arg, "--remote-debugging-port=") ||
			strings.HasPrefix(arg, "--remote-debugging-address=") {
			return nil, fmt.Errorf("CustomArgs 不允许覆盖 remote debugging 参数: %s", arg)
		}
		if strings.HasPrefix(arg, "--enable-blink-features=") && chromeFeatureArgContains(arg, "AutomationControlled") {
			return nil, fmt.Errorf("CustomArgs 不允许启用 AutomationControlled: %s", arg)
		}
		if strings.HasPrefix(arg, "--disable-blink-features=") {
			disableBlinkFeatures = mergeChromeFeatureList(disableBlinkFeatures, strings.TrimPrefix(arg, "--disable-blink-features="))
			continue
		}
		customArgs = append(customArgs, arg)
	}

	plugins := make([]string, 0, len(b.config.ChromeHooks))
	for i := range b.config.ChromeHooks {
		plugins = append(plugins, filepath.Join(b.extPath, "plugin_"+strconv.Itoa(i)))
	}

	hosts := make([]string, 0, len(b.config.FakeHosts))
	for host, addr := range b.config.FakeHosts {
		hosts = append(hosts, fmt.Sprintf("MAP %s %s", host, addr))
	}

	args := []string{
		"--remote-debugging-port=0",
		"--remote-debugging-address=127.0.0.1",
		"--close-last-tab-exit",
		"--user-data-dir=" + b.config.ChromeUserDir,
		"--no-first-run",
		"--disable-blink-features=" + strings.Join(disableBlinkFeatures, ","),
	}
	if len(hosts) > 0 {
		args = append(args, "--host-resolver-rules="+strings.Join(hosts, ","))
	}
	if len(b.config.CAFingerPrints) > 0 {
		args = append(args, "--ignore-certificate-errors-spki-list="+strings.Join(b.config.CAFingerPrints, ","))
	}
	args = append(args, customArgs...)
	if len(plugins) > 0 {
		args = append(args, "--load-extension="+strings.Join(plugins, ","))
	}
	return args, nil
}

func chromeFeatureArgContains(arg string, feature string) bool {
	value := arg
	if idx := strings.IndexByte(arg, '='); idx >= 0 {
		value = arg[idx+1:]
	}
	for _, part := range strings.Split(value, ",") {
		if strings.TrimSpace(part) == feature {
			return true
		}
	}
	return false
}

func mergeChromeFeatureList(base []string, raw string) []string {
	seen := make(map[string]struct{}, len(base)+4)
	merged := make([]string, 0, len(base)+4)
	for _, feature := range base {
		feature = strings.TrimSpace(feature)
		if feature == "" {
			continue
		}
		if _, ok := seen[feature]; ok {
			continue
		}
		seen[feature] = struct{}{}
		merged = append(merged, feature)
	}
	for _, feature := range strings.Split(raw, ",") {
		feature = strings.TrimSpace(feature)
		if feature == "" {
			continue
		}
		if _, ok := seen[feature]; ok {
			continue
		}
		seen[feature] = struct{}{}
		merged = append(merged, feature)
	}
	return merged
}

func findBrowserPath() string {
	if pathFromEnv := findInPath("chrome"); pathFromEnv != "" {
		return pathFromEnv
	}
	if pathFromEnv := findInPath("chromium"); pathFromEnv != "" {
		return pathFromEnv
	}
	if pathFromEnv := findInPath("chromium-browser"); pathFromEnv != "" {
		return pathFromEnv
	}
	if pathFromEnv := findInPath("google-chrome"); pathFromEnv != "" {
		return pathFromEnv
	}
	var possiblePaths []string
	switch runtime.GOOS {
	case "windows":
		possiblePaths = []string{
			"C:\\Program Files\\Chromium\\Application\\chrome.exe",
			"C:\\Program Files (x86)\\Chromium\\Application\\chrome.exe",
			os.Getenv("LOCALAPPDATA") + "\\Chromium\\Application\\chrome.exe",
			"C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
			"C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
			os.Getenv("LOCALAPPDATA") + "\\Google\\Chrome\\Application\\chrome.exe",
		}
	case "darwin":
		possiblePaths = []string{
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			os.Getenv("HOME") + "/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/usr/local/bin/chromium",
			"/opt/homebrew/bin/chromium",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			os.Getenv("HOME") + "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}

	case "linux":
		possiblePaths = []string{
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/local/bin/chromium",
			"/snap/bin/chromium",
			os.Getenv("HOME") + "/.local/bin/chromium",
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/opt/google/chrome/chrome",
			"/usr/bin/chrome",
			"/usr/bin/chrome-browser",
		}
	default:
		return "chrome"
	}
	for _, path := range possiblePaths {
		if fileExists(path) {
			return path
		}
	}
	return "chrome"
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

func findInPath(executable string) string {
	if path, err := exec.LookPath(executable); err == nil {
		return path
	}
	return ""
}

// OwnsProcess distinguishes a process started by this controller from a reused endpoint.
func (b *Browser) OwnsProcess() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.process != nil && !b.processExited
}

// KillOwnedContext never sends Browser.close to a borrowed endpoint.
func (b *Browser) KillOwnedContext(ctx context.Context) error {
	if !b.OwnsProcess() {
		return nil
	}
	return b.KillContext(ctx)
}
