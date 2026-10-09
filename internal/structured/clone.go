// Package structured copies acyclic structured values while retaining their Go types.
package structured

import "reflect"

// Clone copies maps, slices, arrays, pointers, and exported struct fields.
// Values must be acyclic. Opaque values, including unexported fields, channels,
// and functions, remain shared and must be treated as immutable by callers.
func Clone[T any](value T) T {
	cloned := cloneStructuredValue(reflect.ValueOf(value))
	if !cloned.IsValid() {
		return value
	}
	return cloned.Interface().(T)
}

func cloneStructuredValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(cloneStructuredValue(value.Elem()))
		return cloned
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.New(value.Type().Elem())
		cloned.Elem().Set(cloneStructuredValue(value.Elem()))
		return cloned
	case reflect.Map:
		return cloneStructuredMap(value)
	case reflect.Slice:
		return cloneStructuredSlice(value)
	case reflect.Array:
		return cloneStructuredArray(value)
	case reflect.Struct:
		return cloneStructuredStruct(value)
	case reflect.Invalid, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
		reflect.Chan, reflect.Func, reflect.String, reflect.UnsafePointer:
		return value
	}
	return value
}

func cloneStructuredMap(value reflect.Value) reflect.Value {
	if value.IsNil() {
		return reflect.Zero(value.Type())
	}
	cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
	iter := value.MapRange()
	for iter.Next() {
		cloned.SetMapIndex(iter.Key(), cloneStructuredValue(iter.Value()))
	}
	return cloned
}

func cloneStructuredSlice(value reflect.Value) reflect.Value {
	if value.IsNil() {
		return reflect.Zero(value.Type())
	}
	cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
	for i := range value.Len() {
		cloned.Index(i).Set(cloneStructuredValue(value.Index(i)))
	}
	return cloned
}

func cloneStructuredArray(value reflect.Value) reflect.Value {
	cloned := reflect.New(value.Type()).Elem()
	for i := range value.Len() {
		cloned.Index(i).Set(cloneStructuredValue(value.Index(i)))
	}
	return cloned
}

func cloneStructuredStruct(value reflect.Value) reflect.Value {
	cloned := reflect.New(value.Type()).Elem()
	cloned.Set(value)
	for i := range value.NumField() {
		if value.Type().Field(i).IsExported() {
			cloned.Field(i).Set(cloneStructuredValue(value.Field(i)))
		}
	}
	return cloned
}
