package mcpapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"gopkg.d7z.net/cdp"
)

var debugScreencastOptions = cdp.ScreencastOptions{Format: "jpeg", Quality: 72, MaxWidth: 1440, EveryNthFrame: 1}

type debugHub struct {
	service *Service
	catalog *toolCatalog
	started time.Time
	mu      sync.Mutex
	viewers map[string]*debugViewer
}

type debugViewer struct {
	mu     sync.Mutex
	count  int
	stream *cdp.Screencast
}

type debugFrameState struct {
	mu   sync.RWMutex
	info *debugFrameInfo
}

func (s *debugFrameState) store(info *debugFrameInfo) {
	if info == nil {
		return
	}
	copy := *info
	s.mu.Lock()
	s.info = &copy
	s.mu.Unlock()
}

func (s *debugFrameState) load() *debugFrameInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.info == nil {
		return nil
	}
	copy := *s.info
	return &copy
}

func newDebugHub(service *Service, catalog *toolCatalog) *debugHub {
	return &debugHub{service: service, catalog: catalog, started: time.Now(), viewers: map[string]*debugViewer{}}
}

func (hub *debugHub) attach(ctx context.Context, runtime *tabRuntime) error {
	hub.mu.Lock()
	viewer := hub.viewers[runtime.id]
	if viewer == nil {
		viewer = &debugViewer{}
		hub.viewers[runtime.id] = viewer
	}
	hub.mu.Unlock()

	viewer.mu.Lock()
	defer viewer.mu.Unlock()
	if viewer.count > 0 {
		viewer.count++
		return nil
	}
	stream, err := runtime.page.Screencast(ctx, debugScreencastOptions)
	viewer.stream = stream
	if err == nil {
		viewer.count = 1
	}
	return err
}

func (hub *debugHub) detach(runtime *tabRuntime) {
	hub.mu.Lock()
	viewer := hub.viewers[runtime.id]
	hub.mu.Unlock()
	if viewer == nil {
		return
	}

	viewer.mu.Lock()
	defer viewer.mu.Unlock()
	if viewer.count > 1 {
		viewer.count--
		return
	}
	if viewer.count == 1 {
		viewer.count = 0
		if viewer.stream != nil {
			_ = viewer.stream.Close()
		}
	}
}

func (hub *debugHub) viewerCount() int {
	hub.mu.Lock()
	viewers := make([]*debugViewer, 0, len(hub.viewers))
	for _, viewer := range hub.viewers {
		viewers = append(viewers, viewer)
	}
	hub.mu.Unlock()
	count := 0
	for _, viewer := range viewers {
		viewer.mu.Lock()
		count += viewer.count
		viewer.mu.Unlock()
	}
	return count
}

type debugOutbound struct {
	message debugMessage
	frame   []byte
}

