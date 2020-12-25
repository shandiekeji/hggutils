package jsonrpc

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/xerrors"
)

const wsCancel = "xrpc.cancel"
const chValue = "xrpc.ch.val"
const chClose = "xrpc.ch.close"
const wsPing = "xrpc.ping"
const wsPong = "xrpc.pong"

type frame struct {
	// common
	Jsonrpc string            `json:"jsonrpc"`
	ID      *int64            `json:"id,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`

	// request
	Method string  `json:"method,omitempty"`
	Params []param `json:"params,omitempty"`

	// response
	Result json.RawMessage `json:"result,omitempty"`
	Error  *respError      `json:"error,omitempty"`
}

type outChanReg struct {
	reqID int64

	chID uint64
	ch   reflect.Value
}

type wsConn struct {
	// outside params
	conn             *websocket.Conn
	connFactory      func() (*websocket.Conn, error)
	reconnectBackoff backoff
	pingInterval     time.Duration
	timeout          time.Duration
	noReConnect      bool
	handler          *RPCServer
	requests         <-chan clientRequest
	frameChan        chan frame

	isClient bool
	isStoped bool
	stop     <-chan struct{}
	exiting  chan struct{}

	//
	writeChan     chan []byte
	keepAliveChan chan []byte

	// ////
	// Client related

	// inflight are requests we've sent to the remote
	inflight map[int64]clientRequest

	// chanHandlers is a map of client-side channel handlers
	chanHandlers map[uint64]func(m []byte, ok bool)

	// ////
	// Server related

	// handling are the calls we handle
	handling   map[int64]context.CancelFunc
	handlingLk sync.Mutex

	spawnOutChanHandlerOnce sync.Once

	// chanCtr is a counter used for identifying output channels on the server side
	chanCtr uint64

	registerCh chan outChanReg
}

//                 //
// Output channels //
//                 //

// handleOutChans handles channel communication on the server side
// (forwards channel messages to client)
func (c *wsConn) handleOutChans() {
	regV := reflect.ValueOf(c.registerCh)
	exitV := reflect.ValueOf(c.exiting)

	cases := []reflect.SelectCase{
		{ // registration chan always 0
			Dir:  reflect.SelectRecv,
			Chan: regV,
		},
		{ // exit chan always 1
			Dir:  reflect.SelectRecv,
			Chan: exitV,
		},
	}
	internal := len(cases)
	var caseToID []uint64

	for {
		chosen, val, ok := reflect.Select(cases)

		switch chosen {
		case 0: // registration channel
			if !ok {
				// control channel closed - signals closed connection
				// This shouldn't happen, instead the exiting channel should get closed
				log.Warn("control channel closed")
				return
			}

			registration := val.Interface().(outChanReg)

			caseToID = append(caseToID, registration.chID)
			cases = append(cases, reflect.SelectCase{
				Dir:  reflect.SelectRecv,
				Chan: registration.ch,
			})

			msg, _ := json.Marshal(response{
				Jsonrpc: "2.0",
				ID:      registration.reqID,
				Result:  registration.chID,
			})
			c.writeChan <- msg

			continue
		case 1: // exiting channel
			if !ok {
				// exiting channel closed - signals closed connection
				//
				// We're not closing any channels as we're on receiving end.
				// Also, context cancellation below should take care of any running
				// requests
				return
			}
			log.Warn("exiting channel received a message")
			continue
		}

		if !ok {
			// Output channel closed, cleanup, and tell remote that this happened

			id := caseToID[chosen-internal]

			n := len(cases) - 1
			if n > 0 {
				cases[chosen] = cases[n]
				caseToID[chosen-internal] = caseToID[n-internal]
			}

			cases = cases[:n]
			caseToID = caseToID[:n-internal]

			msg, _ := json.Marshal(request{
				Jsonrpc: "2.0",
				ID:      nil, // notification
				Method:  chClose,
				Params:  []param{{v: reflect.ValueOf(id)}},
			})
			c.writeChan <- msg
			continue
		}

		// forward message
		msg, _ := json.Marshal(request{
			Jsonrpc: "2.0",
			ID:      nil, // notification
			Method:  chValue,
			Params:  []param{{v: reflect.ValueOf(caseToID[chosen-internal])}, {v: val}},
		})
		c.writeChan <- msg
	}
}

// handleChanOut registers output channel for forwarding to client
func (c *wsConn) handleChanOut(ch reflect.Value, req int64) error {
	c.spawnOutChanHandlerOnce.Do(func() {
		go c.handleOutChans()
	})
	id := atomic.AddUint64(&c.chanCtr, 1)

	select {
	case c.registerCh <- outChanReg{
		reqID: req,

		chID: id,
		ch:   ch,
	}:
		return nil
	case <-c.exiting:
		return xerrors.New("connection closing")
	}
}

