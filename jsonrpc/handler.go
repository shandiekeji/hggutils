package jsonrpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"reflect"

	"go.opencensus.io/stats"
	"go.opencensus.io/tag"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"
)

var (
	RPCMethod, _ = tag.NewKey("method")
)

// Measures
var (
	LotusInfo                = stats.Int64("info", "Arbitrary counter to tag lotus info to", stats.UnitDimensionless)
	ChainNodeHeight          = stats.Int64("chain/node_height", "Current Height of the node", stats.UnitDimensionless)
	ChainNodeWorkerHeight    = stats.Int64("chain/node_worker_height", "Current Height of workers on the node", stats.UnitDimensionless)
	MessageReceived          = stats.Int64("message/received", "Counter for total received messages", stats.UnitDimensionless)
	MessageValidationFailure = stats.Int64("message/failure", "Counter for message validation failures", stats.UnitDimensionless)
	MessageValidationSuccess = stats.Int64("message/success", "Counter for message validation successes", stats.UnitDimensionless)
	BlockReceived            = stats.Int64("block/received", "Counter for total received blocks", stats.UnitDimensionless)
	BlockValidationFailure   = stats.Int64("block/failure", "Counter for block validation failures", stats.UnitDimensionless)
	BlockValidationSuccess   = stats.Int64("block/success", "Counter for block validation successes", stats.UnitDimensionless)
	PeerCount                = stats.Int64("peer/count", "Current number of FIL peers", stats.UnitDimensionless)
	RPCInvalidMethod         = stats.Int64("rpc/invalid_method", "Total number of invalid RPC methods called", stats.UnitDimensionless)
	RPCRequestError          = stats.Int64("rpc/request_error", "Total number of request errors handled", stats.UnitDimensionless)
	RPCResponseError         = stats.Int64("rpc/response_error", "Total number of responses errors handled", stats.UnitDimensionless)
)

type rpcHandler struct {
	paramReceivers []reflect.Type
	nParams        int

	receiver    reflect.Value
	handlerFunc reflect.Value

	hasCtx int

	errOut int
	valOut int
}

type handlers map[string]rpcHandler

// Request / response

type request struct {
	Jsonrpc string            `json:"jsonrpc"`
	ID      *int64            `json:"id,omitempty"`
	Method  string            `json:"method"`
	Params  []param           `json:"params"`
	Meta    map[string]string `json:"meta,omitempty"`
}

type respError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *respError) Error() string {
	if e.Code >= -32768 && e.Code <= -32000 {
		return fmt.Sprintf("RPC error (%d): %s", e.Code, e.Message)
	}
	return e.Message
}

type response struct {
	Jsonrpc string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	ID      int64       `json:"id"`
	Error   *respError  `json:"error,omitempty"`
}

// Register

func (h handlers) register(namespace string, r interface{}) {
	val := reflect.ValueOf(r)
	//TODO: expect ptr

	for i := 0; i < val.NumMethod(); i++ {
		method := val.Type().Method(i)

		funcType := method.Func.Type()
		hasCtx := 0
		if funcType.NumIn() >= 2 && funcType.In(1) == contextType {
			hasCtx = 1
		}

		ins := funcType.NumIn() - 1 - hasCtx
		recvs := make([]reflect.Type, ins)
		for i := 0; i < ins; i++ {
			recvs[i] = method.Type.In(i + 1 + hasCtx)
		}

		valOut, errOut, _ := processFuncOut(funcType)

		h[namespace+"."+method.Name] = rpcHandler{
			paramReceivers: recvs,
			nParams:        ins,

			handlerFunc: method.Func,
			receiver:    val,

			hasCtx: hasCtx,

			errOut: errOut,
			valOut: valOut,
		}
	}
}

// Handle

type rpcErrFunc func(w func(func(io.Writer)), req *request, code int, err error)
type chanOut func(reflect.Value, int64) error

func (h handlers) handleReader(ctx context.Context, r io.Reader, w io.Writer, rpcError rpcErrFunc) {
	wf := func(cb func(io.Writer)) {
		cb(w)
	}

	var req request
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		rpcError(wf, &req, rpcParseError, fmt.Errorf("unmarshaling request: %v", err))
		return
	}

	h.handle(ctx, req, wf, rpcError, func(bool) {}, nil)
}

func doCall(methodName string, f reflect.Value, params []reflect.Value) (out []reflect.Value, err error) {
	defer func() {
		if i := recover(); i != nil {
			err = fmt.Errorf("panic in rpc method '%s': %s", methodName, i)
			log.Println(err)
		}
	}()

	out = f.Call(params)
	return out, nil
}

