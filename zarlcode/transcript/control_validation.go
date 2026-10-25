package transcript

import (
	"errors"
	"reflect"
)

func validateControlEntry(entry Entry) error {
	if entry.TurnID == "" {
		return errors.New("control turn ID is empty")
	}
	if !reflect.DeepEqual(entry.Payload, Payload{Control: entry.Payload.Control}) {
		return errors.New("control payload has unrelated fields")
	}
	if _, err := encodeControl(entry.Payload.Control); err != nil {
		return errors.New("control payload is invalid")
	}
	return nil
}
