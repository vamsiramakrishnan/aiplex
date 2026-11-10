package aiplex_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestSDKDoesNotAcknowledgeNonSuccessMutation(t *testing.T) {
	for _, status := range []int{http.StatusNotModified, http.StatusTemporaryRedirect, http.StatusPermanentRedirect, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			client := aiplex.NewClient(server.URL)
			err := client.DeleteClientRegistration(context.Background(), "developer", 1)
			var failure *aiplex.Error
			if !errors.As(err, &failure) || failure.StatusCode != status {
				t.Fatalf("unacknowledged mutation became success or lost status: %d %v", status, err)
			}
		})
	}
}
