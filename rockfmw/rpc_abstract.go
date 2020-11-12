package rockfmw

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gotoxu/cors"
)

// DoneHandle rpc完成时的回调函数，调用这个函数会将消息发送出去
type DoneHandle func(v interface{}, err error)

// RPCHTTPServer ...
type RPCHTTPServer struct {
	services *serviceMap
	svr      *http.Server
	debugReq bool
	debugRsp bool
}

// SetDebugRequest ...
func (slf *RPCHTTPServer) SetDebugRequest(b bool) {
	slf.debugReq = b
}

// SetDebugRespone ...
func (slf *RPCHTTPServer) SetDebugRespone(b bool) {
	slf.debugRsp = b
}

// ListenAndServe ...
func (slf *RPCHTTPServer) ListenAndServe(addr string) error {
	r := mux.NewRouter()
	r.HandleFunc("/{server:[a-zA-Z0-9]+}/{method:[a-zA-Z0-9]+}", func(w http.ResponseWriter, r *http.Request) {
		slf.HTTPHandler(w, r)
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

// RegisterService ...
func (slf *RPCHTTPServer) RegisterService(receiver interface{}, name string) error {
	return slf.services.register(receiver, name)
}

// callRPC 调用注册的rpc服务
func (slf *RPCHTTPServer) callRPC(req *ServerRequest, done DoneHandle) {
	serviceSpec, methodSpec, errGet := slf.services.get(req.Method)
	if errGet != nil {
		done(nil, fmt.Errorf("-3, %+v", errGet))
		return
	}

	// 反射并序列化请求参数
	args := reflect.New(methodSpec.argsType)
	err := req.ReadRequest(args.Interface())
	if err != nil {
		done(nil, fmt.Errorf("-4, %+v", err))
		return
	}

	/*
				// 反射应答参数
				reply := reflect.New(methodSpec.replyType)
				// 准备返回值
				var errValue []reflect.Value

			// 调用服务
			errValue = methodSpec.method.Func.Call([]reflect.Value{
				serviceSpec.rcvr,
				args,
				reply,
			})

		// 处理返回值
		var errResult error
		errInter := errValue[0].Interface()
		if errInter != nil {
			errResult = errInter.(error)
		}
		rsp := RPCRsp{Data: reply.Interface(), Msg: "", ID: req.ID}
		if errResult != nil {
			rsp.Msg = errResult.Error()
		}
		return rsp
	*/

	methodSpec.method.Func.Call([]reflect.Value{
		serviceSpec.rcvr,
		args,
		reflect.ValueOf(done),
	})
}

// HTTPHandler http处理流程
func (slf *RPCHTTPServer) HTTPHandler(w http.ResponseWriter, r *http.Request) {
	writeError := func(status int, msg string) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		w.Write([]byte(msg))
	}
	if r.Method != "POST" {
		writeError(http.StatusMethodNotAllowed, "rpc: POST method required, received "+r.Method)
		return
	}
	defer r.Body.Close()
	msg, err := ioutil.ReadAll(r.Body)
	if err != nil {
		writeError(http.StatusBadRequest, "rpc: ioutil.ReadAll "+err.Error())
		return
	}
	if slf.debugReq {
		log.Printf("request: %s\n%s\n", r.URL.Path, string(msg))
	}
	// 解析请求
	req := &ServerRequest{}
	err = json.Unmarshal(msg, req)
	if err != nil {
		writeError(http.StatusBadRequest, "rpc: ioutil.ReadAll "+err.Error())
		return
	}

	// 拼接得到rpc服务的名称
	vstr := strings.Split(r.URL.Path, "/")
	if len(vstr) != 3 {
		writeError(http.StatusBadRequest, "rpc: ioutil.ReadAll "+err.Error())
		return
	}
	req.Method = vstr[1] + "." + vstr[2]
	done := func(v interface{}, err error) {
		rsp := RPCRsp{
			ID:   req.ID,
			Data: v,
		}
		if err != nil {
			rsp.Msg = err.Error()
		}
		jd, _ := json.Marshal(rsp)
		if slf.debugRsp {
			log.Printf("respone: %s\n", string(jd))
		}
		w.Header().Set("Content-Type", "application/json;charset=utf-8")
		w.Write(jd)
	}
	// 调用rpc
	slf.callRPC(req, done)
}

////////////////////////////////////////////////////////////////////////////////////////////

// WSMethodHandle ...
type WSMethodHandle func(conn *WsConn, req *ServerRequest) error

type rpcWSAbstract struct {
	RPCHTTPServer
	mct map[string]WSMethodHandle
}

// RegisterWsHandler 注册websocket特殊消息处理
func (slf *rpcWSAbstract) RegisterWsHandler(method string, f WSMethodHandle) {
	slf.mct[method] = f
}

// OnMessage ...
func (slf *rpcWSAbstract) OnMessage(conn *WsConn, msg []byte) error {
	// 解析请求
	req := &ServerRequest{}
	err := json.Unmarshal(msg, req)
	if err != nil {
		conn.WriteMsg([]byte(err.Error()))
		return nil
	}
	f, ok := slf.mct[req.Method]
	if ok && f != nil {
		return f(conn, req)
	}
	done := func(v interface{}, err error) {
		rsp := RPCRsp{
			ID:   req.ID,
			Data: v,
		}
		if err != nil {
			rsp.Msg = err.Error()
		}
		conn.WriteJSON(rsp)
	}
	slf.callRPC(req, done)
	return nil
}