func (hub *debugHub) stream(w http.ResponseWriter, r *http.Request) {
	tabID := r.URL.Query().Get("tab_id")
	runtime, err := hub.service.runtime(r.Context(), tabID)
	if err != nil {
		writeDebugError(w, http.StatusBadRequest, normalizeToolError("debug.stream", "", err))
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if err := hub.attach(ctx, runtime); err != nil {
		_ = conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer hub.detach(runtime)

	messages := make(chan debugOutbound, 64)
	frames := make(chan debugOutbound, 1)
	deliveredFrame := &debugFrameState{}
	writerDone := make(chan error, 1)
	go func() {
		defer cancel()
		for {
			var outbound debugOutbound
			select {
			case <-ctx.Done():
				writerDone <- ctx.Err()
				return
			case outbound = <-messages:
			case outbound = <-frames:
			}
			outbound.message.Timestamp = time.Now()
			payload, marshalErr := json.Marshal(outbound.message)
			if marshalErr != nil {
				writerDone <- marshalErr
				return
			}
			if writeErr := conn.Write(ctx, websocket.MessageText, payload); writeErr != nil {
				writerDone <- writeErr
				return
			}
			if len(outbound.frame) > 0 {
				if writeErr := conn.Write(ctx, websocket.MessageBinary, outbound.frame); writeErr != nil {
					writerDone <- writeErr
					return
				}
				deliveredFrame.store(outbound.message.Frame)
			}
		}
	}()

	send := func(message debugMessage) {
		select {
		case messages <- debugOutbound{message: message}:
		case <-ctx.Done():
		}
	}
	if status, statusErr := hub.service.debugStatus(ctx, tabID); statusErr == nil {
		send(debugMessage{Type: "state", State: &status})
	}

	go hub.streamFrames(ctx, cancel, runtime, frames, send)
	go hub.streamEvents(ctx, runtime, send)

	for {
		messageType, payload, readErr := conn.Read(ctx)
		if readErr != nil {
			break
		}
		if messageType != websocket.MessageText {
			continue
		}
		var command debugCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			send(debugMessage{Type: "result", ID: command.ID, Error: normalizeToolError("debug.command", "", err)})
			continue
		}
		if command.Type == "control" {
			output := hub.service.debugControl(ctx, tabID, command, deliveredFrame)
			send(debugMessage{Type: "result", ID: command.ID, OK: output.OK, Output: output, Error: output.Error})
			continue
		}
		if command.Type != "tool" {
			err := NewToolError("invalid_argument", "debug.command", "type must be tool or control", nil)
			send(debugMessage{Type: "result", ID: command.ID, Error: err})
			continue
		}
		arguments := command.Arguments
		var values map[string]any
		if len(arguments) == 0 {
			values = map[string]any{}
		} else if err := json.Unmarshal(arguments, &values); err != nil {
			send(debugMessage{Type: "result", ID: command.ID, Error: normalizeToolError("debug.command", "", err)})
			continue
		}
		if values == nil {
			values = map[string]any{}
		}
		if _, exists := values["tab_id"]; !exists {
			values["tab_id"] = tabID
		}
		arguments, _ = json.Marshal(values)
		invocation, invokeErr := hub.catalog.invoke(ctx, command.Name, arguments)
		if invokeErr != nil {
			send(debugMessage{Type: "result", ID: command.ID, Error: normalizeToolError("debug.tool."+command.Name, "", invokeErr)})
			continue
		}
		ok := invocation.Result == nil || !invocation.Result.IsError
		send(debugMessage{Type: "result", ID: command.ID, OK: ok, Output: invocation.Output})
	}
	cancel()
	select {
	case <-writerDone:
	case <-time.After(500 * time.Millisecond):
	}
}

func (hub *debugHub) streamFrames(ctx context.Context, cancel context.CancelFunc, runtime *tabRuntime, frames chan debugOutbound, send func(debugMessage)) {
	var sequence uint64
	for {
		hub.mu.Lock()
		viewer := hub.viewers[runtime.id]
		hub.mu.Unlock()
		viewer.mu.Lock()
		stream := viewer.stream
		viewer.mu.Unlock()
		frame, err := stream.Next(ctx, sequence)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, cdp.ErrClosed) {
				send(debugMessage{Type: "error", Error: normalizeToolError("debug.stream.frame", "", err)})
			}
			cancel()
			return
		}
		sequence = frame.Sequence
		config, _, err := image.DecodeConfig(bytes.NewReader(frame.Data))
		if err != nil {
			continue
		}
		scale := frame.Metadata.PageScaleFactor
		if scale <= 0 {
			scale = 1
		}
		cssWidth := frame.Metadata.DeviceWidth / scale
		cssHeight := frame.Metadata.DeviceHeight / scale
		if cssWidth <= 0 {
			cssWidth = float64(config.Width) / scale
		}
		if cssHeight <= 0 {
			cssHeight = float64(config.Height) / scale
		}
		info := &debugFrameInfo{
			Sequence: frame.Sequence, ImageWidth: config.Width, ImageHeight: config.Height,
			CSSViewportWidth: cssWidth, CSSViewportHeight: cssHeight,
			PageScaleFactor: scale,
		}
		outbound := debugOutbound{message: debugMessage{Type: "frame", Frame: info}, frame: frame.Data}
		if !queueLatestFrame(ctx, frames, outbound) {
			return
		}
	}
}

func queueLatestFrame(ctx context.Context, frames chan debugOutbound, outbound debugOutbound) bool {
	select {
	case frames <- outbound:
		return true
	default:
	}
	select {
	case <-frames:
	default:
	}
	select {
	case frames <- outbound:
		return true
	case <-ctx.Done():
		return false
	}
}

func (hub *debugHub) streamEvents(ctx context.Context, runtime *tabRuntime, send func(debugMessage)) {
	notifications, unsubscribe := runtime.observer.subscribe()
	defer unsubscribe()
	var cursor uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-notifications:
			if status, err := hub.service.debugStatus(ctx, runtime.id); err == nil {
				send(debugMessage{Type: "state", State: &status})
				if ctx.Err() != nil {
					return
				}
			}
			for {
				events, next := runtime.observer.events.after(cursor, 100, func(Event) bool { return true })
				cursor = next
				if len(events) > 0 {
					send(debugMessage{Type: "events", Events: events})
					if ctx.Err() != nil {
						return
					}
				}
				if len(events) < 100 {
					break
				}
			}
		}
	}
}
