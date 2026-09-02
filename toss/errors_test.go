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
