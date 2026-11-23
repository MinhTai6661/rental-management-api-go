// Package testenv holds the response helpers the HTTP integration tests of every feature share. It sits beside the
// feature packages so none of them has to import another one's tests.
package testenv

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// ErrorBody is the error envelope of an API answer.
type ErrorBody struct {
	Error struct {
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Fields  map[string][]string `json:"fields"`
	} `json:"error"`
}

// ExpectError checks the status and the error code of a response and returns the error body.
func ExpectError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) ErrorBody {
	t.Helper()
	var body ErrorBody
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != status || body.Error.Code != code {
		t.Fatalf("want %d %s, got %d: %s", status, code, w.Code, w.Body.String())
	}
	return body
}

// ExpectStatus fails the test unless the response has the status.
func ExpectStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("want %d, got %d: %s", status, w.Code, w.Body.String())
	}
}

// DecodeData decodes the "data" member of a success response.
func DecodeData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var body struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return body.Data
}
