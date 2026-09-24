package engine

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ScreenConfig is the expected CSS display geometry for a launched browser.
type ScreenConfig struct {
	Width, Height int
	Scale         float64
}

func (r *BrowserManager) verifyScreen(ctx context.Context, config ScreenConfig) (err error) {
	if config.Width == 0 {
		return nil
	}
	target, err := r.conn.SendMessageContext(ctx, "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		return err
	}
	targetID, _ := target["targetId"].(string)
	defer func() {
		cleanup, cancel := context.WithTimeout(r.ctx, 5*time.Second)
		defer cancel()
		_, closeErr := r.conn.SendMessageContext(cleanup, "Target.closeTarget", map[string]any{"targetId": targetID})
		err = errors.Join(err, closeErr)
	}()
	attached, err := r.conn.SendMessageContext(ctx, "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true})
	if err != nil {
		return err
	}
	sessionID, _ := attached["sessionId"].(string)
	res, err := r.conn.SendSessionMessage(ctx, sessionID, "Runtime.evaluate", map[string]any{
		"expression": `({screen:{width:screen.width,height:screen.height,scale:devicePixelRatio}})`, "awaitPromise": true, "returnByValue": true,
	})
	if err != nil {
		return err
	}
	result, _ := res["result"].(map[string]any)
	value, _ := result["value"].(map[string]any)
	if value == nil {
		return errors.New("browser environment probe returned no value")
	}
	screen, _ := value["screen"].(map[string]any)
	if screen["width"] != float64(config.Width) || screen["height"] != float64(config.Height) || screen["scale"] != config.Scale {
		return fmt.Errorf("browser did not apply requested virtual screen: %v", screen)
	}
	return nil
}
