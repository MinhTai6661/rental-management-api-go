package invoice

import (
	"reflect"

	"github.com/google/uuid"
)

type reflectValue = reflect.Value

func valueOf(v any) reflectValue { return reflect.ValueOf(v) }

var uuidType = reflect.TypeOf(uuid.UUID{})

// containsUUID says whether the value, or anything inside it, is a uuid (or a pointer to one).
func containsUUID(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}
	if v.Type() == uuidType {
		return true
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return !v.IsNil() && containsUUID(v.Elem())
	case reflect.Struct:
		for i := range v.NumField() {
			if containsUUID(v.Field(i)) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if containsUUID(v.Index(i)) {
				return true
			}
		}
	}
	return false
}
