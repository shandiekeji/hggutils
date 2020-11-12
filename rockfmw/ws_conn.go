package rockfmw

import (
	"sync"

	"github.com/gorilla/websocket"
)

// WsConn ...
type WsConn struct {
	mu   sync.Mutex
	Sock *websocket.Conn
}

// WriteMsg ...
func (slf *WsConn) WriteMsg(msg []byte) error {
	slf.mu.Lock()
	defer slf.mu.Unlock()

	return slf.Sock.WriteMessage(websocket.TextMessage, msg)
}

// WriteJSON ...
func (slf *WsConn) WriteJSON(v interface{}) error {
	slf.mu.Lock()
	defer slf.mu.Unlock()

	return slf.Sock.WriteJSON(v)
}
