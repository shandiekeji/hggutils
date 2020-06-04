package hggutils

import (
	"encoding/json"
	"io/ioutil"
	"reflect"
	"time"
)

// Strftime 格式化成时间格式
func Strftime(t interface{}) string {
	v := reflect.ValueOf(t)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Int32, reflect.Int64, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		it := int64(ToFloat(t))
		return time.Unix(it, 0).Format("2006-01-02 15:04:05")
	}

	if n, ok := v.Interface().(time.Time); ok {
		return n.Format("2006-01-02 15:04:05")
	}
	return ""
}

// ToUnix 将字符串表示的时间转换成时间戳
func ToUnix(t string) int64 {
	tm := time.Time{}
	var err error
	tm, err = time.ParseInLocation(time.RFC3339, t, time.Local)
	if err == nil {
		return tm.Unix()
	}
	tm, err = time.ParseInLocation("2006-01-02 15:04:05", t, time.Local)
	if err == nil {
		return tm.Unix()
	}
	tm, err = time.ParseInLocation("2006-01-02 15:04:05 PM", t, time.Local)
	if err == nil {
		return tm.Unix()
	}
	tm, err = time.ParseInLocation("2006-01-02T15:04:05", t, time.Local)
	if err == nil {
		return tm.Unix()
	}
	return 0
}

// ReadJSONFile ...
func ReadJSONFile(path string, out interface{}) error {
	d, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	err = json.Unmarshal(d, out)
	if err != nil {
		return err
	}
	return nil
}
