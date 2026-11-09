package api

import (
	"context"
	"fmt"
	"net/http"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
)

// Acquire native SDK credentials only after managed admission. Connection
// validation stays pure; signing-account attestation precedes dispatch claim.
type lazyAWSAgentCredentials struct {
	handler    *ConnectedHandler
	tenant     string
	connection cloudagents.Connection
	service    string
}

func (l lazyAWSAgentCredentials) ValidateConnection(c cloudagents.Connection) error {
	if c != l.connection {
		return fmt.Errorf("AWS connection identity changed")
	}
	return (cloudagents.SigV4Credentials{Region: c.Region, Service: l.service}).ValidateConnection(c)
}
func (l lazyAWSAgentCredentials) Authenticate(ctx context.Context, r *http.Request) error {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(l.connection.Region))
	if err != nil {
		return err
	}
	if err = l.handler.verifyLinkedBedrockAccount(ctx, l.tenant, l.connection, cfg.Credentials); err != nil {
		return fmt.Errorf("native signing account could not be attested")
	}
	return (cloudagents.SigV4Credentials{Source: cfg.Credentials, Region: l.connection.Region, Service: l.service}).Authenticate(ctx, r)
}
