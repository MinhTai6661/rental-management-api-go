package httpx

import (
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
)

const (
	// uploadOverheadBytes is the room the multipart framing and the small text parts get on top of the file limit.
	uploadOverheadBytes = 64 << 10
	// maxUploadValueBytes caps each small text part that travels with the file (for example "kind").
	maxUploadValueBytes = 64
	// maxUploadFileNameBytes caps the stored original file name.
	maxUploadFileNameBytes = 255
)

// Upload is one uploaded file plus the small text parts sent with it.
type Upload struct {
	FileName string            // base name only, at most 255 bytes
	Data     []byte            // the file content, at most maxBytes
	Values   map[string]string // text parts by field name, each at most 64 bytes
}

// ReadUpload reads a multipart body in one pass, without temp files: the first part named field is the file and
// the other parts are small text values. A file over maxBytes is a 422 on the field ("file_too_large"), a body over
// the whole limit is 413, a body that is not multipart is 400 and a missing file is 422 "required".
func ReadUpload(c *gin.Context, field string, maxBytes int64) (Upload, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+uploadOverheadBytes)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return Upload{}, apperr.Malformed().WithCause(err)
	}

	up := Upload{Values: map[string]string{}}
	found := false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Upload{}, readFailure(err)
		}
		name := part.FormName()
		switch {
		case name == field && !found:
			data, err := io.ReadAll(io.LimitReader(part, maxBytes+1))
			if err != nil {
				return Upload{}, readFailure(err)
			}
			if int64(len(data)) > maxBytes {
				return Upload{}, apperr.Invalid(apperr.Field{
					Name: field, Rule: apperr.RuleFileTooLarge, Params: map[string]string{"max_kb": strconv.FormatInt(maxBytes/1024, 10)},
				})
			}
			up.Data, up.FileName, found = data, cleanFileName(part.FileName()), true
		case name != "" && part.FileName() == "":
			value, err := io.ReadAll(io.LimitReader(part, maxUploadValueBytes))
			if err != nil {
				return Upload{}, readFailure(err)
			}
			up.Values[name] = string(value)
		}
		// Anything else (a second file, a part without a name) is ignored; the reader skips its bytes.
	}
	if !found {
		return Upload{}, apperr.Invalid(apperr.Field{Name: field, Rule: apperr.RuleRequired})
	}
	return up, nil
}

// readFailure classifies an error met while reading the multipart body.
func readFailure(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return apperr.TooLarge()
	}
	return apperr.Malformed().WithCause(err)
}

// cleanFileName keeps the base name the client sent, cut to 255 bytes on a character boundary.
func cleanFileName(raw string) string {
	name := path.Base(strings.ReplaceAll(raw, "\\", "/"))
	if name == "." || name == "/" {
		return ""
	}
	for len(name) > maxUploadFileNameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}
