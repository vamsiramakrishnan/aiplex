package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// spReadSpec reads a JSON or YAML document from a file or stdin ("-").
func spReadSpec(path string, stdin io.Reader) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("-f <file> is required (use '-' for stdin)")
	}
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(io.LimitReader(stdin, 1<<20+1))
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > 1<<20 {
		return nil, fmt.Errorf("%s exceeds 1 MiB", path)
	}
	// YAML is a superset of JSON; normalise to JSON so decoding is strict and
	// uses the API's field names.
	var generic any
	if err = yaml.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	generic = spJSONCompatible(generic)
	out, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return out, nil
}

// spJSONCompatible converts yaml.v3 map[any]any values to map[string]any.
func spJSONCompatible(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = spJSONCompatible(e)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[fmt.Sprint(k)] = spJSONCompatible(e)
		}
		return m
	case []any:
		for i, e := range t {
			t[i] = spJSONCompatible(e)
		}
		return t
	}
	return v
}
