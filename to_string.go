package hggutils

import (
	"reflect"
	"strconv"
	"strings"
)

// ToString ...
func ToString(from interface{}) string {
	v := reflect.ValueOf(from)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	}
	return ""
}

// ToUpperString ...
func ToUpperString(from interface{}) string {
	return strings.ToUpper(ToString(from))
}

// ToLowerString ...
func ToLowerString(from interface{}) string {
	return strings.ToLower(ToString(from))
}
