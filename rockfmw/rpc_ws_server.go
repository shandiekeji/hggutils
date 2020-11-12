package rockfmw

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/gotoxu/cors"
)

// WSOpenHandler ...
type WSOpenHandler interface {
	OnOpen(*WsConn) error
}

// WSCloseHandler ...
type WSCloseHandler interface {
	OnClose(*WsConn)
}

// RPCWServer ...
type RPCWServer struct {
	rpcWSAbstract
	aliveTime   int64 // ms
	openhandle  WSOpenHandler
	closehandle WSCloseHandler
	mu          sync.Mutex
	conns       map[*WsConn]bool
}

// NewRPCWServer ...
func NewRPCWServer() *RPCWServer {
	s := &RPCWServer{
		conns: map[*WsConn]bool{},
	}
	s.services = new(serviceMap)
	s.mct = map[string]WSMethodHandle{}
	return s
}

// SetAliveTime 设置websocket读取超时时间，如果读取超时将直接触发onclose，如果需要保持连接需要使用 ping
func (slf *RPCWServer) SetAliveTime(ms int64) {
	slf.aliveTime = ms
}

// SetOpenHandle ...
func (slf *RPCWServer) SetOpenHandle(h WSOpenHandler) {
	slf.openhandle = h
}

// SetCloseHandle ...
func (slf *RPCWServer) SetCloseHandle(h WSCloseHandler) {
	slf.closehandle = h
}

// ListenAndServe ...
func (slf *RPCWServer) ListenAndServe(addr string) error {
	r := mux.NewRouter()
	r.HandleFunc("/{server:[a-zA-Z0-9]+}/{method:[a-zA-Z0-9]+}", func(w http.ResponseWriter, r *http.Request) {
		slf.HTTPHandler(w, r)
	})

	r.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		slf.websocketHandler(w, r)
	})
	cors := cors.AllowAll()
	//return cors.Handler(gziphandler.GzipHandler(r))
	slf.svr = &http.Server{
		Addr:           addr,
		Handler:        cors.Handler(r),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	return slf.svr.ListenAndServe()
}

/////////////////////////////////////////////////////////////////////////

// websocketHandler websocket处理流程
func (slf *RPCWServer) websocketHandler(rw http.ResponseWriter, req *http.Request) {
	conn, err := (&websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		}}).Upgrade(rw, req, nil)
	if err != nil {
		return
	}
	c := &WsConn{
		Sock: conn,
	}
	if slf.openhandle != nil && slf.openhandle.OnOpen(c) != nil {
		c.Sock.Close()
		return
	}
	slf.mu.Lock()
	slf.conns[c] = true
	slf.mu.Unlock()

	go slf.enableRead(c)
}

func (slf *RPCWServer) enableRead(conn *WsConn) {
	f := func() {
		slf.mu.Lock()
		conn.Sock.Close()
		delete(slf.conns, conn)
		slf.mu.Unlock()
		if slf.closehandle != nil {
			slf.closehandle.OnClose(conn)
		}
	}
	for {
		if slf.aliveTime > 0 {
			conn.Sock.SetReadDeadline(time.Now().Add(time.Millisecond * time.Duration(slf.aliveTime)))
		}
		_, msg, err := conn.Sock.ReadMessage()
		if err != nil {
			f()
			return
		}
		if err = slf.OnMessage(conn, msg); err != nil {
			f()
		}
	}
}
