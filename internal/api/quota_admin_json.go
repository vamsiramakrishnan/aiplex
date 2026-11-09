package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

func quotaFoldField(name string) string {
	var b strings.Builder
	for _, r := range name {
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		b.WriteRune(minimum)
	}
	return b.String()
}

// Struct aliases follow encoding/json case folding, but map coordinates remain
// case-sensitive. Distinct policy meter IDs are not collapsed into one field.
func quotaAdminUniqueFields(raw json.RawMessage, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil {
			return nil
		}
		seen := map[string]bool{}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if tag == "-" {
				continue
			}
			if tag == "" {
				tag = field.Name
			}
			fields[quotaFoldField(tag)] = field.Type
		}
		for name, value := range object {
			key := quotaFoldField(name)
			if seen[key] {
				return fmt.Errorf("ambiguous JSON struct field")
			}
			seen[key] = true
			if field := fields[key]; field != nil {
				if err := quotaAdminUniqueFields(value, field); err != nil {
					return err
				}
			}
		}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return nil
		}
		for _, item := range items {
			if err := quotaAdminUniqueFields(item, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil {
			return nil
		}
		for _, value := range object {
			if err := quotaAdminUniqueFields(value, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
