package hggutils

import (
	"bytes"
	"encoding/gob"
)

// Clone 深拷贝
func Clone(src, dst interface{}) error {
	buff := new(bytes.Buffer)
	enc := gob.NewEncoder(buff)
	dec := gob.NewDecoder(buff)
	if err := enc.Encode(src); err != nil {
		return err
	}
	return dec.Decode(dst)
}
