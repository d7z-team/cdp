package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
)

func normalizeBrowserEndpoints(raw string) (baseURL, browserWS string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("CDP 地址不能为空")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if parsed.Scheme == "" {
		parsed, err = url.Parse("http://" + raw)
		if err != nil {
			return "", "", err
		}
	}
	if parsed.Host == "" {
		return "", "", fmt.Errorf("无效的 CDP 地址: %s", raw)
	}

	basePath := strings.TrimSpace(parsed.Path)
	switch parsed.Scheme {
	case "ws", "wss":
		browserWS = parsed.String()
		parsed.Scheme = strings.Replace(parsed.Scheme, "ws", "http", 1)
		parsed.Path = ""
		parsed.RawPath = ""
		parsed.RawQuery = ""
		parsed.Fragment = ""
		baseURL = strings.TrimRight(parsed.String(), "/") + "/"
		return baseURL, browserWS, nil
	case "http", "https":
		if strings.Contains(basePath, "/devtools/browser/") {
			wsParsed := *parsed
			wsParsed.Scheme = strings.Replace(wsParsed.Scheme, "http", "ws", 1)
			wsParsed.RawQuery = ""
			wsParsed.Fragment = ""
			browserWS = wsParsed.String()
			parsed.Path = ""
			parsed.RawPath = ""
		} else if strings.HasSuffix(basePath, "/json/version") {
			parsed.Path = strings.TrimSuffix(basePath, "/json/version")
			parsed.RawPath = ""
		}
		parsed.RawQuery = ""
		parsed.Fragment = ""
		baseURL = strings.TrimRight(parsed.String(), "/") + "/"
		return baseURL, browserWS, nil
	default:
		return "", "", fmt.Errorf("不支持的 CDP 协议: %s", parsed.Scheme)
	}
}

func (r *BrowserManager) wsWithContexts(dialCtx, connCtx context.Context, path string) (*CdpConn, <-chan struct{}, error) {
	if r == nil || r.ctx == nil || r.client == nil {
		return nil, nil, ErrBrowserClosed
	}
	if dialCtx == nil {
		dialCtx = r.ctx
	}
	if connCtx == nil {
		connCtx = r.ctx
	}
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") &&
		!strings.HasPrefix(path, "ws://") && !strings.HasPrefix(path, "wss://") {
		path = fmt.Sprintf("%s%s", r.baseURL, strings.TrimPrefix(path, "/"))
	}
	path = strings.Replace(path, "http://", "ws://", 1)
	path = strings.Replace(path, "https://", "wss://", 1)

	dialContext, response, err := websocket.Dial(dialCtx, path, &websocket.DialOptions{
		HTTPClient: r.client,
	})
	if err != nil {
		if response != nil {
			defer func() { _ = response.Body.Close() }()
		}
		return nil, nil, err
	}
	dialContext.SetReadLimit(1024 * 1024 * 1024)
	conn, c := NewCdpConn(connCtx, dialContext, r.idGroup, r.Logger())
	return conn, c, nil
}

func (r *BrowserManager) HTTPGet(ctx context.Context, path string, data any) error {
	return r.httpRequest(ctx, "GET", path, nil, data)
}

func (r *BrowserManager) HTTPPut(ctx context.Context, path string, body io.Reader, data any) error {
	return r.httpRequest(ctx, "PUT", path, body, data)
}

func (r *BrowserManager) httpRequest(ctx context.Context, method, path string, body io.Reader, result any) error {
	if r == nil || r.client == nil {
		return ErrBrowserClosed
	}
	if !r.IsAlive() {
		return ErrBrowserClosed
	}
	if !strings.HasPrefix(path, "http") && !strings.HasPrefix(path, "https") {
		path = fmt.Sprintf("%s%s", r.baseURL, strings.TrimPrefix(path, "/"))
	}
	request, err := http.NewRequestWithContext(ctx, method, path, body)
	if err != nil {
		return err
	}
	response, err := r.client.Do(request)
	responseBody := make([]byte, 0)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
		responseBody, err = io.ReadAll(response.Body)
		if err != nil {
			return err
		}
	}
	if err != nil || response == nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !r.IsAlive() {
			return ErrBrowserClosed
		}
		if err == nil {
			return fmt.Errorf("请求失败: 响应为空 (%s)", string(responseBody))
		}
		return fmt.Errorf("请求失败: %w (%s)", err, string(responseBody))
	}
	if response.StatusCode >= 400 {
		return fmt.Errorf("浏览器返回错误: %s", string(responseBody))
	}
	return json.Unmarshal(responseBody, result)
}
