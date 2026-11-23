//go:build integration

package identityhttp_test

import (
	"net/http/httptest"
	"testing"

	"rental-management/internal/adapter/http/testenv"
)

// The shared response helpers under the short names the tests use.
var (
	expectError  = testenv.ExpectError
	expectStatus = testenv.ExpectStatus
)

func decodeData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	return testenv.DecodeData[T](t, w)
}
