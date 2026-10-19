# MCP credential refresh evaluation

Decision for [#366](https://github.com/CaliLuke/loom-mcp/issues/366): retain host-owned HTTP clients. Do not introduce a framework credential refresh or reactive retry helper. This evaluation changes tests and this report only.

An application can already supply an OAuth-aware client through `runtime/mcp.HTTPOptions.Client`. The existing `golang.org/x/oauth2` transport can obtain a valid token before dispatch. A generic replay after rejection would add policy that the framework cannot safely infer: an MCP POST may have already executed a non-idempotent tool. A reproducible request body and a 401 response do not establish that replay is safe. A 403 does not authorize credential refresh or broader grants.

## Surface and owners

| Surface | Existing extension and owner | Evaluation boundary |
| --- | --- | --- |
| MCP Streamable HTTP | `HTTPOptions.Client` passes the host client through the identity wrapper to the official SDK. The host owns credentials and transport policy; the SDK owns protocol negotiation and sessions. | Exercised with local HTTP servers and a real official SDK server. |
| MCP SSE | `NewSSECaller` accepts the same `HTTPOptions.Client`; the SDK owns SSE connections. | Source inventory only; no SSE auth/reconnect claims. |
| Gemini | `gemini.VertexOptions.HTTPClient` passes into `genai.ClientConfig` through `NewFromVertex`. General `gemini.Options.Client` accepts a `ModelsClient`, including `.Models` from a host-created `genai.Client` configured with `genai.ClientConfig.HTTPClient`. | Source inventory only. The supplied SDK owns transport retries; the Gemini adapter owns its bounded generation continuation, which is outside this evaluation. |
| Official MCP OAuth | Pinned `StreamableClientTransport.OAuthHandler` provides SDK-owned authorization and one retry. `HTTPOptions` does not expose that hook. | Existing extension inventoried; neither a replacement handler nor a new framework option is introduced. |

The issue body’s `gemini.Options.HTTPClient` shorthand is inaccurate: that field belongs to `VertexOptions`; general `Options` exposes `Client`. The exact declarations and constructor handoff are in [the Gemini client source](../../features/model/gemini/client.go).

The application chooses account, audience, scopes, credential storage, refresh authority, token-source sharing and lifetime. The OAuth library owns token caching and synchronized renewal. The MCP SDK owns discovery, legacy handshake fallback and transport/session behavior. This division avoids stacking a second retry manager on SDK behavior.

The pinned SDK calls `OAuthHandler.Authorize` after either 401 or 403 and retries once after success. The handler owns closing the rejected response body and deciding whether authorization is appropriate. Exposing that hook would require its own endpoint-specific contract; its presence does not establish that an arbitrary mutation can be replayed. See the [handler contract](https://github.com/modelcontextprotocol/go-sdk/blob/3e9d812b1814/auth/client.go#L14) and [SDK retry implementation](https://github.com/modelcontextprotocol/go-sdk/blob/3e9d812b1814/mcp/streamable.go#L2570).

## Permitted requests and cancellation

For the evaluated path, automatic authentication replay count is **zero**. A host may renew an expired token before the original dispatch. Renewal failure returns its error and prevents resource dispatch. Permission-denied responses are returned without changing credentials or scopes. A valid but rejected credential stays rejected until the host explicitly decides what to do.

A future opt-in replay facility would need evidence that the specific endpoint rejects before execution, or a supported idempotency/deduplication contract with a stable operation key. It would also need a replayable body, explicit eligible error classification, a bounded attempt count and coordination with SDK retries. None of those endpoint guarantees exists for arbitrary MCP tools, so this evaluation produces no implementation ticket.

`oauth2.Config.TokenSource(ctx, token)` captures the **host refresh context**. Canceling that context cancels an in-flight token exchange and prevents the resource request. Its `Token()` interface has no per-request context argument: canceling an individual resource request does not cancel a refresh that uses a separate host context. The probe confirms that the token exchange remains live until explicitly released, after which the canceled resource request is not sent. Hosts must bound the refresh context/token HTTP client independently and choose whether a shared refresh may outlive one caller. A deadline on the resource request alone must not be advertised as a deadline on token acquisition. See [context capture and cache mutex](https://github.com/golang/oauth2/blob/v0.37.0/oauth2.go#L244) and [transport dispatch/body ownership](https://github.com/golang/oauth2/blob/v0.37.0/transport.go#L30).

## Executable evidence

The two focused files use the existing OAuth library and local HTTP endpoints; no new auth implementation, live credentials, sleeps or dependency changes are involved. The endpoint's auth style is explicit, avoiding OAuth token-endpoint authentication-style discovery when counting exchange attempts. Failed and successful response bodies remain available to the caller, original request headers/body are preserved, request-body closure is observed, and `GetBody` is never called by an auth replay.

| Test and scenario | Observed bound |
| --- | --- |
| `TestCredentialRefreshHostBoundary/preflight_renewal_succeeds` | One token exchange, one resource request; renewed Authorization, host header and exact body arrive. Original request headers stay unchanged. |
| `TestCredentialRefreshHostBoundary/preflight_renewal_fails` | One failed token exchange, zero resource requests; typed `invalid_grant` error and original body closure. |
| `TestCredentialRefreshHostBoundary/repeated_rejection` | Two explicit caller requests produce two 401 responses, zero renewals and no hidden retry. |
| `TestCredentialRefreshHostBoundary/permission_denied` | One 403 response, zero renewals; status and response body remain available. |
| `TestCredentialRefreshHostBoundary/non-replayable_rejection` | One resource request with nil `GetBody`, no body recreation, original body closed. |
| `TestCredentialRefreshHostBoundary/effect_before_rejection` | Replayable POST applies one effect before 401; one request and zero body recreation. |
| `TestCredentialRefreshHostBoundary/feature_absent` | Ordinary host client dispatches one unauthenticated request with no renewal. |
| `TestCredentialRefreshConcurrent` | Eight callers share one expired-token source: one renewal and eight resource requests. Channels control the renewal interleaving. |
| `TestCredentialRefreshCancellation` | Host refresh cancellation returns `context.Canceled` with zero resource dispatch. Separate request cancellation leaves the host token exchange alive, then prevents dispatch after release. Cleanup explicitly releases the server even if an assertion fails. |
| `TestCredentialRefreshSDKBoundary/host_renewal_reaches_SDK_discovery_and_tool` | Real `NewHTTPCaller` and SDK server: one renewal, one discovery, one tool call and one effect. |
| `TestCredentialRefreshSDKBoundary/SDK_does_not_replay_a_dispatched_tool_after_401` | SDK executes the tool before endpoint rejection: one effect, one tool request and an error to the caller. |
| `TestCredentialRefreshSDKBoundary/absent_host_auth_remains_unauthorized` | Zero renewal/effects. Pinned SDK sends one `server/discover` and one fallback `initialize`, both rejected. These are distinct protocol requests, not an authentication replay. |

The SDK's [discovery/fallback logic](https://github.com/modelcontextprotocol/go-sdk/blob/3e9d812b1814/mcp/client.go#L321) explains the two unauthenticated handshake requests. Counts above apply to the pinned SDK, not an invented framework retry layer.

Existing `TestHTTPUserAgentPreservesClientAndRequestOwnership` and `TestHTTPUserAgentPreservesBodyContextAndResponse` cover client timeout, redirect policy, cookie jar, host transport, request/context/GetBody identity, response ownership and downstream errors. The evaluation reuses that established ownership boundary rather than duplicating those tests.

Run from the repository root:

```sh
go test -short -race ./runtime/mcp -run '^TestCredentialRefresh' -count=10 -timeout=60s
go test -short ./runtime/mcp -run 'TestCredentialRefresh|TestHTTPUserAgentPreserves' -count=1
```

The initial untracked overlay probe passed the full matrix under race 10 times (2.349s). The cancellation fixture was corrected before this result to drain the token request and guarantee cleanup release. A first native SDK probe incorrectly assumed legacy initialization and one absent-auth request; source inspection and exact method counts established discovery plus fallback, as documented above. These were probe corrections, not production fixes. Final tracked verification on 2026-10-19: the race command above passed all scenarios 10 times (2.076s); the focused ownership command passed (0.666s); `golangci-lint run --timeout=5m --allow-serial-runners ./runtime/mcp/...` reported zero issues. `gofmt` and `git diff --check` passed. These are targeted results; repository gates remain the coordinator’s responsibility.

## Sources and limits

Evaluated dependencies: `golang.org/x/oauth2 v0.37.0` and `github.com/modelcontextprotocol/go-sdk v1.8.1-0.20261016161459-3e9d812b1814`. The motivating [ADK commit 5381399](https://github.com/google/adk-go/commit/5381399b87b72859402db7a0d803953c699f7dd8) permits retries for replayable requests after 401/403, including non-idempotent methods. That behavior is inspiration for the question, not evidence that this framework needs it.

These probes establish the bounded host-client behavior above. They do not qualify arbitrary OAuth providers, token-endpoint retries, login flows, credential persistence, SSE reconnection, Gemini retries or servers that guarantee deduplication. No credentials are logged, and no grants or permissions are broadened.
