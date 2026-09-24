package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func (l *Locator) Attribute(ctx context.Context, name string) (string, bool, error) {
	c, cancel, err := l.operation(ctx, l.timeouts().Read)
	if err != nil {
		return "", false, err
	}
	defer cancel()
	e, err := l.resolve(c, "")
	if err != nil {
		return "", false, err
	}
	value, exists, err := e.Attribute(c, name)
	return value, exists, l.wrapError("attribute", err)
}
func (l *Locator) Eval(ctx context.Context, script string, result any) error {
	c, cancel, err := l.operation(ctx, l.timeouts().Read)
	if err != nil {
		return l.wrapError("eval", err)
	}
	defer cancel()
	e, err := l.resolve(c, "")
	if err != nil {
		return l.wrapError("eval", err)
	}
	return l.wrapError("eval", e.Eval(c, script, result))
}
func (l *Locator) EvalJSON(ctx context.Context, script string) (json.RawMessage, error) {
	var v json.RawMessage
	err := l.Eval(ctx, script, &v)
	return v, err
}
func (l *Locator) SetStyles(ctx context.Context, styles map[string]string) error {
	raw, _ := json.Marshal(styles)
	return l.Eval(ctx, "const styles="+string(raw)+";for(const [name,value] of Object.entries(styles)){this.style.setProperty(name,value)}", nil)
}
func (l *Locator) Upload(ctx context.Context, name string, data []byte) error {
	dir, err := os.MkdirTemp("", "cdp-upload-*")
	if err != nil {
		return l.wrapError("upload", err)
	}
	defer os.RemoveAll(dir)
	file, err := os.Create(filepath.Join(dir, filepath.Base(name)))
	if err != nil {
		return l.wrapError("upload", err)
	}
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return l.wrapError("upload", err)
	}
	if closeErr != nil {
		return closeErr
	}
	return l.SetFiles(ctx, []string{file.Name()})
}
func (l *Locator) UploadURL(ctx context.Context, name, url string) error {
	c, cancel, err := l.operation(ctx, l.timeouts().Action)
	if err != nil {
		return l.wrapError("upload_url", err)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, url, nil)
	if err != nil {
		return l.wrapError("upload_url", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return l.wrapError("upload_url", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload source: %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return l.wrapError("upload_url", err)
	}
	return l.Upload(c, name, data)
}

type Direction string

const (
	DirectionDown  Direction = "down"
	DirectionUp    Direction = "up"
	DirectionLeft  Direction = "left"
	DirectionRight Direction = "right"
)

func (l *Locator) ScrollNext(ctx context.Context, direction Direction) (bool, error) {
	c, cancel, err := l.operation(ctx, l.timeouts().Action)
	if err != nil {
		return false, l.wrapError("scroll_next", err)
	}
	defer cancel()
	e, err := l.resolve(c, "")
	if err != nil {
		return false, l.wrapError("scroll_next", err)
	}
	axis, step := "y", 1
	switch direction {
	case DirectionDown:
	case DirectionUp:
		step = -1
	case DirectionLeft:
		axis, step = "x", -1
	case DirectionRight:
		axis = "x"
	default:
		return false, fmt.Errorf("invalid scroll direction %q", direction)
	}
	moved, err := e.page.engine.SelectorTargetScrollNext(c, e.ref, axis, step)
	return moved, l.wrapError("scroll_next", err)
}
func (l *Locator) SurroundingHTML(ctx context.Context, levels int) (string, error) {
	var v string
	err := l.Eval(ctx, fmt.Sprintf("let element=this;for(let i=0;i<%d&&element.parentElement;i++)element=element.parentElement;return element.outerHTML", max(0, levels)), &v)
	return v, err
}
func (l *Locator) ChildHTML(ctx context.Context, maxDepth int) (string, error) {
	var v string
	err := l.Eval(ctx, fmt.Sprintf("const clone=this.cloneNode(true);const trim=(node,depth)=>{if(depth>=%d){for(const child of Array.from(node.children))child.remove();return};for(const child of node.children)trim(child,depth+1)};trim(clone,0);return clone.outerHTML", max(0, maxDepth)), &v)
	return v, err
}

func (l *Locator) ScrollIntoViewAt(ctx context.Context, x, y float64) error {
	if x < -1 || x > 1 || y < -1 || y > 1 {
		return fmt.Errorf("alignment outside [-1,1]")
	}
	c, cancel, err := l.operation(ctx, l.timeouts().Action)
	if err != nil {
		return l.wrapError("scroll_into_view_at", err)
	}
	defer cancel()
	e, err := l.resolve(c, "")
	if err != nil {
		return l.wrapError("scroll_into_view_at", err)
	}
	return operationError("locator.scroll_align", e.page.engine.SelectorTargetScrollTo(c, e.ref, x, y))
}
