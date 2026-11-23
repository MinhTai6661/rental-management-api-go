package httpadapter

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	domain "rental-management/internal/domain/property"
	usecase "rental-management/internal/usecase/property"
	"rental-management/pkg/httpx"
)

// MediaHandler serves the picture routes of rooms and buildings and the public picture route.
type MediaHandler struct {
	uc             *usecase.Media
	urls           FileURLs
	binder         *httpx.Binder
	maxUploadBytes int
}

// NewMediaHandler returns the handler. maxUploadBytes is the size limit of one picture upload.
func NewMediaHandler(uc *usecase.Media, urls FileURLs, binder *httpx.Binder, maxUploadBytes int) *MediaHandler {
	return &MediaHandler{uc: uc, urls: urls, binder: binder, maxUploadBytes: maxUploadBytes}
}

// readUpload reads the multipart picture of the request into an Upload.
func (h *MediaHandler) readUpload(c *gin.Context) (usecase.Upload, error) {
	up, err := httpx.ReadUpload(c, "file", int64(h.maxUploadBytes))
	if err != nil {
		return usecase.Upload{}, err
	}
	return usecase.Upload{FileName: up.FileName, Data: up.Data, Kind: up.Values["kind"]}, nil
}

// mediaOwner reads the owner of the pictures from the path: a room or a building.
func mediaOwner(c *gin.Context, room bool) (domain.MediaOwner, error) {
	id, err := httpx.ParseUUIDParam(c, "id")
	if err != nil {
		return domain.MediaOwner{}, err
	}
	if room {
		return domain.RoomMedia(id), nil
	}
	return domain.BuildingMedia(id), nil
}

// Upload answers POST .../media: it stores a new picture.
func (h *MediaHandler) Upload(room bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner, ok := landlordIDOf(c)
		if !ok {
			return
		}
		target, err := mediaOwner(c, room)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		up, err := h.readUpload(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		media, err := h.uc.Add(c.Request.Context(), owner, target, up)
		if err != nil {
			httpx.Fail(c, mapPropertyError(err))
			return
		}
		httpx.Created(c, mediaViewOf(media, h.urls))
	}
}

// ReplaceFile answers PUT .../media/{media_id}/file: it puts a new image in place of a picture.
func (h *MediaHandler) ReplaceFile(room bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner, ok := landlordIDOf(c)
		if !ok {
			return
		}
		target, err := mediaOwner(c, room)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		mediaID, err := httpx.ParseUUIDParam(c, "media_id")
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		up, err := h.readUpload(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		media, err := h.uc.ReplaceFile(c.Request.Context(), owner, target, mediaID, up)
		if err != nil {
			httpx.Fail(c, mapPropertyError(err))
			return
		}
		httpx.OK(c, mediaViewOf(media, h.urls))
	}
}

// Arrange answers PUT .../media: it sets the order and the cover of the pictures.
func (h *MediaHandler) Arrange(room bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner, ok := landlordIDOf(c)
		if !ok {
			return
		}
		target, err := mediaOwner(c, room)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		var req arrangeRequest
		if err := h.binder.JSON(c, &req); err != nil {
			httpx.Fail(c, err)
			return
		}
		items := make([]domain.ArrangeItem, len(req.Items))
		for i, it := range req.Items {
			items[i] = domain.ArrangeItem(it)
		}
		media, err := h.uc.Arrange(c.Request.Context(), owner, target, items)
		if err != nil {
			httpx.Fail(c, mapPropertyError(err))
			return
		}
		httpx.OK(c, mediaViewsOf(media, h.urls))
	}
}

// Delete answers DELETE .../media/{media_id}.
func (h *MediaHandler) Delete(room bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner, ok := landlordIDOf(c)
		if !ok {
			return
		}
		target, err := mediaOwner(c, room)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		mediaID, err := httpx.ParseUUIDParam(c, "media_id")
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if err := h.uc.Delete(c.Request.Context(), owner, target, mediaID); err != nil {
			httpx.Fail(c, mapPropertyError(err))
			return
		}
		httpx.NoContent(c)
	}
}

// Serve answers the public picture route. The URL holds an unguessable, never reused key, so the answer can be
// cached for ever and the key's file name is the validator.
func (h *MediaHandler) Serve(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	body, info, err := h.uc.Open(c.Request.Context(), key)
	if err != nil {
		httpx.Fail(c, mapPropertyError(err))
		return
	}
	defer func() { _ = body.Close() }()

	etag := mediaETag(key)
	header := c.Writer.Header()
	header.Set("ETag", etag)
	header.Set("Cache-Control", "public, max-age=31536000, immutable")
	header.Set("Cross-Origin-Resource-Policy", "cross-origin")
	header.Set("Content-Security-Policy", "default-src 'none'")
	header.Set("X-Content-Type-Options", "nosniff")
	if etagMatches(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		c.Writer.WriteHeaderNow()
		return
	}
	c.DataFromReader(http.StatusOK, info.Size, info.ContentType, body, nil)
}
