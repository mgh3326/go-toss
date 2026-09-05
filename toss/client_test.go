package toss

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mgh3326/go-toss/internal/testutil"
)

type tokenProviderFunc func(context.Context) (string, error)

func (function tokenProviderFunc) AccessToken(ctx context.Context) (string, error) {
	return function(ctx)
}

func TestPricesSendsPinnedRequestAndUnwrapsResult(t *testing.T) {
	t.Parallel()
	client := NewClient(tokenProviderFunc(func(context.Context) (string, error) { return "token", nil }), WithHTTPClient(&http.Client{
		Transport: testutil.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
			if got, want := request.URL.String(), HostOpenAPI+"/api/v1/prices?symbols=005930%2CAAPL"; got != want {
				t.Errorf("URL = %q, want %q", got, want)
			}
			if got := request.Header.Get("Authorization"); got != "Bearer token" {
				t.Errorf("Authorization = %q", got)
			}
			return testutil.Response(request, http.StatusOK, `{"result":[{"symbol":"005930","timestamp":"2026-06-12T00:00:00Z","lastPrice":"70000","currency":"KRW"}]}`, nil), nil
		}),
	}))

	payload, err := client.Prices(context.Background(), []string{"005930", "AAPL"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(payload), `[{"symbol":"005930","timestamp":"2026-06-12T00:00:00Z","lastPrice":"70000","currency":"KRW"}]`; got != want {
		t.Errorf("payload = %s, want %s", got, want)
	}
}

func TestMarketCalendarCarriesPythonReadEndpoint(t *testing.T) {
	t.Parallel()
	client := NewClient(tokenProviderFunc(func(context.Context) (string, error) { return "token", nil }), WithHTTPClient(&http.Client{
		Transport: testutil.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
			if got, want := request.URL.Path, "/api/v1/market-calendar/KR"; got != want {
				t.Errorf("path = %q, want %q", got, want)
			}
			if got, want := request.URL.Query().Get("date"), "2026-09-03"; got != want {
				t.Errorf("date = %q, want %q", got, want)
			}
			return testutil.Response(request, http.StatusOK, `{"result":{"today":{"date":"2026-09-03"}}}`, nil), nil
		}),
	}))
	payload, err := client.MarketCalendar(context.Background(), "kr", "2026-09-03")
	if err != nil || string(payload) != `{"today":{"date":"2026-09-03"}}` {
		t.Fatalf("MarketCalendar() = %s, %v", payload, err)
	}
}

func TestHTTPURLIsBlocked(t *testing.T) {
	t.Parallel()
	err := assertTossURL(&url.URL{Scheme: "http", Host: "openapi.tossinvest.com"})
	var hostError *HostError
	if !errors.As(err, &hostError) {
		t.Fatalf("http URL error = %v, want HostError", err)
	}
}

func TestURLGuardRejectsBypassesWithoutLeakingInput(t *testing.T) {
	t.Parallel()
	const fixtureSecret = "fixture-query-secret"
	for name, target := range map[string]*url.URL{
		"uppercase scheme": {Scheme: "HTTPS", Host: "openapi.tossinvest.com"},
		"opaque":           {Scheme: "https", Opaque: "//evil.invalid/" + fixtureSecret},
		"userinfo":         {Scheme: "https", Host: "openapi.tossinvest.com", User: url.UserPassword("user", fixtureSecret)},
		"port":             {Scheme: "https", Host: "openapi.tossinvest.com:443"},
	} {
		t.Run(name, func(t *testing.T) {
			err := assertTossURL(target)
			var hostError *HostError
			if !errors.As(err, &hostError) || strings.Contains(err.Error(), fixtureSecret) || hostError.URL != HostOpenAPI {
				t.Fatalf("error = %#v, want safe HostError", err)
			}
		})
	}
	if err := assertTossURL(&url.URL{Scheme: "https", Host: "OPENAPI.TOSSINVEST.COM"}); err != nil {
		t.Fatalf("canonical host casing rejected: %v", err)
	}
}

