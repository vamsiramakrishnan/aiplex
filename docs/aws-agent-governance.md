# AWS AgentCore Identity governance

`pkg/governance/aws` calls the native AgentCore Identity control and data APIs. This is separate from `pkg/cloudagents` invoking a Bedrock agent or AgentCore Runtime using SigV4. A runtime ARN (`...:runtime/...`) cannot stand in for a workload identity ARN (`...:workload-identity-directory/default/workload-identity/...`).

The adapter manages entries in AWS's existing **default workload identity directory and default token vault**. It does not invent directory/vault creation, workload suspension or identity resource-policy APIs. AWS currently documents `GetResourcePolicy`/`PutResourcePolicy` for Runtime/Gateway, not AgentCore Identity resources. IAM role/policy attachment and service-managed Runtime/Gateway identity provisioning remain deployment responsibilities. Deleting a workload identity is an actual native deletion, not an advertised reversible disable.

## Supported native contracts

All requests below are signed `POST` requests with native JSON bodies. The host pins separate origins: `https://bedrock-agentcore-control.<region>.amazonaws.com` for control operations and `https://bedrock-agentcore.<region>.amazonaws.com` for credential exchanges. China uses `amazonaws.com.cn`. Both AgentCore clients use the `bedrock-agentcore` SigV4 service and their configured region. Tests use fake HTTP transports; no AWS resources were created or modified.

| Driver operation | Native API/path suffix after `/identities/` | Public controller |
| --- | --- | --- |
| `workloadIdentities.list/get/create/update/delete` | `ListWorkloadIdentities`, `GetWorkloadIdentity`, `CreateWorkloadIdentity`, `UpdateWorkloadIdentity`, `DeleteWorkloadIdentity` | Scoped metadata and configuration only |
| `oauth2CredentialProviders.list/get/delete` | `ListOauth2CredentialProviders`, `GetOauth2CredentialProvider`, `DeleteOauth2CredentialProvider` | Sanitized metadata; deletion requires reviewed mutation |
| `oauth2CredentialProviders.create/update` | `CreateOauth2CredentialProvider`, `UpdateOauth2CredentialProvider` | **Confidential library only** |
| `apiKeyCredentialProviders.list/get/delete` | `ListApiKeyCredentialProviders`, `GetApiKeyCredentialProvider`, `DeleteApiKeyCredentialProvider` | Sanitized metadata; deletion requires reviewed mutation |
| `apiKeyCredentialProviders.create/update` | `CreateApiKeyCredentialProvider`, `UpdateApiKeyCredentialProvider` | **Confidential library only** |
| `tokenVaults.get/setCMK` | `get-token-vault`, `set-token-vault-cmk` | KMS metadata and allowlisted key configuration |
| `workloadTokens.get/forJWT/forUserID` | `GetWorkloadAccessToken`, `GetWorkloadAccessTokenForJWT`, `GetWorkloadAccessTokenForUserId` | **Confidential library only** |
| `resourceCredentials.oauth2/apiKey` | `oauth2/token`, `api-key` | **Confidential library only** |

Workload get/delete bodies are `{ "name": "my-agent" }`; creation accepts `allowedResourceOauth2ReturnUrls` and optional native `tags`. Update replaces the return-URL list and does not accept tags. Provider create/update accept the native `APIKeyProviderInput`/`OAuth2ProviderInput` shapes, including AWS Secrets Manager references. Provider-specific OAuth configuration is the native one-member union, with vendor/member consistency enforced. Vendor-specific nested schemas remain AWS-validated and are available only to confidential library calls.

Provider inventory is projected into `ProviderMetadata`: name, scoped ARN, vendor, timestamps and status. It excludes native provider configuration, secret references, callback URLs and arbitrary extension fields. Workload/vault reads also use typed projections. The AWS API reference still documents legacy `acps` provider ARN responses while its developer guide uses `bedrock-agentcore`; both response forms must match the exact configured account, region, default vault and allowlisted provider name. Operation resource references use the developer guide's `bedrock-agentcore` form.

