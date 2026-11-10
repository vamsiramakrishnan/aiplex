package main

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

var debug bool

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func printYAML(v any) {
	enc := yaml.NewEncoder(os.Stdout)
	enc.SetIndent(2)
	enc.Encode(v)
	enc.Close()
}

// printTable renders rows under the CLI table policy (see table.go). Every
// column is shown; cells shorten to fit the terminal.
func printTable(headers []string, rows [][]string) {
	tableFrom(headers, rows).render(os.Stdout)
}

func tableFrom(headers []string, rows [][]string) *outTable {
	cols := make([]column, len(headers))
	for i, h := range headers {
		cols[i] = col(h)
	}
	return &outTable{cols: cols, rows: rows}
}

func debugf(format string, args ...any) {
	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] "+format+"\n", args...)
	}
}
