package hggutils

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// SQLDecoder ...
type SQLDecoder struct {
	rows *sql.Rows
	tag  string
}

// NewSQLDecoder ...
func NewSQLDecoder(rows *sql.Rows) *SQLDecoder {
	return &SQLDecoder{
		rows: rows,
		tag:  "col",
	}
}

// SetSpecificTag ...
func (slf *SQLDecoder) SetSpecificTag(tag string) *SQLDecoder {
	slf.tag = tag
	return slf
}

// ToMap ...
func (slf *SQLDecoder) ToMap() ([]map[string]string, error) {
	columns, err := slf.rows.Columns()
	if err != nil {
		return nil, err
	}
	count := len(columns)
	tableData := make([]map[string]string, 0)
	values := make([]string, count)
	valuePtrs := make([]interface{}, count)
	for slf.rows.Next() {
		for i := 0; i < count; i++ {
			valuePtrs[i] = &values[i]
		}
		err := slf.rows.Scan(valuePtrs...)
		if err != nil {
			fmt.Println(err)
		}
		entry := make(map[string]string)
		for i, col := range columns {
			entry[strings.ToLower(col)] = values[i]
		}
		tableData = append(tableData, entry)
	}
	return tableData, nil
}

// UnMarshal ...
func (slf *SQLDecoder) UnMarshal(out interface{}) error {
	tbm, err := slf.ToMap()
	if err != nil {
		return err
	}
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Ptr {
		return errors.New("interface must be a pointer")
	}
	if v.Elem().Kind() == reflect.Struct {
		if len(tbm) != 1 {
			return fmt.Errorf("数据结果集的长度不匹配 len=%d", len(tbm))
		}
		return slf.mapSingle2interface(tbm[0], v)
	}
	if v.Elem().Kind() == reflect.Slice {
		return slf.mapSlice2interface(tbm, out)
	}
	return fmt.Errorf("错误的数据类型 %v", v.Elem().Kind())
}

func (slf *SQLDecoder) mapSingle2interface(m map[string]string, v reflect.Value) error {
	t := v.Type()
	val := v.Elem()
	typ := t.Elem()

	if !val.IsValid() {
		return errors.New("数据类型不正确")
	}

	for i := 0; i < val.NumField(); i++ {
		value := val.Field(i)
		kind := value.Kind()
		tag := typ.Field(i).Tag.Get(slf.tag)
		if tag == "" {
			tag = typ.Field(i).Name
		}

		if tag != "" && tag != "-" {
			vtag := strings.Split(strings.ToLower(tag), ",")
			meta, ok := m[vtag[0]]
			if !ok {
				continue
			}
			if !value.CanSet() {
				return errors.New("结构体字段没有读写权限")
			}
			if len(meta) == 0 {
				continue
			}
			switch kind {
			case reflect.String:
				value.SetString(meta)
			case reflect.Float32, reflect.Float64:
				f, err := strconv.ParseFloat(meta, 64)
				if err != nil {
					return err
				}
				value.SetFloat(f)
			case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				integer64, err := strconv.ParseInt(meta, 10, 64)
				if err != nil {
					return err
				}
				value.SetInt(integer64)
			case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				integer64, err := strconv.ParseUint(meta, 10, 64)
				if err != nil {
					return err
				}
				value.SetUint(integer64)
			case reflect.Bool:
				b, err := strconv.ParseBool(meta)
				if err != nil {
					return err
				}
				value.SetBool(b)
			case reflect.Slice:
				if value.Type().Elem().Kind() == reflect.Uint8 {
					// 只支持[]byte
					value.SetBytes([]byte(meta))
				}
			default:
				return fmt.Errorf("错误的数据类型 %v", kind)
			}
		}
	}
	return nil
}

func (slf *SQLDecoder) mapSlice2interface(data []map[string]string, in interface{}) error {
	length := len(data)

	if length > 0 {
		v := reflect.ValueOf(in).Elem()
		newv := reflect.MakeSlice(v.Type(), 0, length)
		v.Set(newv)
		v.SetLen(length)

		for i := 0; i < length; i++ {
			idxv := v.Index(i)
			if idxv.Kind() == reflect.Ptr {
				newObj := reflect.New(idxv.Type().Elem())
				v.Index(i).Set(newObj)
				idxv = newObj
			} else {
				idxv = idxv.Addr()
			}
			err := slf.mapSingle2interface(data[i], idxv)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
