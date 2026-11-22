//go:build goexperiment.jsonv2

package tools

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"reflect"
)

const argumentJSONv2 = true

var argumentRawFallbackType = reflect.TypeFor[jsontext.Value]()

func argumentStreamingDecoder(t reflect.Type) bool {
	method := reflect.TypeFor[jsonv2.UnmarshalerFrom]()
	return t.Implements(method) || reflect.PointerTo(t).Implements(method)
}

func argumentStreamingMarshaler(t reflect.Type) bool {
	method := reflect.TypeFor[jsonv2.MarshalerTo]()
	return t.Implements(method) || reflect.PointerTo(t).Implements(method)
}
