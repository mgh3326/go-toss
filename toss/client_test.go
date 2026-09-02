package toss

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

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
			return testutil.Response(request, http.StatusOK, `{"result":[{"symbol":"005930","lastPrice":"70000","currency":"KRW"}]}`, nil), nil
		}),
	}))

	payload, err := client.Prices(context.Background(), []string{"005930", "AAPL"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(payload), `[{"symbol":"005930","lastPrice":"70000","currency":"KRW"}]`; got != want {
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

func TestRedirectIsBlocked(t *testing.T) {
	t.Parallel()
	client := NewClient(tokenProviderFunc(func(context.Context) (string, error) { return "token", nil }), WithHTTPClient(&http.Client{
		Transport: testutil.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return testutil.Response(request, http.StatusFound, "", map[string]string{"Location": "https://example.invalid"}), nil
		}),
	}))
	_, err := client.Prices(context.Background(), []string{"005930"})
	var hostError *HostError
	if !errors.As(err, &hostError) {
		t.Fatalf("redirect error = %v, want HostError", err)
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

type limiterFunc func(context.Context) error

func (function limiterFunc) Wait(ctx context.Context) error { return function(ctx) }
