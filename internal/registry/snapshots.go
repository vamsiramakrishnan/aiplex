package registry

import "reflect"

// MemoryStore has value semantics like its durable counterpart. Nested config
// maps and pointer fields must not turn a read into an uncommitted write.
func memorySnapshot[T any](value T) T {
	return snapshotValue(reflect.ValueOf(value)).Interface().(T)
}

func snapshotValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		if value.Kind() == reflect.Pointer {
			out := reflect.New(value.Type().Elem())
			out.Elem().Set(snapshotValue(value.Elem()))
			return out
		}
		out := reflect.New(value.Type()).Elem()
		out.Set(snapshotValue(value.Elem()))
		return out
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), snapshotValue(iter.Value()))
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			out.Index(i).Set(snapshotValue(value.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			out.Index(i).Set(snapshotValue(value.Index(i)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		out.Set(value)
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).IsExported() {
				out.Field(i).Set(snapshotValue(value.Field(i)))
			}
		}
		return out
	default:
		return value
	}
}
