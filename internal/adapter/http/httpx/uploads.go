package httpx

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
)

// Uploads is several uploaded files plus the small text parts sent with them.
type Uploads struct {
	Files  []Upload
	Values map[string]string // text parts by field name
}

// ReadUploads reads a multipart body in one pass, without temp files: every part named one of fileFields is a file (at
// most maxFiles of them, each at most maxBytes), and the other parts are text values of at most maxValueBytes. More
// files than allowed is a 422 on the first file field ("max_items"), a file over maxBytes a 422 ("file_too_large"), a body
// over the whole limit a 413 and a body that is not multipart a 400. No file at all is fine: the caller decides whether
// one is needed.
func ReadUploads(c *gin.Context, fileFields []string, maxFiles int, maxBytes int64, maxValueBytes int64) (Uploads, error) {
	field := fileFields[0]
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, int64(maxFiles)*maxBytes+uploadOverheadBytes+16*maxValueBytes)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return Uploads{}, apperr.Malformed().WithCause(err)
	}
	up := Uploads{Values: map[string]string{}}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return up, nil
		}
		if err != nil {
			return Uploads{}, readFailure(err)
		}
		name := part.FormName()
		switch {
		case slices.Contains(fileFields, name):
			if len(up.Files) == maxFiles {
				return Uploads{}, apperr.Invalid(apperr.Field{Name: field, Rule: apperr.RuleMaxItems, Params: map[string]string{"max": strconv.Itoa(maxFiles)}})
			}
			data, err := io.ReadAll(io.LimitReader(part, maxBytes+1))
			if err != nil {
				return Uploads{}, readFailure(err)
			}
			if int64(len(data)) > maxBytes {
				return Uploads{}, apperr.Invalid(apperr.Field{
					Name: field, Rule: apperr.RuleFileTooLarge, Params: map[string]string{"max_kb": strconv.FormatInt(maxBytes/1024, 10)},
				})
			}
			up.Files = append(up.Files, Upload{FileName: cleanFileName(part.FileName()), Data: data})
		case name != "" && part.FileName() == "":
			value, err := io.ReadAll(io.LimitReader(part, maxValueBytes))
			if err != nil {
				return Uploads{}, readFailure(err)
			}
			up.Values[name] = string(value)
		}
		// Anything else (a file under another name, a part without a name) is ignored; the reader skips its bytes.
	}
}
