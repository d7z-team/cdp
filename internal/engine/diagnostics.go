package engine

import "context"

// IsRuntimeDiagnosticEvent identifies events requiring automatic Runtime collection.
func IsRuntimeDiagnosticEvent(method string) bool {
	switch method {
	case "Runtime.consoleAPICalled", "Runtime.exceptionThrown", "Runtime.exceptionRevoked", "Runtime.executionContextCreated", "Runtime.executionContextDestroyed", "Runtime.executionContextsCleared", "Runtime.inspectRequested":
		return true
	}
	return false
}

func (r *BrowserManager) enableRuntimeDiagnostics(ctx context.Context, conn *CdpConn, sessionID string) error {
	if !r.runtimeDiagnostics {
		return nil
	}
	if sessionID != "" {
		return conn.SendSessionPacket(ctx, sessionID, "Runtime.enable", nil)
	}
	_, err := conn.SendMessageContext(ctx, "Runtime.enable", nil)
	return err
}