## Host wiring and authority

`New(Config, *nativeapi.Client)` constructs a driver; it performs no cloud calls. `Config` pins account, partition, region and exact workload/provider names. Empty allowlists grant no named resources. List operations require the configured directory/vault ARN and filter out every unallowlisted or incorrectly scoped entry. Caller-selected ARN accounts, regions, runtime resources, provider names and callback/scopes outside those lists fail validation before authentication or HTTP.

The host must call `ValidateOperation` before saving a desired operation. `Execute` repeats that pure validation. Native identity APIs select resources by **name**, so ARN validation alone cannot protect against credentials for another AWS account. Every operation additionally requires `WithAccountVerifier`. `NewSTSAccountVerifier(httpClient, credentialsProvider, region)` performs the real regional, SigV4-signed STS `GetCallerIdentity` XML protocol and checks the account and principal ARN partition. The host must use the same stable, account-bound credentials provider for STS and both AgentCore clients. Missing or failed account attestation fails closed, including inventory reads. The host must authorize the local principal/resource/action before calling the driver, and its network client must explicitly permit the regional STS origin.

```go
driver, err := awsgovernance.New(awsgovernance.Config{
    AccountID: "123456789012", Region: "us-east-1",
    AllowedWorkloadNames: []string{"my-agent"},
    AllowedProviderNames: []string{"google-drive"},
    AllowedReturnURLs: []string{"https://console.example/callback"},
    AllowedOAuthFlows: []string{"USER_FEDERATION"},
    AllowedScopes: []string{"drive.readonly"},
}, controlClient)
// Check err after every constructor. Clients carry a bounded HTTP transport and
// actual SigV4 authenticator, not bearer credentials or placeholder signatures.
verifier, err := awsgovernance.NewSTSAccountVerifier(stsHTTPClient, credentials, "us-east-1")
driver, err = driver.WithAccountVerifier(verifier)
```

AWS does not expose etag/CAS arguments on these identity mutations. Update/delete/KMS changes require `allow_non_atomic_mutations: true` and the exact `sha256:` fingerprint returned by get. The driver fetches the native object again and compares it before writing. This detects intervening drift, including provider configuration changes omitted from the public projection; it **does not provide atomic cloud compare-and-swap**. Create remains subject to AWS's normal name-conflict semantics. The driver performs no automatic retries.

## Workload and user credential binding

`WithDataPlane(dataClient, TokenBroker)` installs the separate data client and a trusted authorization broker. Without both, credential exchanges fail closed. The broker is code injected by the deployment, never an API payload. It must authenticate and authorize the current tenant and principal for the exact workload, provider, flow, scopes, audiences and resource targets, and resolve OAuth session handles under that principal's ownership. Broker authorization happens before STS credential retrieval/network calls.

Client payloads cannot supply `userToken`, `userId`, `workloadIdentityToken`, `sessionUri`, CSRF state or provider credentials. `TokenBinding` supplies the broker's verified JWT, trusted subject identifier, or opaque workload identity token. Its tenant/principal, workload ARN and provider must match the authorized request. Autonomous token requests cannot discard a delegated user binding; M2M and delegated OAuth flows must agree with the broker's identity mode.

`GetWorkloadAccessTokenForJWT` uses the verified user's IdP proof. `GetWorkloadAccessTokenForUserId` is disabled unless `allow_user_id_exchange` is explicitly enabled; AWS does not cryptographically authenticate that user ID. A host enabling it must derive a provider-qualified subject from authenticated context. Runtime/Gateway-managed workload identities can have service-managed token restrictions: a rejected native exchange stays a failure; the driver does not impersonate a different identity to bypass it.