func (handlers) getSpan(ctx context.Context, req request) (context.Context, *trace.Span) {
	if req.Meta == nil {
		return ctx, nil
	}
	if eSC, ok := req.Meta["SpanContext"]; ok {
		bSC := make([]byte, base64.StdEncoding.DecodedLen(len(eSC)))
		_, err := base64.StdEncoding.Decode(bSC, []byte(eSC))
		if err != nil {
			log.Println("SpanContext: decode error ", err)
			return ctx, nil
		}
		sc, ok := propagation.FromBinary(bSC)
		if !ok {
			log.Println("SpanContext: could not create span data", bSC)
			return ctx, nil
		}
		ctx, span := trace.StartSpanWithRemoteParent(ctx, "api.handle", sc)
		span.AddAttributes(trace.StringAttribute("method", req.Method))
		return ctx, span
	}
	return ctx, nil
}

func (h handlers) handle(ctx context.Context, req request, w func(func(io.Writer)), rpcError rpcErrFunc, done func(keepCtx bool), chOut chanOut) {
	// Not sure if we need to sanitize the incoming req.Method or not.
	ctx, span := h.getSpan(ctx, req)
	ctx, _ = tag.New(ctx, tag.Insert(RPCMethod, req.Method))
	defer span.End()

	handler, ok := h[req.Method]
	if !ok {
		rpcError(w, &req, rpcMethodNotFound, fmt.Errorf("method '%s' not found", req.Method))
		stats.Record(ctx, RPCInvalidMethod.M(1))
		done(false)
		return
	}

	if len(req.Params) != handler.nParams {
		rpcError(w, &req, rpcInvalidParams, fmt.Errorf("wrong param count"))
		stats.Record(ctx, RPCRequestError.M(1))
		done(false)
		return
	}

	outCh := handler.valOut != -1 && handler.handlerFunc.Type().Out(handler.valOut).Kind() == reflect.Chan
	defer done(outCh)

	if chOut == nil && outCh {
		rpcError(w, &req, rpcMethodNotFound, fmt.Errorf("method '%s' not supported in this mode (no out channel support)", req.Method))
		stats.Record(ctx, RPCRequestError.M(1))
		return
	}

	callParams := make([]reflect.Value, 1+handler.hasCtx+handler.nParams)
	callParams[0] = handler.receiver
	if handler.hasCtx == 1 {
		callParams[1] = reflect.ValueOf(ctx)
	}

	for i := 0; i < handler.nParams; i++ {
		rp := reflect.New(handler.paramReceivers[i])
		if err := json.NewDecoder(bytes.NewReader(req.Params[i].data)).Decode(rp.Interface()); err != nil {
			rpcError(w, &req, rpcParseError, fmt.Errorf("unmarshaling params for '%s': %v", handler.handlerFunc, err))
			stats.Record(ctx, RPCRequestError.M(1))
			return
		}

		callParams[i+1+handler.hasCtx] = reflect.ValueOf(rp.Elem().Interface())
	}

	///////////////////

	callResult, err := doCall(req.Method, handler.handlerFunc, callParams)
	if err != nil {
		rpcError(w, &req, 0, fmt.Errorf("fatal error calling '%s': %v", req.Method, err))
		stats.Record(ctx, RPCRequestError.M(1))
		return
	}
	if req.ID == nil {
		return // notification
	}

	///////////////////

	resp := response{
		Jsonrpc: "2.0",
		ID:      *req.ID,
	}

	if handler.errOut != -1 {
		err := callResult[handler.errOut].Interface()
		if err != nil {
			log.Printf("error in RPC call to '%s': %+v\n", req.Method, err)
			stats.Record(ctx, RPCResponseError.M(1))
			resp.Error = &respError{
				Code:    1,
				Message: err.(error).Error(),
			}
		}
	}
	if handler.valOut != -1 {
		resp.Result = callResult[handler.valOut].Interface()
	}
	if resp.Result != nil && reflect.TypeOf(resp.Result).Kind() == reflect.Chan {
		// Channel responses are sent from channel control goroutine.
		// Sending responses here could cause deadlocks on writeLk, or allow
		// sending channel messages before this rpc call returns

		//noinspection GoNilness // already checked above
		err = chOut(callResult[handler.valOut], *req.ID)
		if err == nil {
			return // channel goroutine handles responding
		}

		log.Printf("failed to setup channel in RPC call to '%s': %+v\n", req.Method, err)
		stats.Record(ctx, RPCResponseError.M(1))
		resp.Error = &respError{
			Code:    1,
			Message: err.(error).Error(),
		}
	}

	w(func(w io.Writer) {
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Println(err)
			stats.Record(ctx, RPCResponseError.M(1))
			return
		}
	})
}