//                          //
// Context.Done propagation //
//                          //

// handleCtxAsync handles context lifetimes for client
// TODO: this should be aware of events going through chanHandlers, and quit
//  when the related channel is closed.
//  This should also probably be a single goroutine,
//  Note that not doing this should be fine for now as long as we are using
//  contexts correctly (cancelling when async functions are no longer is use)
func (c *wsConn) handleCtxAsync(actx context.Context, id int64) {
	<-actx.Done()
	msg, _ := json.Marshal(request{
		Jsonrpc: "2.0",
		Method:  wsCancel,
		Params:  []param{{v: reflect.ValueOf(id)}},
	})
	c.writeChan <- msg
}

// cancelCtx is a built-in rpc which handles context cancellation over rpc
func (c *wsConn) cancelCtx(req frame) {
	if req.ID != nil {
		log.Warnf("%s call with ID set, won't respond", wsCancel)
	}

	var id int64
	if err := json.Unmarshal(req.Params[0].data, &id); err != nil {
		log.Error("handle me:", err)
		return
	}

	c.handlingLk.Lock()
	defer c.handlingLk.Unlock()

	cf, ok := c.handling[id]
	if ok {
		cf()
	}
}

//                     //
// Main Handling logic //
//                     //

func (c *wsConn) handleChanMessage(frame frame) {
	var chid uint64
	if err := json.Unmarshal(frame.Params[0].data, &chid); err != nil {
		log.Error("failed to unmarshal channel id in xrpc.ch.val: %s", err)
		return
	}

	hnd, ok := c.chanHandlers[chid]
	if !ok {
		log.Errorf("xrpc.ch.val: handler %d not found", chid)
		return
	}

	hnd(frame.Params[1].data, true)
}

func (c *wsConn) handleChanClose(frame frame) {
	var chid uint64
	if err := json.Unmarshal(frame.Params[0].data, &chid); err != nil {
		log.Error("failed to unmarshal channel id in xrpc.ch.val: %s", err)
		return
	}

	hnd, ok := c.chanHandlers[chid]
	if !ok {
		log.Errorf("xrpc.ch.val: handler %d not found", chid)
		return
	}

	delete(c.chanHandlers, chid)

	hnd(nil, false)
}

func (c *wsConn) handleResponse(frame frame) {
	req, ok := c.inflight[*frame.ID]
	if !ok {
		log.Error("client got unknown ID in response")
		return
	}

	if req.retCh != nil && frame.Result != nil {
		// output is channel
		var chid uint64
		if err := json.Unmarshal(frame.Result, &chid); err != nil {
			log.Errorf("failed to unmarshal channel id response: %s, data '%s'", err, string(frame.Result))
			return
		}

		var chanCtx context.Context
		chanCtx, c.chanHandlers[chid] = req.retCh()
		go c.handleCtxAsync(chanCtx, *frame.ID)
	}

	req.ready <- clientResponse{
		Jsonrpc: frame.Jsonrpc,
		Result:  frame.Result,
		ID:      *frame.ID,
		Error:   frame.Error,
	}
	delete(c.inflight, *frame.ID)
}

func (c *wsConn) handleCall(ctx context.Context, frame frame) {
	if c.handler == nil {
		log.Error("handleCall on client")
		return
	}

	req := request{
		Jsonrpc: frame.Jsonrpc,
		ID:      frame.ID,
		Meta:    frame.Meta,
		Method:  frame.Method,
		Params:  frame.Params,
	}

	ctx, cancel := context.WithCancel(ctx)

	nextWriter := func(interface{}) {
		//
	}
	done := func(keepCtx bool) {
		if !keepCtx {
			cancel()
		}
	}
	if frame.ID != nil {
		nextWriter = func(v interface{}) {
			msg, _ := json.Marshal(v)
			c.writeChan <- msg
		}

		c.handlingLk.Lock()
		c.handling[*frame.ID] = cancel
		c.handlingLk.Unlock()

		done = func(keepctx bool) {
			c.handlingLk.Lock()
			defer c.handlingLk.Unlock()

			if !keepctx {
				cancel()
				delete(c.handling, *frame.ID)
			}
		}
	}

	go c.handler.handle(ctx, req, nextWriter, rpcError, done, c.handleChanOut)
}

