package toss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Token is the result of a Toss client-credentials exchange.
type Token struct {
	AccessToken string
	ExpiresIn   time.Duration
}

// OAuthClient issues Toss OAuth client-credentials tokens. It does not cache
// them. A caller should use a TokenProvider backed by shared storage and
// single-flight coordination, because Toss has one valid token per client.
type OAuthClient struct {
	httpClient *http.Client
	limiter    Limiter
	setupErr   error
}

// NewOAuthClient creates an OAuth client pinned to HostOpenAPI.
func NewOAuthClient(options ...Option) *OAuthClient {
	config := newConfig(options)
	return &OAuthClient{httpClient: config.httpClient, limiter: config.limiter, setupErr: config.setupErr}
}

// Issue exchanges a client ID and secret using OAuth2 client_credentials.
func (client *OAuthClient) Issue(ctx context.Context, clientID, clientSecret string) (Token, error) {
	if client == nil {
		return Token{}, errors.New("toss: OAuthClient is nil")
	}
	if client.setupErr != nil {
		return Token{}, client.setupErr
	}
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" ||
		strings.ContainsAny(clientID+clientSecret, "\r\n") {
		return Token{}, errors.New("toss: client ID and secret are required")
	}
	if err := client.limiter.Wait(ctx); err != nil {
		return Token{}, fmt.Errorf("toss: limiter: %w", err)
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	request, err := newRequest(ctx, http.MethodPost, "/oauth2/token", nil, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	payload, err := doJSON(client.httpClient, request)
	if err != nil {
		return Token{}, err
	}
	var response struct {
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.RawMessage `json:"expires_in"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return Token{}, fmt.Errorf("toss: malformed OAuth response: %w", err)
	}
	seconds, err := parseExpiresIn(response.ExpiresIn)
	if err != nil || response.AccessToken == "" || strings.ContainsAny(response.AccessToken, "\r\n") || seconds <= 0 {
		return Token{}, errors.New("toss: malformed OAuth response")
	}
	return Token{AccessToken: response.AccessToken, ExpiresIn: time.Duration(seconds) * time.Second}, nil
}

func parseExpiresIn(raw json.RawMessage) (int64, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return strconv.ParseInt(number.String(), 10, 64)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	return strconv.ParseInt(text, 10, 64)
}
