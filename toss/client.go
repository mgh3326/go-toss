// Package toss is a small, read-only client for the Toss Securities Open API.
//
// It deliberately has no order endpoints. Rate limiting and any authorization
// policy beyond the hooks exposed here belong to the calling application.
package toss

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// HostOpenAPI is the only live Toss Securities Open API authority.
	HostOpenAPI = "https://openapi.tossinvest.com"

	defaultTimeout = 10 * time.Second
)

var (
	errRedirectBlocked = errors.New("toss: redirect blocked")
	// ErrUnsafeTransport is returned before a request can consult a caller
	// supplied transport that weakens TLS or authority verification.
	ErrUnsafeTransport = errors.New("toss: caller HTTP transport weakens TLS or authority verification")
	// ErrTransportFailure is the only sentinel unwrapped by TransportError.
	// It deliberately does not expose the lower-level transport failure.
	ErrTransportFailure = errors.New("toss: transport request failed")
)

// TokenProvider supplies a usable access token for an API request.
//
// Toss accepts only one valid OAuth token per client. This library never
// persists or refreshes tokens itself: implementations must coordinate shared
// storage and single-flight issuance when they are used by multiple processes.
type TokenProvider interface {
	AccessToken(context.Context) (string, error)
}

// Limiter is the optional request hook used before each outbound request.
// The library's default is a no-op. Applications own rate-limit policy.
type Limiter interface {
	Wait(context.Context) error
}

type nopLimiter struct{}

func (nopLimiter) Wait(context.Context) error { return nil }

type config struct {
	httpClient *http.Client
	limiter    Limiter
	setupErr   error
}

// Option configures a Client or OAuthClient.
type Option func(*config)

// WithHTTPClient supplies the HTTP client used for requests. Its redirect
// policy is overridden: redirects are always rejected.
func WithHTTPClient(client *http.Client) Option {
	return func(config *config) { config.httpClient = client }
}

// WithLimiter supplies the pre-request rate-limit hook.
func WithLimiter(limiter Limiter) Option {
	return func(config *config) { config.limiter = limiter }
}

func newConfig(options []Option) config {
	config := config{httpClient: &http.Client{Timeout: defaultTimeout}, limiter: nopLimiter{}}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	if config.httpClient == nil {
		config.httpClient = &http.Client{Timeout: defaultTimeout}
	}
	// Copy rather than mutate a caller-owned client, then force no redirects.
	clientCopy := *config.httpClient
	transport, err := safeTransport(config.httpClient)
	if err != nil {
		config.setupErr = err
	} else {
		clientCopy.Transport = transport
	}
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errRedirectBlocked
	}
	config.httpClient = &clientCopy
	if config.limiter == nil {
		config.limiter = nopLimiter{}
	}
	return config
}

// safeTransport accepts fake RoundTrippers for offline tests. Standard
// transports are owned by this client so later global/caller mutation cannot
// change TLS roots or transport fields used by a request.
func safeTransport(client *http.Client) (http.RoundTripper, error) {
	var base http.RoundTripper
	if client != nil {
		base = client.Transport
	}
	if base == nil {
		defaultTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return &http.Transport{}, nil
		}
		return ownedStandardTransport(defaultTransport, true)
	}
	if transport, ok := base.(*http.Transport); ok {
		return ownedStandardTransport(transport, false)
	}
	return base, nil
}

func ownedStandardTransport(source *http.Transport, implicit bool) (*http.Transport, error) {
	if !implicit && unsafeStandardTransport(source) {
		return nil, ErrUnsafeTransport
	}
	clone := source.Clone()
	clone.Proxy = nil
	if implicit {
		clone.TLSNextProto = nil
	}
	if clone.TLSClientConfig != nil && clone.TLSClientConfig.RootCAs != nil {
		// tls.Config.Clone retains CertPool identity; a separate pool prevents an
		// in-place AddCert on the caller/global pool changing this client.
		clone.TLSClientConfig.RootCAs = clone.TLSClientConfig.RootCAs.Clone()
	}
	if unsafeStandardTransport(clone) {
		return nil, ErrUnsafeTransport
	}
	return clone, nil
}

