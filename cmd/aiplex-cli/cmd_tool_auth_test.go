package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestToolAuthorizationCompletionRequiresBoundedPrivateFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "callback.json")
	raw := []byte(`{"flow_id":"flow","state":"state","code":"private-code","iss":"https://issuer.example"}`)
	if e := os.WriteFile(p, raw, 0600); e != nil {
		t.Fatal(e)
	}
	value, e := toolAuthResponseFile(p)
	if e != nil || value.Code != "private-code" {
		t.Fatal(value, e)
	}
	if e = os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = toolAuthResponseFile(p); e == nil && runtime.GOOS != "windows" {
		t.Fatal("publicly readable callback accepted")
	}
	if e = os.Chmod(p, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, []byte(strings.Repeat("x", 17000)), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = toolAuthResponseFile(p); e == nil {
		t.Fatal("unbounded callback accepted")
	}
}
