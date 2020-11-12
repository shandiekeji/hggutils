package rockfmw

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

// RPCHTTPClient ...
type RPCHTTPClient struct {
	client *http.Client
	addr   string
}

// NewRPCHTTPClient addr "http://192.168.0.34:8888"
func NewRPCHTTPClient(addr string) *RPCHTTPClient {
	s := &RPCHTTPClient{
		addr: addr,
	}
	s.client = &http.Client{Transport: &http.Transport{},
		Timeout: 5 * time.Second,
	}
	return s
}

// Call ...
func (slf *RPCHTTPClient) Call(method string, arg interface{}, reply interface{}) error {
	vs := strings.Split(method, ".")
	method = "/" + strings.Join(vs, "/")
	Params := map[string]interface{}{}
	Params["Params"] = arg
	js, _ := json.Marshal(Params)
	req, err := http.NewRequest("POST", slf.addr+method, bytes.NewReader(js))
	if err != nil {
		return err
	}
	rsp, err := slf.client.Do(req)
	if err != nil {
		return err
	}
	defer rsp.Body.Close()
	msg, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		return err
	}
	if rsp.StatusCode != http.StatusOK {
		return errors.New(string(msg))
	}
	out := RPCRsp{}
	out.Data = reply
	err = json.Unmarshal(msg, &out)
	if err != nil {
		return err
	}
	if out.Msg != "" {
		return errors.New(out.Msg)
	}
	return nil
}