func TestRedirectIsBlocked(t *testing.T) {
	t.Parallel()
	calls := 0
	client := NewClient(tokenProviderFunc(func(context.Context) (string, error) { return "token", nil }), WithHTTPClient(&http.Client{
		Transport: testutil.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			return testutil.Response(request, http.StatusFound, "", map[string]string{"Location": "https://example.invalid/?fixture-redirect-secret"}), nil
		}),
	}))
	_, err := client.Prices(context.Background(), []string{"005930"})
	var hostError *HostError
	if !errors.As(err, &hostError) {
		t.Fatalf("redirect error = %v, want HostError", err)
	}
	if calls != 1 || strings.Contains(err.Error(), "fixture-redirect-secret") {
		t.Fatalf("calls=%d error=%v, redirect was not safely stopped before request two", calls, err)
	}
}

func TestLimiterRunsBeforeTokenProvider(t *testing.T) {
	t.Parallel()
	called := false
	limiter := limiterFunc(func(context.Context) error { called = true; return errors.New("full") })
	client := NewClient(tokenProviderFunc(func(context.Context) (string, error) {
		t.Fatal("token provider called after limiter failure")
		return "", nil
	}), WithLimiter(limiter))
	_, err := client.Prices(context.Background(), []string{"005930"})
	if !called || err == nil {
		t.Fatalf("limiter called=%v err=%v", called, err)
	}
}

func TestUnsafeStandardTransportFailsBeforeLimiterTokenOrNetwork(t *testing.T) {
	unsafe := map[string]*http.Transport{
		"insecure skip verify": {TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // intentional rejection probe
		"verify peer":          {TLSClientConfig: &tls.Config{VerifyPeerCertificate: func([][]byte, [][]*x509.Certificate) error { return nil }}},
		"verify connection":    {TLSClientConfig: &tls.Config{VerifyConnection: func(tls.ConnectionState) error { return nil }}},
		"time":                 {TLSClientConfig: &tls.Config{Time: time.Now}},
		"session cache":        {TLSClientConfig: &tls.Config{ClientSessionCache: tls.NewLRUClientSessionCache(1)}},
		"old max TLS":          {TLSClientConfig: &tls.Config{MaxVersion: tls.VersionTLS11}},
		"dial TLS hook":        {DialTLSContext: func(context.Context, string, string) (net.Conn, error) { return nil, nil }},
		"TLS next proto":       {TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}},
	}
	for name, transport := range unsafe {
		t.Run(name, func(t *testing.T) {
			limiterCalled, tokenCalled := false, false
			client := NewClient(tokenProviderFunc(func(context.Context) (string, error) {
				tokenCalled = true
				return "fixture-token", nil
			}), WithLimiter(limiterFunc(func(context.Context) error {
				limiterCalled = true
				return nil
			})), WithHTTPClient(&http.Client{Transport: transport}))
			_, err := client.Prices(context.Background(), []string{"005930"})
			if !errors.Is(err, ErrUnsafeTransport) || limiterCalled || tokenCalled {
				t.Fatalf("err=%v limiter=%v token=%v, want early unsafe-transport rejection", err, limiterCalled, tokenCalled)
			}
		})
	}
}

