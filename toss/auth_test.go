package toss

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/mgh3326/go-toss/internal/testutil"
)

func TestOAuthIssue(t *testing.T) {
	t.Parallel()
	client := NewOAuthClient(WithHTTPClient(&http.Client{Transport: testutil.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if got, want := request.URL.Path, "/oauth2/token"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		body, readErr := io.ReadAll(request.Body)
		form, err := url.ParseQuery(string(body))
		if readErr != nil || err != nil || form.Get("grant_type") != "client_credentials" || form.Get("client_id") != "id" || form.Get("client_secret") != "secret" {
			t.Errorf("unexpected form %q: read=%v parse=%v", body, readErr, err)
		}
		return testutil.Response(request, http.StatusOK, `{"access_token":"access","expires_in":3600}`, nil), nil
	})}))
	token, err := client.Issue(context.Background(), "id", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access" || token.ExpiresIn.Hours() != 1 {
		t.Errorf("token = %+v", token)
	}
}
