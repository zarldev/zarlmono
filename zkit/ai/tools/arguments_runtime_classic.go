//go:build !goexperiment.jsonv2

package tools

import "reflect"

const argumentJSONv2 = false

var argumentRawFallbackType reflect.Type

func argumentStreamingDecoder(reflect.Type) bool { return false }

func argumentStreamingMarshaler(reflect.Type) bool { return false }