func TestStandardTransportAndRootsAreOwned(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	callerTransport := http.DefaultTransport.(*http.Transport).Clone()
	callerTransport.TLSClientConfig = &tls.Config{RootCAs: roots}
	client := NewClient(tokenProviderFunc(func(context.Context) (string, error) { return "token", nil }), WithHTTPClient(&http.Client{Transport: callerTransport}))
	owned := client.httpClient.Transport.(*http.Transport)
	if client.setupErr != nil || owned == callerTransport || owned.TLSClientConfig == callerTransport.TLSClientConfig || owned.TLSClientConfig.RootCAs == roots {
		t.Fatalf("standard transport/root pool was not independently owned")
	}
	callerTransport.MaxIdleConns = 987
	roots.AddCert(testCertificate(t))
	if owned.MaxIdleConns == callerTransport.MaxIdleConns || owned.TLSClientConfig.RootCAs.Equal(roots) {
		t.Fatal("caller transport or CA pool mutation affected constructed client")
	}

	originalDefault := http.DefaultTransport
	globalTransport := originalDefault.(*http.Transport).Clone()
	http.DefaultTransport = globalTransport
	t.Cleanup(func() { http.DefaultTransport = originalDefault })
	implicit := NewClient(tokenProviderFunc(func(context.Context) (string, error) { return "token", nil }))
	implicitOwned := implicit.httpClient.Transport.(*http.Transport)
	globalTransport.MaxIdleConns = 654
	if implicit.setupErr != nil || implicitOwned == globalTransport || implicitOwned.MaxIdleConns == globalTransport.MaxIdleConns {
		t.Fatal("implicit default transport remained mutable through http.DefaultTransport")
	}
}

func testCertificate(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(987654321), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func TestTransportErrorsUseFixedOpaqueTaxonomy(t *testing.T) {
	const fixtureSecret = "fixture-transport-secret"
	sentinel := errors.New(fixtureSecret)
	client := NewClient(tokenProviderFunc(func(context.Context) (string, error) { return "token", nil }), WithHTTPClient(&http.Client{
		Transport: testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("lower error with %s: %w", fixtureSecret, sentinel)
		}),
	}))
	_, err := client.Prices(context.Background(), []string{"005930"})
	var transportError *TransportError
	if !errors.As(err, &transportError) || !errors.Is(err, ErrTransportFailure) || errors.Is(err, sentinel) || strings.Contains(err.Error(), fixtureSecret) || transportError.Location != HostOpenAPI || transportError.Category != "other" {
		t.Fatalf("error = %#v, want opaque fixed transport error", err)
	}
}

func TestTransportFailureCategories(t *testing.T) {
	cases := map[string]struct {
		err      error
		category string
	}{
		"canceled":   {context.Canceled, "canceled"},
		"timeout":    {context.DeadlineExceeded, "timeout"},
		"dns":        {&net.DNSError{Err: "fixture", Name: "fixture-secret.invalid"}, "dns"},
		"tls":        {x509.UnknownAuthorityError{}, "tls"},
		"connection": {&net.OpError{Op: "dial", Err: errors.New("fixture-secret")}, "connection"},
		"other":      {errors.New("fixture-secret"), "other"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			client := NewClient(tokenProviderFunc(func(context.Context) (string, error) { return "token", nil }), WithHTTPClient(&http.Client{Transport: testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, test.err
			})}))
			_, err := client.Prices(context.Background(), []string{"005930"})
			var transportError *TransportError
			if !errors.As(err, &transportError) || transportError.Category != test.category || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatalf("error = %#v, want category %q without fixture", err, test.category)
			}
		})
	}
}

func TestTokenProviderOwnsExpiryAndRefresh(t *testing.T) {
	t.Parallel()
	tokens := []string{"expired-caller-token", "refreshed-caller-token"}
	seen := []string{}
	client := NewClient(tokenProviderFunc(func(context.Context) (string, error) {
		token := tokens[0]
		tokens = tokens[1:]
		return token, nil
	}), WithHTTPClient(&http.Client{Transport: testutil.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.Header.Get("Authorization"))
		return testutil.Response(request, http.StatusOK, `{"result":[]}`, nil), nil
	})}))
	for range 2 {
		if _, err := client.Prices(context.Background(), []string{"005930"}); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := strings.Join(seen, ","), "Bearer expired-caller-token,Bearer refreshed-caller-token"; got != want {
		t.Fatalf("authorization=%q, TokenProvider refresh ownership was not preserved", got)
	}
}

type limiterFunc func(context.Context) error

func (function limiterFunc) Wait(ctx context.Context) error { return function(ctx) }
