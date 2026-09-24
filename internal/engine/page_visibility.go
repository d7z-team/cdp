package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
)

func (p *Page) IsVisible(ctx context.Context, nodeID int) (bool, error) {
	result, err := p.EvalNodef(ctx, nodeID, "%s", elementPureVisibleScript)
	if err != nil {
		return false, err
	}

	if value, ok := SafeGet[bool](result, "result", "value"); ok {
		return value, nil
	}
	return false, nil
}

func (p *Page) IsEnabled(ctx context.Context, nodeID int) (bool, error) {
	code := `function() {
		const elem = this;
		if (!elem) return false;
		return !elem.disabled;
	}`
	result, err := p.EvalNodef(ctx, nodeID, "%s", code)
	if err != nil {
		return false, err
	}

	if value, ok := SafeGet[bool](result, "result", "value"); ok {
		return value, nil
	}
	return false, nil
}

const selectorImageDataScript = `function() {
		let img = this;
		if (img.tagName !== 'IMG') {
			const inner = img.querySelector('img');
			if (inner) img = inner;
			else throw new Error('Element is not an IMG');
		}

		if (!img.complete || img.naturalWidth === 0) {
			throw new Error('Image not loaded');
		}

		const canvas = document.createElement('canvas');
		canvas.width = img.naturalWidth;
		canvas.height = img.naturalHeight;
		const ctx = canvas.getContext('2d');
		ctx.drawImage(img, 0, 0);
		return canvas.toDataURL('image/png');
	}`

func imageBytesFromDataURLResult(result map[string]any) ([]byte, error) {
	if runtimeErr := runtimeResultError(result); runtimeErr != nil {
		return nil, runtimeErr
	}
	if value, ok := SafeGet[string](result, "result", "value"); ok {
		if idx := strings.Index(value, ","); idx != -1 {
			b64 := value[idx+1:]
			return base64.StdEncoding.DecodeString(b64)
		}
	}
	return nil, errors.New("获取图片数据失败")
}

func (p *Page) SelectorTargetImageData(ctx context.Context, ref SelectorTargetRef) ([]byte, error) {
	target, backendNodeID, err := p.selectorTargetTargetAndBackendNodeID(ctx, ref)
	if err != nil {
		return nil, err
	}
	result, err := p.callTargetBackendNodeFunctionContext(ctx, target, backendNodeID, true, selectorImageDataScript)
	if err != nil {
		return nil, err
	}
	return imageBytesFromDataURLResult(result)
}
