package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestAnonymousRateLimitIsPerAddressNotPerConnection(t *testing.T) {
	h := RateLimit(5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	limited := false
	for i := 0; i < 10; i++ {
		r := httptest.NewRequest("GET", "/healthz", nil)
		r.RemoteAddr = "192.0.2.7:" + strconv.Itoa(40000+i)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		limited = limited || w.Code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("a new source port bypassed the anonymous rate limit")
	}
}