// Keep this taxonomy aligned with go-kis: callbacks and protocol hooks can
// bypass ordinary peer/hostname verification and are not safe to inherit.
func unsafeStandardTransport(transport *http.Transport) bool {
	if transport.DialTLS != nil || transport.DialTLSContext != nil || transport.TLSNextProto != nil {
		return true
	}
	config := transport.TLSClientConfig
	if config == nil {
		return false
	}
	return config.InsecureSkipVerify || config.VerifyPeerCertificate != nil || config.VerifyConnection != nil || config.Time != nil || config.GetCertificate != nil || config.GetClientCertificate != nil || config.GetConfigForClient != nil || config.ClientSessionCache != nil || config.Renegotiation != tls.RenegotiateNever || config.KeyLogWriter != nil || config.Rand != nil || config.WrapSession != nil || config.UnwrapSession != nil || config.EncryptedClientHelloRejectionVerify != nil || config.GetEncryptedClientHelloKeys != nil || (config.MinVersion != 0 && config.MinVersion < tls.VersionTLS12) || (config.MaxVersion != 0 && config.MaxVersion < tls.VersionTLS12)
}

// Client makes read-only Toss API requests.
type Client struct {
	httpClient *http.Client
	tokens     TokenProvider
	limiter    Limiter
	setupErr   error
}

// NewClient creates a client pinned to HostOpenAPI.
func NewClient(tokens TokenProvider, options ...Option) *Client {
	config := newConfig(options)
	return &Client{httpClient: config.httpClient, tokens: tokens, limiter: config.limiter, setupErr: config.setupErr}
}

func (client *Client) get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	if client == nil {
		return nil, errors.New("toss: TokenProvider is required")
	}
	if client.setupErr != nil {
		return nil, client.setupErr
	}
	if client.tokens == nil {
		return nil, errors.New("toss: TokenProvider is required")
	}
	if err := client.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("toss: limiter: %w", err)
	}
	token, err := client.tokens.AccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("toss: access token: %w", err)
	}
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("toss: TokenProvider returned an invalid token")
	}
	request, err := newRequest(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	return doJSON(client.httpClient, request)
}

func newRequest(ctx context.Context, method, path string, query url.Values, body io.Reader) (*http.Request, error) {
	base, err := url.Parse(HostOpenAPI)
	if err != nil {
		return nil, fmt.Errorf("toss: invalid pinned host: %w", err)
	}
	base.Path = path
	base.RawQuery = query.Encode()
	if err := assertTossURL(base); err != nil {
		return nil, err
	}
	return http.NewRequestWithContext(ctx, method, base.String(), body)
}

func assertTossURL(target *url.URL) error {
	if target == nil || target.Scheme != "https" {
		return &HostError{URL: HostOpenAPI, Reason: "https is required"}
	}
	// URL.Hostname and URL.Port normalize a trailing colon and DNS brackets
	// away, so compare the raw authority to enforce the canonical DNS form.
	if target.Opaque != "" || target.User != nil || target.Port() != "" || !strings.EqualFold(target.Host, "openapi.tossinvest.com") {
		return &HostError{URL: HostOpenAPI, Reason: "host is not allowlisted"}
	}
	return nil
}

func doJSON(client *http.Client, request *http.Request) (json.RawMessage, error) {
	if err := assertTossURL(request.URL); err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, errRedirectBlocked) {
			return nil, &HostError{URL: HostOpenAPI, Reason: "redirect refused"}
		}
		return nil, transportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, &HostError{URL: HostOpenAPI, Reason: "redirect refused"}
	}
	return ParseResponse(response)
}

// TransportError contains only a fixed category and a controlled location.
// It intentionally never retains a url.Error or any lower transport error.
type TransportError struct {
	Category string
	Location string
}

func (error *TransportError) Error() string {
	return fmt.Sprintf("toss: transport failure (category=%s, location=%s)", error.Category, error.Location)
}

func (error *TransportError) Unwrap() error { return ErrTransportFailure }

func transportError(err error) error {
	return &TransportError{Category: classifyTransportFailure(err), Location: HostOpenAPI}
}

func classifyTransportFailure(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns"
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificateInvalid x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &certificateInvalid) {
		return "tls"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		return "connection"
	}
	return "other"
}
