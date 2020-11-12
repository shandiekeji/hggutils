package rockfmw

import (
	"fmt"

	"github.com/gorilla/websocket"
)

// RPCWSClient ...
type RPCWSClient struct {
	rpcWSAbstract
	Conn *WsConn
	url  string
}

// NewRPCWSClient url "ws://192.168.0.34:8888/ws"
func NewRPCWSClient(url string) *RPCWSClient {
	s := &RPCWSClient{
		url: url,
	}
	s.services = new(serviceMap)
	s.mct = map[string]WSMethodHandle{}
	return s
}

// Run ...
func (slf *RPCWSClient) Run(ch WSCloseHandler) error {
	conn, _, err := websocket.DefaultDialer.Dial(slf.url, nil)
	fmt.Printf("连接服务器 %s  %+v\n", slf.url, err)
	if err != nil {
		return err
	}
	slf.Conn = &WsConn{
		Sock: conn,
	}
	go slf.enableRead(ch)
	return nil
}

// Send ...
func (slf *RPCWSClient) Send(method string, params interface{}) error {
	arg := map[string]interface{}{}
	arg["Method"] = method
	arg["Params"] = params
	return slf.Conn.WriteJSON(arg)
}

func (slf *RPCWSClient) enableRead(ch WSCloseHandler) {
	for {
		_, msg, err := slf.Conn.Sock.ReadMessage()
		if err != nil {
			slf.Conn.Sock.Close()
			ch.OnClose(slf.Conn)
			return
		}
		if err = slf.OnMessage(slf.Conn, msg); err != nil {
			slf.Conn.Sock.Close()
			ch.OnClose(slf.Conn)
			return
		}
	}
}
