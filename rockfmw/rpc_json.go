package rockfmw

import (
	"encoding/json"
	"fmt"
)

// ServerRequest 请求格式
type ServerRequest struct {
	// 请求的服务名称
	Method string
	// 客户端自定义数据
	ID string
	// 请求的参数
	Params *json.RawMessage
}

// RPCRsp ...
type RPCRsp struct {
	ID   string
	Msg  string
	Data interface{}
}

// ReadRequest fills the request object for the RPC method.
func (slf *ServerRequest) ReadRequest(out interface{}) error {
	if slf.Params != nil {
		// JSON params is array value. RPC params is struct.
		// Unmarshal into array containing the request struct.
		return json.Unmarshal(*slf.Params, out)
	}
	return fmt.Errorf("Params miss")
}
