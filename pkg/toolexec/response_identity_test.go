package toolexec

import (
	"encoding/json"
	"testing"
)

func TestRPCResponseRejectsRequestMembers(t *testing.T) {
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":"rpc","method":"tools/call","result":{}}`,
		`{"jsonrpc":"2.0","id":"rpc","method":null,"result":{}}`,
		`{"jsonrpc":"2.0","id":"rpc","params":{},"result":{}}`,
		`{"jsonrpc":"2.0","id":"rpc","params":null,"error":{"code":-1,"message":"native error"}}`,
	} {
		if err := ValidateResponse([]byte(body), json.RawMessage(`"rpc"`)); err == nil {
			t.Fatal("ambiguous JSON-RPC shape accepted", body)
		}
	}
	if err := ValidateResponse([]byte(`{"jsonrpc":"2.0","id":"rpc","result":{"content":[{"type":"text","text":"data"}],"method":"ordinary result data","params":{}}}`), json.RawMessage(`"rpc"`)); err != nil {
		t.Fatal("nested result data was treated as envelope authority", err)
	}
}

func FuzzToolResponseIdentity(f *testing.F) {
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":"rpc","result":{"content":[]}}`,
		`{"jsonrpc":"2.0","id":"foreign","result":{}}`,
		`{"jsonrpc":"2.0","id":"rpc","method":"tools/call","result":{}}`,
		`{"jsonrpc":"2.0","id":"rpc","error":{"code":-1,"message":"native error"}}`,
		`{"jsonrpc":"2.0","id":"foreign","id":"rpc","result":{}}`,
	} {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 8192 {
			t.Skip()
		}
		if ValidateResponse([]byte(body), json.RawMessage(`"rpc"`)) != nil {
			return
		}
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(body), &object) != nil {
			t.Fatal("accepted non-JSON response")
		}
		var id string
		if json.Unmarshal(object["id"], &id) != nil || id != "rpc" {
			t.Fatal("foreign response became terminal evidence")
		}
		if _, exists := object["method"]; exists {
			t.Fatal("server method became response evidence")
		}
		if _, exists := object["params"]; exists {
			t.Fatal("server request params became response evidence")
		}
	})
}
