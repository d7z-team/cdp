package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"gopkg.d7z.net/cdp/internal/syncutil"
)

type CDPRequest struct {
	ID        int64          `json:"id,omitempty"`
	Method    string         `json:"method"`
	Params    map[string]any `json:"params,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
}

type CDPResponse struct {
	ID        int64          `json:"id,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`

	Method string         `json:"method"`
	Params map[string]any `json:"params"`
	Error  *CDPError      `json:"error,omitempty"`
}

func (r *CDPResponse) ParamsUnmarshal(s any) error {
	marshal, err := json.Marshal(r.Params)
	if err != nil {
		return err
	}
	return json.Unmarshal(marshal, s)
}

type CDPError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

var ErrBrowserClosed = errors.New("浏览器已经关闭")

func normalizeBrowserError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return ErrBrowserClosed
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusAbnormalClosure:
		return ErrBrowserClosed
	}
	return err
}

type CdpConn struct {
	context.Context
	conn *websocket.Conn

	idGroup *atomic.Int64

	call   *syncutil.SyncMap[int64, chan CDPResponse]
	event  *syncutil.PubSub[CDPResponse]
	events chan cdpEventQueueItem
}

type cdpEventQueueItem struct {
	response *CDPResponse
	barrier  chan struct{}
}

func (c *CdpConn) unavailableErr() error {
	if c == nil {
		return ErrBrowserClosed
	}
	if c.Context == nil || c.conn == nil || c.idGroup == nil || c.call == nil || c.event == nil {
		return ErrBrowserClosed
	}
	select {
	case <-c.Done():
		return ErrBrowserClosed
	default:
	}
	return nil
}

func NewCdpConn(ctx context.Context, conn *websocket.Conn, idGroup *atomic.Int64) (*CdpConn, <-chan struct{}) {
	ctx, cancel := context.WithCancel(ctx)
	c := &CdpConn{
		Context: ctx,
		conn:    conn,
		idGroup: idGroup,
		call:    syncutil.NewSyncMap[int64, chan CDPResponse](),
		event:   syncutil.NewPubSub[CDPResponse](nil),
		events:  make(chan cdpEventQueueItem, 256),
	}
	syncutil.Go(func() {
		for {
			select {
			case item := <-c.events:
				if item.barrier != nil {
					close(item.barrier)
					continue
				}
				if item.response != nil {
					c.event.Publish(*item.response)
				}
			case <-ctx.Done():
				return
			}
		}
	})
	ch := make(chan struct{}, 1)
	syncutil.Go(func() {
		defer cancel()
		defer func() {
			if c != nil && c.conn != nil {
				_ = c.conn.Close(websocket.StatusNormalClosure, "")
			}
			ch <- struct{}{}
			close(ch)
		}()
		if c.unavailableErr() != nil {
			return
		}
		for {
			var raw CDPResponse
			raw.ID = -1
			if err := wsjson.Read(ctx, c.conn, &raw); err != nil {
				err = normalizeBrowserError(err)
				// 正常关闭不打印错误
				if !errors.Is(err, ErrBrowserClosed) {
					slog.Error("cdpConn read error", "error", err)
				}
				break
			}

			if raw.ID != -1 {
				if value, loaded := c.call.LoadAndDelete(raw.ID); loaded {
					select {
					case value <- raw:
					default:
					}
					close(value)
				}
				continue
			}
			if raw.Method != "" {
				select {
				case c.events <- cdpEventQueueItem{response: &raw}:
				case <-ctx.Done():
					return
				}
			}
		}
	})
	return c, ch
}

// flushEvents waits until protocol events already read from the websocket have
// been published to subscribers. It does not query or mutate browser state.
func (c *CdpConn) flushEvents(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.unavailableErr(); err != nil {
		return err
	}
	barrier := make(chan struct{})
	select {
	case c.events <- cdpEventQueueItem{barrier: barrier}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.Done():
		return ErrBrowserClosed
	}
	select {
	case <-barrier:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.Done():
		return ErrBrowserClosed
	}
}

func (c *CdpConn) SendPacket(method string, params map[string]any) error {
	_, err := c.SendMessage(method, params)
	return err
}

func (c *CdpConn) SendMessage(method string, params map[string]any) (map[string]any, error) {
	if c == nil {
		return nil, ErrBrowserClosed
	}
	return c.SendMessageContext(c.Context, method, params)
}

func (c *CdpConn) SendMessageContext(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	return c.sendMessageContext(ctx, "", method, params)
}

func (c *CdpConn) SendSessionPacket(ctx context.Context, sessionID, method string, params map[string]any) error {
	_, err := c.SendSessionMessage(ctx, sessionID, method, params)
	return err
}

