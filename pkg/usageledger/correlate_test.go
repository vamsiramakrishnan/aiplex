package usageledger

import "testing"

func TestIsServerRequestID(t *testing.T) {
	for id, want := range map[string]bool{
		"req-7": true, "req-0123456789abcdef-12": true,
		"req-abc-1": false, "req-0123456789ABCDEF-1": false, "req-0123456789abcdef-": false, "my-correlation-id": false, "": false,
	} {
		if IsServerRequestID(id) != want {
			t.Errorf("%q: want %v", id, want)
		}
	}
}