// handleFrame handles all incoming messages (calls and responses)
func (c *wsConn) handleFrame(ctx context.Context, frame frame) {
	// Get message type by method name:
	// "" - response
	// "xrpc.*" - builtin
	// anything else - incoming remote call
	switch frame.Method {
	case "": // Response to our call
		c.handleResponse(frame)
	case wsCancel:
		c.cancelCtx(frame)
	case wsPing:
		msg, _ := json.Marshal(request{
			Jsonrpc: "2.0",
			Method:  wsPong,
			Params:  []param{{v: reflect.ValueOf(time.Now().Format(time.RFC3339))}},
		})
		c.keepAliveChan <- msg
		//log.Infow("ping", "remote", c.conn.RemoteAddr().String(), "time", frame.Params)
	case wsPong:
		//log.Infow("pong", "remote", c.conn.RemoteAddr().String(), "time", frame.Params)
		return
	case chValue:
		c.handleChanMessage(frame)
	case chClose:
		c.handleChanClose(frame)
	default: // Remote call
		c.handleCall(ctx, frame)
	}
}

func (c *wsConn) closeInFlight() {
	for id, req := range c.inflight {
		req.ready <- clientResponse{
			Jsonrpc: "2.0",
			ID:      id,
			Error: &respError{
				Message: "handler: websocket connection closed",
				Code:    2,
			},
		}
	}

	c.handlingLk.Lock()
	for _, cancel := range c.handling {
		cancel()
	}
	c.handlingLk.Unlock()

	c.inflight = map[int64]clientRequest{}
	c.handling = map[int64]context.CancelFunc{}
}

func (c *wsConn) closeChans() {
	for chid := range c.chanHandlers {
		hnd := c.chanHandlers[chid]
		delete(c.chanHandlers, chid)
		hnd(nil, false)
	}
}

func (c *wsConn) handleWsConn(ctx context.Context) {
	c.inflight = map[int64]clientRequest{}
	c.handling = map[int64]context.CancelFunc{}
	c.chanHandlers = map[uint64]func(m []byte, ok bool){}
	c.writeChan = make(chan []byte, 100)
	c.keepAliveChan = make(chan []byte, 100)
	c.registerCh = make(chan outChanReg)
	c.frameChan = make(chan frame, 100)
	exitCh := make(chan struct{})

	bretry := !c.noReConnect
	var once sync.Once
	exitfun := func() {
		close(exitCh)
		if bretry && c.isClient {
			go func() {
				for attempts := 0; !c.isStoped; attempts++ {
					time.Sleep(time.Second * time.Duration(attempts+1))
					conn, err := c.connFactory()
					log.Infow("websocket connection retry", "error", err)
					if err != nil {
						continue
					}
					c.conn = conn
					c.handleWsConn(ctx)
					return
				}
				close(c.exiting)
			}()
		} else {
			close(c.exiting)
		}
	}

	// ////

	// on close, make sure to return from all pending calls, and cancel context
	//  on all calls we handle
	defer c.closeInFlight()
	defer c.closeChans()
	defer c.conn.Close()

	// 发送消息的协程
	go func() {
		var ptmr <-chan time.Time
		if c.pingInterval > 0 && c.isClient {
			ptmr = time.NewTicker(c.pingInterval).C
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-exitCh:
				return
			case <-ptmr:
				msg, _ := json.Marshal(request{
					Jsonrpc: "2.0",
					Method:  wsPing,
					Params:  []param{{v: reflect.ValueOf(time.Now().Format(time.RFC3339))}},
				})
				c.keepAliveChan <- msg
			case data := <-c.keepAliveChan:
				err := c.conn.WriteMessage(websocket.TextMessage, data)
				if err != nil {
					log.Errorf("write ping pong message error, %v, %v", c.conn.RemoteAddr(), err)
					once.Do(exitfun)
					return
				}
				//log.Infow("send", "remote", c.conn.RemoteAddr().String(), "data", string(data))
			case data := <-c.writeChan:
				err := c.conn.WriteMessage(websocket.TextMessage, data)
				if err != nil {
					log.Errorf("write message error, %v, %v", c.conn.RemoteAddr(), err)
					once.Do(exitfun)
					return
				}
			case req := <-c.requests:
				if req.req.ID != nil {
					c.inflight[*req.req.ID] = req
				}
				msg, _ := json.Marshal(req.req)
				c.writeChan <- msg
			case fm := <-c.frameChan:
				c.handleFrame(ctx, fm)
			case <-c.stop:
				cmsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "stop")
				if err := c.conn.WriteMessage(websocket.CloseMessage, cmsg); err != nil {
					log.Warn("failed to write close message: ", err)
				}
				bretry = false
				once.Do(exitfun)
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-exitCh:
			return
		default:
			if c.pingInterval > 0 {
				c.conn.SetReadDeadline(time.Now().Add(c.pingInterval * 3))
			}
			_, data, err := c.conn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					log.Errorf("read message error, %v, %v", c.conn.RemoteAddr(), err)
				} else {
					bretry = false
				}
				once.Do(exitfun)
				return
			}

			var frame frame
			if err := json.Unmarshal(data, &frame); err != nil {
				log.Error("handle me:", err)
				continue
			}

			c.frameChan <- frame
		}
	}
}
