package main

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/gin-gonic/gin"

	"rental-management/internal/adapter/filestore"
	"rental-management/internal/adapter/imageproc"
	notificationdomain "rental-management/internal/domain/notification"
	propertydomain "rental-management/internal/domain/property"
	notificationuc "rental-management/internal/usecase/notification"
)

// announcementFiles is the file store of the announcement pictures on the byte store: it speaks the notification
// domain's "file not found" (the filestore adapter speaks the property domain's).
type announcementFiles struct{ inner *filestore.Store }

// Put implements notificationuc.FileStore.
func (f announcementFiles) Put(ctx context.Context, key string, data []byte, contentType string) error {
	return f.inner.Put(ctx, key, data, contentType)
}

// Open implements notificationuc.FileStore.
func (f announcementFiles) Open(ctx context.Context, key string) (io.ReadCloser, notificationuc.FileInfo, error) {
	rc, info, err := f.inner.Open(ctx, key)
	if errors.Is(err, propertydomain.ErrFileNotFound) {
		return nil, notificationuc.FileInfo{}, notificationdomain.ErrFileNotFound
	}
	return rc, notificationuc.FileInfo{Size: info.Size, ContentType: info.ContentType}, err
}

// Delete implements notificationuc.FileStore.
func (f announcementFiles) Delete(ctx context.Context, keys ...string) error {
	return f.inner.Delete(ctx, keys...)
}

// announcementImages verifies popup pictures with the image adapter and reports a refusal as the notification domain's
// error.
type announcementImages struct{ inner *imageproc.Processor }

// Process implements notificationuc.ImageProcessor.
func (p announcementImages) Process(ctx context.Context, data []byte) (notificationdomain.ProcessedImage, error) {
	img, err := p.inner.Process(ctx, data)
	switch {
	case errors.Is(err, propertydomain.ErrImageType):
		return notificationdomain.ProcessedImage{}, notificationdomain.ErrImageType
	case errors.Is(err, propertydomain.ErrImageCorrupt):
		return notificationdomain.ProcessedImage{}, notificationdomain.ErrImageCorrupt
	case errors.Is(err, propertydomain.ErrImageDimensions):
		return notificationdomain.ProcessedImage{}, notificationdomain.ErrImageDimensions
	case errors.Is(err, propertydomain.ErrImageTooLarge):
		return notificationdomain.ProcessedImage{}, notificationdomain.ErrImageTooLarge
	case err != nil:
		return notificationdomain.ProcessedImage{}, err
	}
	return notificationdomain.ProcessedImage{Data: img.Data, ContentType: img.ContentType, Ext: img.Ext, Width: img.Width, Height: img.Height}, nil
}

// mountMedia adds the public picture route. It is shared: the key says which context stored the file, so announcement
// pictures go to the notification handler and everything else to property's.
func mountMedia(v1 *gin.RouterGroup, property, announcements gin.HandlerFunc) {
	serve := func(ginCtx *gin.Context) {
		if strings.HasPrefix(strings.TrimPrefix(ginCtx.Param("key"), "/"), notificationdomain.ImageKeyPrefix) {
			announcements(ginCtx)
			return
		}
		property(ginCtx)
	}
	v1.GET("/media/*key", serve)
	v1.HEAD("/media/*key", serve)
}
