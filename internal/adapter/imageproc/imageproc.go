// Package imageproc adapts pkg/imaging to the image port of the property use cases: it reports a refused picture as
// one of the domain's errors.
package imageproc

import (
	"context"
	"errors"

	domain "rental-management/internal/domain/property"
	"rental-management/pkg/imaging"
)

// Processor verifies pictures with pkg/imaging.
type Processor struct{ inner *imaging.Processor }

// New returns a processor on the imaging one.
func New(inner *imaging.Processor) *Processor { return &Processor{inner: inner} }

// Process verifies data and returns the picture and thumbnail to store.
func (p *Processor) Process(ctx context.Context, data []byte) (domain.ProcessedImage, error) {
	res, err := p.inner.Process(ctx, data)
	if err != nil {
		return domain.ProcessedImage{}, refusal(err)
	}
	return domain.ProcessedImage{
		Data: res.Data, ContentType: res.ContentType, Ext: res.Ext, Width: res.Width, Height: res.Height, Thumb: res.Thumb,
	}, nil
}

// refusal turns what the image checks refuse into the domain's errors; any other error is returned as it is.
func refusal(err error) error {
	switch {
	case errors.Is(err, imaging.ErrUnsupported), errors.Is(err, imaging.ErrAnimated):
		return domain.ErrImageType
	case errors.Is(err, imaging.ErrDimensions):
		return domain.ErrImageDimensions
	case errors.Is(err, imaging.ErrCorrupt):
		return domain.ErrImageCorrupt
	case errors.Is(err, imaging.ErrTooLarge):
		return domain.ErrImageTooLarge
	}
	return err
}
