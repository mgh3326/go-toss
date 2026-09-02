// Package toss is a small, read-only client for the Toss Securities Open API.
//
// It deliberately has no order endpoints. Rate limiting and any authorization
// policy beyond the hooks exposed here belong to the calling application.
package toss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

var errRedirectBlocked = errors.New("toss: redirect blocked")

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
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errRedirectBlocked
	}
	config.httpClient = &clientCopy
	if config.limiter == nil {
		config.limiter = nopLimiter{}
	}
	return config
}

// Client makes read-only Toss API requests.
type Client struct {
	httpClient *http.Client
	tokens     TokenProvider
	limiter    Limiter
}

// NewClient creates a client pinned to HostOpenAPI.
func NewClient(tokens TokenProvider, options ...Option) *Client {
	config := newConfig(options)
	return &Client{httpClient: config.httpClient, tokens: tokens, limiter: config.limiter}
}

func (client *Client) get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	if client == nil || client.tokens == nil {
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
	if target == nil || !strings.EqualFold(target.Scheme, "https") {
		return &HostError{URL: urlString(target), Reason: "https is required"}
	}
	if target.User != nil || !strings.EqualFold(target.Host, "openapi.tossinvest.com") {
		return &HostError{URL: urlString(target), Reason: "host is not allowlisted"}
	}
	return nil
}

func urlString(target *url.URL) string {
	if target == nil {
		return ""
	}
	return target.String()
}

func doJSON(client *http.Client, request *http.Request) (json.RawMessage, error) {
	if err := assertTossURL(request.URL); err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, errRedirectBlocked) {
			return nil, &HostError{URL: request.URL.String(), Reason: "redirect refused"}
		}
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, &HostError{URL: request.URL.String(), Reason: "redirect refused"}
	}
	return ParseResponse(response)
}
