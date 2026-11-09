package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// AccountVerifier must attest the account of the same credential source used
// by the native clients. AgentCore identity APIs accept names, not account ARNs;
// local ARN validation alone cannot constrain credentials from another account.
type AccountVerifier interface {
	VerifyAccount(context.Context, string, string) error
}
type AccountVerifierFunc func(context.Context, string, string) error

func (f AccountVerifierFunc) VerifyAccount(ctx context.Context, account, partition string) error {
	if f == nil {
		return errors.New("AWS account attestation is unavailable")
	}
	return f(ctx, account, partition)
}
func (d *Driver) WithAccountVerifier(v AccountVerifier) (*Driver, error) {
	if d == nil || v == nil {
		return nil, errors.New("AWS account verifier is required")
	}
	copy := *d
	copy.account = v
	return &copy, nil
}
func (d *Driver) verifyAccount(ctx context.Context) error {
	if d.account == nil {
		return errors.New("AWS account verifier is not configured")
	}
	if d.account.VerifyAccount(ctx, d.cfg.AccountID, d.cfg.Partition) != nil {
		return errors.New("AWS credential account attestation failed")
	}
	return nil
}

type stsAccountVerifier struct {
	http                        *http.Client
	credentials                 sdkaws.CredentialsProvider
	region, partition, endpoint string
}

// NewSTSAccountVerifier performs signed regional STS GetCallerIdentity calls.
// The supplied HTTP client must allow only this region's STS origin and enforce
// the host's network policy. Redirects and application retries are disabled.
func NewSTSAccountVerifier(client *http.Client, credentials sdkaws.CredentialsProvider, region string) (AccountVerifier, error) {
	if client == nil || credentials == nil || !regionName.MatchString(region) {
		return nil, errors.New("STS requires configured HTTP, credentials and exact region")
	}
	partition := "aws"
	suffix := "amazonaws.com"
	if strings.HasPrefix(region, "cn-") {
		partition = "aws-cn"
		suffix = "amazonaws.com.cn"
	} else if strings.HasPrefix(region, "us-gov-") {
		partition = "aws-us-gov"
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copy.Timeout == 0 {
		copy.Timeout = 15 * time.Second
	}
	return &stsAccountVerifier{http: &copy, credentials: credentials, region: region, partition: partition, endpoint: "https://sts." + region + "." + suffix}, nil
}
func (v *stsAccountVerifier) VerifyAccount(ctx context.Context, expected, partition string) error {
	if !accountID.MatchString(expected) || partition != v.partition {
		return errors.New("AWS account attestation scope is invalid")
	}
	const body = "Action=GetCallerIdentity&Version=2011-06-15"
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint, strings.NewReader(body))
	if e != nil {
		return errors.New("STS request could not be constructed")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/xml")
	credentials, e := v.credentials.Retrieve(ctx)
	if e != nil {
		return errors.New("STS credentials are unavailable")
	}
	h := sha256.Sum256([]byte(body))
	if e = v4.NewSigner().SignHTTP(ctx, credentials, req, hex.EncodeToString(h[:]), "sts", v.region, time.Now()); e != nil {
		return errors.New("STS signing failed")
	}
	resp, e := v.http.Do(req)
	if e != nil {
		return errors.New("STS account attestation failed")
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil || len(data) > 65536 || resp.StatusCode != http.StatusOK {
		return errors.New("STS account attestation failed")
	}
	var out struct {
		XMLName xml.Name `xml:"GetCallerIdentityResponse"`
		Result  struct {
			Account string `xml:"Account"`
			ARN     string `xml:"Arn"`
		} `xml:"GetCallerIdentityResult"`
	}
	if xml.Unmarshal(data, &out) != nil || out.Result.Account != expected {
		return errors.New("AWS credentials do not belong to the configured account")
	}
	parts := strings.SplitN(out.Result.ARN, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" || parts[1] != partition || (parts[2] != "iam" && parts[2] != "sts") || parts[4] != expected || parts[5] == "" {
		return errors.New("STS principal does not belong to the configured account/partition")
	}
	return nil
}
