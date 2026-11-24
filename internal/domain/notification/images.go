package notification

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ImageKeyPrefix is the start of every storage key the announcement pictures use; the shared public picture route sends
// keys that start with it to this context.
const ImageKeyPrefix = "announcements/"

// Image is one picture of a popup (a slide).
type Image struct {
	ID          uuid.UUID
	FileKey     string
	ContentType string
	Width       int
	Height      int
	SizeBytes   int
	// LinkURL is where pressing the picture goes (platform messages only); "" for none.
	LinkURL      string
	DisplayOrder int
}

// NewImage is a picture to add.
type NewImage struct {
	FileKey     string
	ContentType string
	Width       int
	Height      int
	SizeBytes   int
	LinkURL     string
}

// ArrangeItem is one picture of the new order. LinkURL nil keeps the link it had; an empty string clears it.
type ArrangeItem struct {
	ID      uuid.UUID
	LinkURL *string
}

// ProcessedImage is a verified picture ready to store.
type ProcessedImage struct {
	Data        []byte
	ContentType string
	// Ext is "webp" or "jpg".
	Ext    string
	Width  int
	Height int
}

// MaxImages is how many pictures the popup of a sender of the kind may have.
func MaxImages(k SenderKind, platformMax, landlordMax int) int {
	if k == SenderLandlord {
		return landlordMax
	}
	return platformMax
}

// ImageKey builds the storage key of a picture from the date and 16 random bytes. The name is unguessable and never
// reused, so the file can be cached for ever.
func ImageKey(now time.Time, random [16]byte, ext string) string {
	return fmt.Sprintf("%s%04d/%02d/%x.%s", ImageKeyPrefix, now.Year(), int(now.Month()), random[:], ext)
}
