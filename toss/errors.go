package toss

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const responseBodyLimit = 128 * 1024

// HostError reports an attempt to leave the pinned HTTPS Toss Open API host.
type HostError struct {
	URL    string
	Reason string
}

func (error *HostError) Error() string {
	return fmt.Sprintf("toss: blocked host request (%s): %s", error.Reason, error.URL)
}

// ErrorEnvelope is the normalized Toss API error payload.
type ErrorEnvelope struct {
	RequestID string
	Code      string
	Message   string
	Data      json.RawMessage
}

// ResponseError is returned for non-2xx responses and for non-JSON responses.
type ResponseError struct {
	StatusCode int
	Envelope   ErrorEnvelope
}

func (error *ResponseError) Error() string {
	return fmt.Sprintf("toss: API response status=%d code=%q request_id=%q", error.StatusCode, error.Envelope.Code, error.Envelope.RequestID)
}

// RateLimitError is returned for HTTP 429 responses.
type RateLimitError struct{ *ResponseError }

// ParseResponse implements the Python parse_toss_response contract:
//
//   - JSON 2xx envelopes return their result value; other JSON 2xx is returned unchanged.
//   - JSON error responses normalize error.requestId/code/message/data.
//   - non-JSON responses return a typed ResponseError with code non-json-response.
//   - HTTP 429 is a RateLimitError in either JSON case.
func ParseResponse(response *http.Response) (json.RawMessage, error) {
	if response == nil {
		return nil, &ResponseError{Envelope: ErrorEnvelope{Code: "non-json-response", Message: "nil HTTP response"}}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, responseBodyLimit+1))
	if err != nil || len(body) > responseBodyLimit {
		return nil, responseError(response, ErrorEnvelope{Code: "non-json-response", Message: "response body unavailable"})
	}
	var payload json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, responseError(response, nonJSONEnvelope(response, body))
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var envelope map[string]json.RawMessage
		if json.Unmarshal(payload, &envelope) == nil {
			if result, found := envelope["result"]; found {
				return result, nil
			}
		}
		return payload, nil
	}
	return nil, responseError(response, parseErrorEnvelope(payload))
}

func responseError(response *http.Response, envelope ErrorEnvelope) error {
	error := &ResponseError{StatusCode: response.StatusCode, Envelope: envelope}
	if response.StatusCode == http.StatusTooManyRequests {
		return &RateLimitError{ResponseError: error}
	}
	return error
}

func nonJSONEnvelope(response *http.Response, body []byte) ErrorEnvelope {
	requestID := response.Header.Get("X-Request-Id")
	if requestID == "" {
		requestID = response.Header.Get("CF-Ray")
	}
	message := string(bytes.TrimSpace(body))
	if len(message) > 200 {
		message = message[:200]
	}
	return ErrorEnvelope{RequestID: requestID, Code: "non-json-response", Message: message}
}

func parseErrorEnvelope(payload json.RawMessage) ErrorEnvelope {
	var outer struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(payload, &outer) != nil || len(outer.Error) == 0 {
		return ErrorEnvelope{Code: "malformed-error", Message: "Toss error response did not contain an error object"}
	}
	var errorObject map[string]json.RawMessage
	if json.Unmarshal(outer.Error, &errorObject) != nil || errorObject == nil {
		return ErrorEnvelope{Code: "malformed-error", Message: "Toss error response did not contain an error object"}
	}
	envelope := ErrorEnvelope{Code: "unknown-error"}
	if value, found := errorObject["requestId"]; found && !isJSONNull(value) {
		envelope.RequestID = valueString(value)
	}
	if value, found := errorObject["code"]; found && !isJSONNull(value) {
		if code := valueString(value); code != "" {
			envelope.Code = code
		}
	}
	if value, found := errorObject["message"]; found && !isJSONNull(value) {
		envelope.Message = valueString(value)
	}
	if value, found := errorObject["data"]; found && json.Valid(value) {
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) == nil && object != nil {
			envelope.Data = value
		}
	}
	return envelope
}

func isJSONNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func valueString(value json.RawMessage) string {
	var stringValue string
	if json.Unmarshal(value, &stringValue) == nil {
		return stringValue
	}
	var anyValue any
	if json.Unmarshal(value, &anyValue) == nil {
		return fmt.Sprint(anyValue)
	}
	return ""
}
