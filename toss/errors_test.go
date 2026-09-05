package toss

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mgh3326/go-toss/internal/testutil"
)

func TestParseResponseErrorEnvelope(t *testing.T) {
	t.Parallel()
	request, _ := http.NewRequest(http.MethodGet, HostOpenAPI, nil)
	response := testutil.Response(request, http.StatusBadRequest, `{"error":{"requestId":"req-1","code":"invalid-token","message":"expired","data":{"reason":"test"}}}`, nil)
	_, err := ParseResponse(response)
	var responseError *ResponseError
	if !errors.As(err, &responseError) {
		t.Fatalf("error = %v, want ResponseError", err)
	}
	if responseError.Envelope.RequestID != "req-1" || responseError.Envelope.Code != "invalid-token" || string(responseError.Envelope.Data) != `{"reason":"test"}` {
		t.Errorf("envelope = %+v", responseError.Envelope)
	}
}

func TestParseResponseNonJSONRateLimit(t *testing.T) {
	t.Parallel()
	request, _ := http.NewRequest(http.MethodGet, HostOpenAPI, nil)
	response := testutil.Response(request, http.StatusTooManyRequests, "<html>slow down</html>", map[string]string{"CF-Ray": "ray-1"})
	_, err := ParseResponse(response)
	var rateLimit *RateLimitError
	if !errors.As(err, &rateLimit) {
		t.Fatalf("error = %v, want RateLimitError", err)
	}
	if rateLimit.Envelope.Code != "non-json-response" || rateLimit.Envelope.RequestID != "ray-1" || !strings.Contains(rateLimit.Envelope.Message, "slow down") {
		t.Errorf("envelope = %+v", rateLimit.Envelope)
	}
}

func TestParseResponsePythonCanonicalFixtures(t *testing.T) {
	// Copied in compact form from auto_trader tests/services/brokers/toss/
	// test_errors.py: 401 envelope, JSON 429, non-JSON 503, and non-JSON 2xx.
	request, _ := http.NewRequest(http.MethodGet, HostOpenAPI, nil)
	for name, test := range map[string]struct {
		status int
		body   string
		code   string
		rate   bool
	}{
		"401 JSON":     {http.StatusUnauthorized, `{"error":{"requestId":"req-401","code":"invalid-token","message":"expired"}}`, "invalid-token", false},
		"429 JSON":     {http.StatusTooManyRequests, `{"error":{"requestId":"req-429","code":"too-many-requests","message":"slow down","data":{"retryAfterSeconds":"1"}}}`, "too-many-requests", true},
		"503 non JSON": {http.StatusServiceUnavailable, "<html>fixture 503</html>", "non-json-response", false},
		"2xx non JSON": {http.StatusOK, "fixture text", "non-json-response", false},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseResponse(testutil.Response(request, test.status, test.body, nil))
			var rateLimit *RateLimitError
			isRateLimit := errors.As(err, &rateLimit)
			if isRateLimit != test.rate {
				t.Fatalf("rate-limit typing=%v, want %v", errors.As(err, &rateLimit), test.rate)
			}
			if isRateLimit {
				if rateLimit.Envelope.Code != test.code {
					t.Fatalf("error=%#v, want code=%q", err, test.code)
				}
				return
			}
			var responseError *ResponseError
			if !errors.As(err, &responseError) || responseError.Envelope.Code != test.code {
				t.Fatalf("error=%#v, want code=%q", err, test.code)
			}
		})
	}
}

func TestParseResponseMalformedErrorObject(t *testing.T) {
	t.Parallel()
	request, _ := http.NewRequest(http.MethodGet, HostOpenAPI, nil)
	response := testutil.Response(request, http.StatusBadRequest, `{"error":null}`, nil)
	_, err := ParseResponse(response)
	var responseError *ResponseError
	if !errors.As(err, &responseError) || responseError.Envelope.Code != "malformed-error" {
		t.Fatalf("error = %#v, want malformed ResponseError", err)
	}
}
