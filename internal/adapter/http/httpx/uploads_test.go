package httpx

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
)

func uploadsRequest(t *testing.T, build func(w *multipart.Writer)) *gin.Context {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	build(w)
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

func addFile(t *testing.T, w *multipart.Writer, field, name, content string) {
	t.Helper()
	part, err := w.CreateFormFile(field, name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(content))
}

func TestReadUploadsCollectsFilesUnderEitherNameAndTheTextValues(t *testing.T) {
	c := uploadsRequest(t, func(w *multipart.Writer) {
		_ = w.WriteField("amount_vnd", "400000")
		_ = w.WriteField("note", strings.Repeat("n", 300))
		addFile(t, w, "proofs", "a.webp", "AAA")
		addFile(t, w, "proofs[]", "b.webp", "BB")
		addFile(t, w, "other", "c.webp", "ignored")
	})
	up, err := ReadUploads(c, []string{"proofs", "proofs[]"}, 3, 1024, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if len(up.Files) != 2 || string(up.Files[0].Data) != "AAA" || up.Files[1].FileName != "b.webp" {
		t.Errorf("files: %+v", up.Files)
	}
	if up.Values["amount_vnd"] != "400000" || len(up.Values["note"]) != 300 {
		t.Errorf("values: %v", up.Values)
	}
}

func TestReadUploadsWithNoFileIsFine(t *testing.T) {
	c := uploadsRequest(t, func(w *multipart.Writer) { _ = w.WriteField("amount_vnd", "1") })
	up, err := ReadUploads(c, []string{"proofs"}, 3, 1024, 64)
	if err != nil || len(up.Files) != 0 || up.Values["amount_vnd"] != "1" {
		t.Errorf("%+v, %v", up, err)
	}
}

func TestReadUploadsRefusesTooManyOrTooLargeFiles(t *testing.T) {
	c := uploadsRequest(t, func(w *multipart.Writer) {
		for range 4 {
			addFile(t, w, "proofs", "a.webp", "x")
		}
	})
	_, err := ReadUploads(c, []string{"proofs"}, 3, 1024, 64)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Fields[0].Rule != apperr.RuleMaxItems || ae.Fields[0].Params["max"] != "3" {
		t.Errorf("four files with a limit of three: %v", err)
	}

	c = uploadsRequest(t, func(w *multipart.Writer) { addFile(t, w, "proofs", "a.webp", strings.Repeat("x", 2048)) })
	_, err = ReadUploads(c, []string{"proofs"}, 3, 1024, 64)
	if !errors.As(err, &ae) || ae.Fields[0].Rule != apperr.RuleFileTooLarge || ae.Fields[0].Params["max_kb"] != "1" {
		t.Errorf("a file over the limit: %v", err)
	}
}

func TestReadUploadsRefusesABodyThatIsNotMultipart(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	c.Request.Header.Set("Content-Type", "application/json")
	_, err := ReadUploads(c, []string{"proofs"}, 3, 1024, 64)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Kind != apperr.KindBadRequest {
		t.Errorf("%v", err)
	}
}
