// Package imaging checks, cleans and shrinks uploaded pictures. It is the only place that imports golang.org/x/image.
//
// The web app already scales pictures and encodes them as WebP; the server only verifies them. What it stores
// holds no metadata (EXIF/GPS): a WebP has its EXIF and XMP chunks removed, a JPEG or PNG is decoded and written
// again as lossy WebP (pure-Go encoder from github.com/kolesa-team/go-webp, no cgo). Everything stored is WebP.
package imaging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/png" // registers the PNG decoder for image.Decode
	"math"
	"net/http"

	"github.com/kolesa-team/go-webp/v2/encoder"
	"github.com/kolesa-team/go-webp/v2/webp"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder for image.Decode
)

// The reasons a picture is refused.
var (
	ErrUnsupported = errors.New("imaging: not a WebP, JPEG or PNG image")
	ErrTooLarge    = errors.New("imaging: file too large")
	ErrDimensions  = errors.New("imaging: image dimensions too large")
	ErrCorrupt     = errors.New("imaging: image cannot be decoded")
	ErrAnimated    = errors.New("imaging: animated images are not accepted")
)

const (
	storedWebPQuality = 85
	thumbWebPQuality  = 80
	// sniffBytes is how much of the file http.DetectContentType looks at.
	sniffBytes = 512
)

// Limits bound what is accepted and produced.
type Limits struct {
	MaxBytes     int // largest file
	MaxDimension int // longest side, in pixels
	ThumbMaxSize int // longest side of the thumbnail, in pixels
}

// Result is a verified picture ready to store.
type Result struct {
	Data        []byte // the picture to store
	ContentType string // always "image/webp"
	Ext         string // always "webp"
	Width       int
	Height      int
	Thumb       []byte // WebP
}

// Processor verifies pictures; at most `concurrency` full decodes run at once.
type Processor struct {
	limits Limits
	sem    chan struct{}
}

// NewProcessor builds a Processor (concurrency below 1 is treated as 1).
func NewProcessor(l Limits, concurrency int) *Processor {
	return &Processor{limits: l, sem: make(chan struct{}, max(concurrency, 1))}
}

// Process verifies data and returns what to store. The size, type and dimensions are checked from the header
// before any pixel is decoded, so a decompression bomb is refused cheaply. It waits for a free decode slot and
// gives up when ctx ends.
func (p *Processor) Process(ctx context.Context, data []byte) (Result, error) {
	if len(data) > p.limits.MaxBytes {
		return Result{}, ErrTooLarge
	}
	contentType := http.DetectContentType(data[:min(len(data), sniffBytes)])
	switch contentType {
	case "image/webp":
		chunks, err := readWebPChunks(data)
		if err != nil {
			return Result{}, err
		}
		if isAnimatedWebP(chunks) {
			return Result{}, ErrAnimated
		}
	case "image/jpeg", "image/png":
	default:
		return Result{}, ErrUnsupported
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > p.limits.MaxDimension || cfg.Height > p.limits.MaxDimension {
		return Result{}, ErrDimensions
	}

	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	res := Result{Width: cfg.Width, Height: cfg.Height}
	if contentType == "image/webp" {
		// A WebP is kept as it is (the client already encoded it), minus its metadata.
		if res.Data, err = StripWebPMetadata(data); err != nil {
			return Result{}, err
		}
		res.ContentType, res.Ext = "image/webp", "webp"
	} else {
		if res.Data, err = encodeWebP(onWhite(img, img.Bounds().Dx(), img.Bounds().Dy()), storedWebPQuality); err != nil {
			return Result{}, err
		}
		res.ContentType, res.Ext = "image/webp", "webp"
	}
	if res.Thumb, err = p.thumbnail(img); err != nil {
		return Result{}, err
	}
	return res, nil
}

// thumbnail scales img so its longest side is ThumbMaxSize (never enlarging) and encodes it as WebP.
func (p *Processor) thumbnail(img image.Image) ([]byte, error) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	longest := max(w, h)
	target := min(p.limits.ThumbMaxSize, longest)
	scale := float64(target) / float64(longest)
	tw, th := max(1, int(math.Round(float64(w)*scale))), max(1, int(math.Round(float64(h)*scale)))

	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	xdraw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, xdraw.Src)
	xdraw.BiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Over, nil)
	return encodeWebP(dst, thumbWebPQuality)
}

// onWhite copies img onto an opaque white canvas, so transparency becomes white (stored pictures are opaque, which keeps WebP small).
func onWhite(img image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, xdraw.Src)
	xdraw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, xdraw.Over)
	return dst
}

func encodeWebP(img image.Image, quality float32) ([]byte, error) {
	opts, err := encoder.NewLossyEncoderOptions(encoder.PresetPhoto, quality)
	if err != nil {
		return nil, fmt.Errorf("webp options: %w", err)
	}
	var buf bytes.Buffer
	if err := webp.Encode(&buf, img, opts); err != nil {
		return nil, fmt.Errorf("encode webp: %w", err)
	}
	return buf.Bytes(), nil
}
