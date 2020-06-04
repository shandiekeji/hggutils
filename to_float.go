package hggutils

import (
	"reflect"
	"strconv"
)

// ToFloat ...
func ToFloat(i interface{}) float64 {
	v := reflect.ValueOf(i)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	var f float64
	switch v.Kind() {
	case reflect.String:
		f, _ = strconv.ParseFloat(v.String(), 64)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f = float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		f = float64(v.Uint())
	case reflect.Float32, reflect.Float64:
		f = v.Float()
	}

	return f
}

// ToInt32 ...
func ToInt32(i interface{}) int32 {
	return int32(ToFloat(i))
}

// ToUint32 ...
func ToUint32(i interface{}) uint32 {
	return uint32(ToFloat(i))
}

// ToInt64 ...
func ToInt64(i interface{}) int64 {
	return int64(ToFloat(i))
}

// ToUint64 ...
func ToUint64(i interface{}) uint64 {
	return uint64(ToFloat(i))
}
