# go-toss

Minimal, read-only Go client for the Toss Securities Open API.

`go-toss` is deliberately a protocol client: it contains OAuth client-
credentials issuance and two read endpoints, but no order API, credential
storage, retry policy, or rate-limit policy.

## Install

```sh
go get github.com/mgh3326/go-toss
```

```go
oauth := toss.NewOAuthClient()
token, err := oauth.Issue(ctx, clientID, clientSecret)
// Persist token with your application's shared single-flight policy.

client := toss.NewClient(myTokenProvider, toss.WithLimiter(myLimiter))
prices, err := client.Prices(ctx, []string{"005930", "AAPL"})
calendar, err := client.MarketCalendar(ctx, "KR", "2026-09-03")
```

`TokenProvider` returns an access token for each request. Toss maintains one
valid OAuth token per client, so callers are responsible for shared storage and
single-flight refresh. The reference Python integration uses Redis keys
`toss:oauth:<first 16 hex of sha256(client_id)>:access_token` and `:lock`; this
library intentionally does not couple callers to Redis.

All requests are pinned to `toss.HostOpenAPI` (`https://openapi.tossinvest.com`).
HTTP, non-lowercase HTTPS schemes, userinfo, ports, opaque URLs,
non-allowlisted hosts, and all redirects are rejected. Standard Go transports
are copied at construction; unsafe TLS callbacks/configuration are rejected,
while offline fake RoundTrippers and independently-owned custom root CAs work.
This prevents a Bearer token or OAuth secret being sent to another authority.

## Public API

| Symbol | Purpose |
| --- | --- |
| `HostOpenAPI` | Pinned live Open API authority |
| `NewOAuthClient`, `OAuthClient.Issue` | OAuth2 client-credentials issue only |
| `TokenProvider`, `NewClient` | Caller-owned usable-token retrieval |
| `Client.Prices` | `GET /api/v1/prices` (1–200 symbols) |
| `Client.MarketCalendar` | `GET /api/v1/market-calendar/KR` or `/US` |
| `Limiter`, `WithLimiter` | Pre-request hook; default is no-op |
| `ParseResponse`, `ResponseError`, `RateLimitError` | Toss envelope and non-JSON response errors |
| `ErrUnsafeTransport` | Unsafe standard transport configuration rejection |
| `TransportError`, `ErrTransportFailure` | Fixed, non-leaking transport failure taxonomy |

Endpoint results are `json.RawMessage`, preserving Toss decimal strings and
fields without making a lossy schema commitment in v0.

## Error response compatibility

`ParseResponse` follows Python's `parse_toss_response` behavior:

| Response | Go behavior |
| --- | --- |
| JSON 2xx with `result` | Return `result` |
| Other JSON 2xx | Return complete JSON payload |
| JSON non-2xx | `*ResponseError` with normalized `ErrorEnvelope` |
| Non-JSON response | `*ResponseError`, code `non-json-response`, request ID from `X-Request-Id` or `CF-Ray` |
| HTTP 429 | `*RateLimitError` (including non-JSON 429) |

## Rate limits

Rate policy is an application concern. The Python reference policy is shown for
integration planning only; this package enforces none by default.

| API group | Reference limit |
| --- | --- |
| AUTH | 5 TPS |
| MARKET_DATA (prices) | 10 TPS |
| MARKET_INFO (calendar) | 3 TPS |

Run local checks with `go vet ./...`, `go test -race ./...`, and `gitleaks dir .`.
