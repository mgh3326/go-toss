// Package testutil contains small HTTP fakes for go-toss tests.
package testutil

import (
	"io"
	"net/http"
	"strings"
)

// RoundTripperFunc adapts a function into an http.RoundTripper.
type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (function RoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// Response builds a response for a stub transport. The request is retained so
// redirect handling has the same shape as a real net/http response.
func Response(request *http.Request, status int, body string, headers map[string]string) *http.Response {
	header := make(http.Header, len(headers))
	for key, value := range headers {
		header.Set(key, value)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}
}
