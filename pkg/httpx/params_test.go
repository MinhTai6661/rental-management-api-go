package httpx

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/pkg/apperr"
)

// ctxWithQuery is a gin context for GET /?query.
func ctxWithQuery(query string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
	return c
}

func TestParseUUIDParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := uuid.Must(uuid.NewV7())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Params = gin.Params{{Key: "id", Value: id.String()}}
	if got, err := ParseUUIDParam(c, "id"); err != nil || got != id {
		t.Errorf("valid id: %v %v", got, err)
	}
	c.Params = gin.Params{{Key: "id", Value: "nope"}}
	_, err := ParseUUIDParam(c, "id")
	if ae, ok := apperr.As(err); !ok || ae.Kind != apperr.KindNotFound {
		t.Errorf("bad id must be a 404, got %v", err)
	}
}

func TestQueryInt64(t *testing.T) {
	cases := []struct {
		query string
		want  *int64
		rule  string
	}{
		{"", nil, ""},
		{"x=", nil, ""},
		{"x=5", ptrInt64(5), ""},
		{"x=abc", nil, apperr.RuleInteger},
		{"x=-1", nil, apperr.RuleMinNumber},
		{"x=11", nil, apperr.RuleMaxNumber},
	}
	for _, tc := range cases {
		got, field := QueryInt64(ctxWithQuery(tc.query), "x", 0, 10)
		if tc.rule == "" && (field != nil || (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want)) {
			t.Errorf("%q: got %v %v", tc.query, got, field)
		}
		if tc.rule != "" && (field == nil || field.Rule != tc.rule) {
			t.Errorf("%q: field %v, want rule %s", tc.query, field, tc.rule)
		}
	}
}

func TestQueryFloatUUIDBoolOneOf(t *testing.T) {
	if f, field := QueryFloat(ctxWithQuery("x=2.5"), "x", 0, 10); field != nil || *f != 2.5 {
		t.Errorf("float: %v %v", f, field)
	}
	for _, q := range []string{"x=abc", "x=NaN", "x=Inf"} {
		if _, field := QueryFloat(ctxWithQuery(q), "x", 0, 10); field == nil {
			t.Errorf("%s must be refused", q)
		}
	}
	if _, field := QueryFloat(ctxWithQuery("x=11"), "x", 0, 10); field == nil || field.Rule != apperr.RuleMaxNumber {
		t.Errorf("float max: %v", field)
	}

	id := uuid.Must(uuid.NewV7())
	if got, field := QueryUUID(ctxWithQuery("x="+id.String()), "x"); field != nil || *got != id {
		t.Errorf("uuid: %v %v", got, field)
	}
	if _, field := QueryUUID(ctxWithQuery("x=zz"), "x"); field == nil || field.Rule != apperr.RuleUUID {
		t.Errorf("bad uuid: %v", field)
	}

	for q, want := range map[string]bool{"x=true": true, "x=1": true, "x=false": false, "x=0": false} {
		if got, field := QueryBool(ctxWithQuery(q), "x"); field != nil || *got != want {
			t.Errorf("%s: %v %v", q, got, field)
		}
	}
	if got, field := QueryBool(ctxWithQuery(""), "x"); got != nil || field != nil {
		t.Errorf("absent bool: %v %v", got, field)
	}
	if _, field := QueryBool(ctxWithQuery("x=yes"), "x"); field == nil || field.Rule != apperr.RuleOneOf {
		t.Errorf("bad bool: %v", field)
	}

	if got, field := QueryOneOf(ctxWithQuery("x=b"), "x", "a", "b"); field != nil || *got != "b" {
		t.Errorf("oneof: %v %v", got, field)
	}
	if _, field := QueryOneOf(ctxWithQuery("x=c"), "x", "a", "b"); field == nil || field.Rule != apperr.RuleOneOf {
		t.Errorf("bad oneof: %v", field)
	}
}

func ptrInt64(v int64) *int64 { return &v }

// multipartRequest builds a POST with a file part and text parts.
func multipartRequest(t *testing.T, fileName string, file []byte, values map[string]string) *gin.Context {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range values {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if file != nil {
		part, err := w.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(file)
	}
	_ = w.Close()
	return uploadContext(body.Bytes(), w.FormDataContentType())
}

func uploadContext(body []byte, contentType string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	return c
}

func TestReadUpload(t *testing.T) {
	c := multipartRequest(t, `C:\pics\front.webp`, []byte("hello"), map[string]string{"kind": "cover", "long": strings.Repeat("x", 200)})
	up, err := ReadUpload(c, "file", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(up.Data) != "hello" || up.FileName != "front.webp" || up.Values["kind"] != "cover" || len(up.Values["long"]) != 64 {
		t.Errorf("upload: %+v", up)
	}
}

func TestReadUploadFailures(t *testing.T) {
	// Over the file limit: 422 on the field, with the limit in KiB.
	_, err := ReadUpload(multipartRequest(t, "a.webp", bytes.Repeat([]byte("x"), 3000), nil), "file", 2048)
	ae, _ := apperr.As(err)
	if ae == nil || ae.Kind != apperr.KindValidation || ae.Fields[0].Rule != apperr.RuleFileTooLarge || ae.Fields[0].Params["max_kb"] != "2" {
		t.Errorf("too large file: %v", err)
	}

	// No file part.
	_, err = ReadUpload(multipartRequest(t, "", nil, map[string]string{"kind": "cover"}), "file", 1024)
	ae, _ = apperr.As(err)
	if ae == nil || ae.Fields[0].Rule != apperr.RuleRequired || ae.Fields[0].Name != "file" {
		t.Errorf("missing file: %v", err)
	}

	// Not multipart.
	_, err = ReadUpload(uploadContext([]byte("{}"), "application/json"), "file", 1024)
	ae, _ = apperr.As(err)
	if ae == nil || ae.Kind != apperr.KindBadRequest {
		t.Errorf("not multipart: %v", err)
	}

	// The whole body over the reader limit: 413.
	_, err = ReadUpload(multipartRequest(t, "a.webp", bytes.Repeat([]byte("x"), 200<<10), nil), "file", 1024)
	ae, _ = apperr.As(err)
	if ae == nil || (ae.Kind != apperr.KindTooLarge && ae.Kind != apperr.KindValidation) {
		t.Errorf("huge body: %v", err)
	}
}

func TestCleanFileName(t *testing.T) {
	if got := cleanFileName("../../etc/passwd"); got != "passwd" {
		t.Errorf("got %q", got)
	}
	if got := cleanFileName(""); got != "" {
		t.Errorf("empty: %q", got)
	}
	long := strings.Repeat("é", 200) // 400 bytes
	if got := cleanFileName(long); len(got) > 255 || len(got)%2 != 0 {
		t.Errorf("cut mid-character: %d bytes", len(got))
	}
}