For `resourceCredentials.oauth2`, the payload selects an allowlisted provider, `oauth2Flow` (`M2M`, `USER_FEDERATION`, `ON_BEHALF_OF_TOKEN_EXCHANGE`) and scopes. Optional audiences/resources must also be allowlisted. User federation requires a configured return URL. A caller can supply only an opaque `sessionHandle`; the broker resolves it into the native URI and supplies CSRF state for a new user authorization. A continuation whose session handle cannot be resolved fails closed. The library does not implement an implicit shared session cache or a public callback/token endpoint.

`Exchange(ctx, op)` returns a `CredentialResult` whose JSON, `String` and `GoString` representations are redacted. Consumers explicitly extract `WorkloadAccessToken()`, `AccessToken()` or `APIKey()` into their outbound transport; OAuth consent handling can use `AuthorizationURL()`, `SessionURI()` and `SessionStatus()`. `TokenBinding` also redacts formatting/JSON. The common low-level `Execute` contract returns raw native bodies for confidential library operations: never log or journal those results. Public controllers must reject **all `OperationInfo.Credential` operations**, including provider create/update, before persisting payloads or invoking the driver.

Native IAM must still enforce the same boundary. Grant only the necessary AgentCore actions to the execution role and scope both workload identities/directory and specific provider/vault resources as required by AWS's action table. JWT exchanges support `bedrock-agentcore:InboundJwtClaim/*` conditions; the user-ID action supports `bedrock-agentcore:userid`. Resource credential permissions can be constrained by workload identity and tags. This adapter does not attach broad IAM policies, automatically grant provider access, or infer enforcement from successful local registration.

## Verification

`go test ./pkg/governance/aws` covers exact native paths/bodies; workload and provider lifecycle; scoped/paginated inventories; typed secret redaction; native provider configuration drift; return URL/scope/provider/user-token substitution; delegated vs M2M binding; owned OAuth sessions; KMS allowlists; mandatory account attestation; and an actual regional STS SigV4 signing request. AWS service responses and HTTP are fake; deployment-specific IAM, IdP consent, PrivateLink and real regional service compatibility require deployment validation.

## Official contracts

- [AgentCore Identity overview](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/identity-overview.html)
- [Native resource architecture and ARNs](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/identity-tagging.html)
- [CreateWorkloadIdentity](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_CreateWorkloadIdentity.html), [GetWorkloadIdentity](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_GetWorkloadIdentity.html), [ListWorkloadIdentities](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_ListWorkloadIdentities.html), [UpdateWorkloadIdentity](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_UpdateWorkloadIdentity.html), [DeleteWorkloadIdentity](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_DeleteWorkloadIdentity.html)
- [CreateOauth2CredentialProvider](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_CreateOauth2CredentialProvider.html), [GetOauth2CredentialProvider](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_GetOauth2CredentialProvider.html), [OAuth input union](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_Oauth2ProviderConfigInput.html)
- [CreateApiKeyCredentialProvider](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_CreateApiKeyCredentialProvider.html), [GetApiKeyCredentialProvider](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_GetApiKeyCredentialProvider.html)
- [GetTokenVault](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_GetTokenVault.html), [SetTokenVaultCMK](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_SetTokenVaultCMK.html)
- [Workload token exchange security](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/get-workload-access-token.html), [JWT exchange API](https://docs.aws.amazon.com/bedrock-agentcore/latest/APIReference/API_GetWorkloadAccessTokenForJWT.html)
- [Resource OAuth token API](https://docs.aws.amazon.com/bedrock-agentcore/latest/APIReference/API_GetResourceOauth2Token.html), [Resource API-key API](https://docs.aws.amazon.com/bedrock-agentcore/latest/APIReference/API_GetResourceApiKey.html), [OAuth session binding](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/oauth2-authorization-url-session-binding.html)
- [IAM actions, resource types and condition keys](https://docs.aws.amazon.com/service-authorization/latest/reference/list_bedrock-agentcore.html), [Runtime/Gateway-only resource policies](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_GetResourcePolicy.html)
- [STS GetCallerIdentity](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetCallerIdentity.html)
