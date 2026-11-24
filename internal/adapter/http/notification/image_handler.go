package notificationhttp

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/notification"
	usecase "rental-management/internal/usecase/notification"
)

// ImageHandler serves the picture routes of announcements (/admin/announcements/{id}/images and the landlord's twin) and
// the public picture route for the keys this context stored.
type ImageHandler struct {
	uc             *usecase.Images
	urls           FileURLs
	binder         *httpx.Binder
	maxUploadBytes int
}

// NewImageHandler returns the handler. maxUploadBytes is the size limit of one picture upload.
func NewImageHandler(uc *usecase.Images, urls FileURLs, binder *httpx.Binder, maxUploadBytes int) *ImageHandler {
	return &ImageHandler{uc: uc, urls: urls, binder: binder, maxUploadBytes: maxUploadBytes}
}

// arrangeRequest is the body of PUT .../images: the pictures in their new order. link_url left out keeps the link, ""
// clears it.
type arrangeRequest struct {
	Items []struct {
		ID      string  `json:"id" validate:"required,uuid"`
		LinkURL *string `json:"link_url"`
	} `json:"items" validate:"max=20,dive"`
}

// Upload answers POST .../images: it adds one picture (multipart "file") at the end.
func (handler *ImageHandler) Upload(ginCtx *gin.Context) {
	sender, id, ok := target(ginCtx)
	if !ok {
		return
	}
	upload, err := httpx.ReadUpload(ginCtx, "file", int64(handler.maxUploadBytes))
	if err != nil {
		httpx.Fail(ginCtx, err)
		return
	}
	image, err := handler.uc.Upload(ginCtx.Request.Context(), sender, id, upload.Data)
	if err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	httpx.Created(ginCtx, imageViewOf(image, handler.urls))
}

// Arrange answers PUT .../images: the pictures in the order sent; the ones not listed are removed.
func (handler *ImageHandler) Arrange(ginCtx *gin.Context) {
	sender, id, ok := target(ginCtx)
	if !ok {
		return
	}
	var requestBody arrangeRequest
	if err := handler.binder.JSON(ginCtx, &requestBody); err != nil {
		httpx.Fail(ginCtx, err)
		return
	}
	items := make([]domain.ArrangeItem, 0, len(requestBody.Items))
	for _, item := range requestBody.Items {
		itemID, err := parseUUID(item.ID)
		if err != nil {
			httpx.Fail(ginCtx, apperr.Invalid(apperr.Field{Name: "items", Rule: apperr.RuleUUID}))
			return
		}
		items = append(items, domain.ArrangeItem{ID: itemID, LinkURL: item.LinkURL})
	}
	images, err := handler.uc.Arrange(ginCtx.Request.Context(), sender, id, items)
	if err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	httpx.OK(ginCtx, imageViewsOf(images, handler.urls))
}

// Delete answers DELETE .../images/{image_id}.
func (handler *ImageHandler) Delete(ginCtx *gin.Context) {
	sender, id, ok := target(ginCtx)
	if !ok {
		return
	}
	imageID, err := httpx.ParseUUIDParam(ginCtx, "image_id")
	if err != nil {
		httpx.Fail(ginCtx, err)
		return
	}
	if err := handler.uc.Delete(ginCtx.Request.Context(), sender, id, imageID); err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	httpx.NoContent(ginCtx)
}

// Serve answers the public picture route for announcement keys. The URL holds an unguessable, never reused key, so the
// answer can be cached for ever and the key's file name is the validator.
func (handler *ImageHandler) Serve(ginCtx *gin.Context) {
	key := strings.TrimPrefix(ginCtx.Param("key"), "/")
	body, info, err := handler.uc.Open(ginCtx.Request.Context(), key)
	if err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	defer func() { _ = body.Close() }()

	etag := `"` + key[strings.LastIndex(key, "/")+1:] + `"`
	header := ginCtx.Writer.Header()
	header.Set("ETag", etag)
	header.Set("Cache-Control", "public, max-age=31536000, immutable")
	header.Set("Cross-Origin-Resource-Policy", "cross-origin")
	header.Set("Content-Security-Policy", "default-src 'none'")
	header.Set("X-Content-Type-Options", "nosniff")
	if etagMatches(ginCtx.GetHeader("If-None-Match"), etag) {
		ginCtx.Status(http.StatusNotModified)
		ginCtx.Writer.WriteHeaderNow()
		return
	}
	ginCtx.DataFromReader(http.StatusOK, info.Size, info.ContentType, body, nil)
}

// etagMatches reports whether an If-None-Match header lists etag (or is "*"); the weak prefix is ignored, as a weak
// comparison is the one the specification asks for.
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}