func (c *CdpConn) SendSessionMessage(ctx context.Context, sessionID, method string, params map[string]any) (map[string]any, error) {
	return c.sendMessageContext(ctx, sessionID, method, params)
}

func (c *CdpConn) sendMessageContext(ctx context.Context, sessionID, method string, params map[string]any) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.unavailableErr(); err != nil {
		return nil, err
	}
	conn := c.conn
	id := c.idGroup.Add(1)
	exit := make(chan CDPResponse, 1)
	c.call.Store(id, exit)
	defer func() {
		value, loaded := c.call.LoadAndDelete(id)
		if loaded {
			close(value)
		}
	}()
	// Canceling a caller must not cancel websocket I/O: the websocket library
	// closes the entire connection on an interrupted write. Dispatch uses a bounded
	// connection lifetime; the caller may stop waiting without replaying the packet.
	writeDone := make(chan error, 1)
	go func() {
		if err := ctx.Err(); err != nil {
			writeDone <- err
			return
		}
		writeCtx, cancel := context.WithTimeout(c.Context, 5*time.Second)
		defer cancel()
		writeDone <- wsjson.Write(writeCtx, conn, &CDPRequest{ID: id, Method: method, Params: params, SessionID: sessionID})
	}()
	var err error
	select {
	case err = <-writeDone:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.Done():
		return nil, ErrBrowserClosed
	}

	if err != nil {
		if ctx.Err() != nil {
			select {
			case <-c.Done():
				return nil, ErrBrowserClosed
			default:
				return nil, ctx.Err()
			}
		}
		return nil, normalizeBrowserError(err)
	}
	select {
	case data, ok := <-exit:
		if !ok {
			return nil, ErrBrowserClosed
		}
		if data.Error != nil {
			return nil, fmt.Errorf("%s: CDP 错误 (%d): %s", method, data.Error.Code, data.Error.Message)
		}
		return data.Result, nil
	case <-ctx.Done():
		select {
		case <-c.Done():
			return nil, ErrBrowserClosed
		default:
			return nil, ctx.Err()
		}
	case <-c.Done():
		return nil, ErrBrowserClosed
	}
}

// SafeGet 安全地从嵌套 map 中获取值并进行类型转换
// 示例: val, ok := SafeGet[string](result, "object", "objectId")
func SafeGet[T any](m map[string]any, keys ...string) (T, bool) {
	var zero T
	if m == nil {
		return zero, false
	}
	var current any = m
	for _, key := range keys {
		currMap, ok := current.(map[string]any)
		if !ok {
			return zero, false
		}
		current, ok = currMap[key]
		if !ok {
			return zero, false
		}
	}
	val, ok := current.(T)
	return val, ok
}

func (c *CdpConn) Event(method string, params map[string]any) (<-chan CDPResponse, func(), error) {
	if err := c.unavailableErr(); err != nil {
		return nil, func() {}, err
	}
	ctx, cancel := context.WithCancel(c.Context)
	// Event establishes a protocol state stream before enabling its CDP domain.
	// Unlike feature streams such as screencast frames, lifecycle events cannot
	// be coalesced without corrupting the consumer's state.
	watch := c.event.WatchWhereReliable(ctx, nil, nil)
	err := c.SendPacket(method, params)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return watch, cancel, nil
}

func (c *CdpConn) Subscribe(methods ...string) (<-chan CDPResponse, func()) {
	return c.subscribe(false, methods...)
}

func (c *CdpConn) SubscribeReliable(methods ...string) (<-chan CDPResponse, func()) {
	return c.subscribe(true, methods...)
}

func (c *CdpConn) subscribe(reliable bool, methods ...string) (<-chan CDPResponse, func()) {
	if c == nil || c.Context == nil || c.event == nil {
		ch := make(chan CDPResponse)
		close(ch)
		return ch, func() {}
	}
	ctx, cancel := context.WithCancel(c.Context)
	methodSet := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		if method != "" {
			methodSet[method] = struct{}{}
		}
	}
	predicate := func(resp CDPResponse) bool {
		if len(methodSet) == 0 {
			return true
		}
		_, ok := methodSet[resp.Method]
		return ok
	}
	var watch <-chan CDPResponse
	if reliable {
		watch = c.event.WatchWhereReliable(ctx, predicate, nil)
	} else {
		watch = c.event.WatchWhere(ctx, predicate, nil)
	}
	return watch, cancel
}

func (c *CdpConn) SendPacketContext(ctx context.Context, method string, params map[string]any) error {
	_, err := c.SendMessageContext(ctx, method, params)
	return err
}
