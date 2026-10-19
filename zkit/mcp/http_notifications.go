package mcp

import "encoding/json"

func (t *httpTransport) dispatchHTTPNotification(payload []byte) {
	var notification rpcNotificationIn
	if json.Unmarshal(payload, &notification) != nil || notification.JSONRPC != jsonrpcVersion || notification.Method == "" {
		return
	}
	t.notifMu.Lock()
	handler := t.notifier
	t.notifMu.Unlock()
	if handler != nil {
		handler(notification.Method, notification.Params)
	}
}
