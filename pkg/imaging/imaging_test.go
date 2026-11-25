package imaging

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/kolesa-team/go-webp/v2/webp"
	_ "golang.org/x/image/webp"
)

var testLimits = Limits{MaxBytes: 2 << 20, MaxDimension: 2048, ThumbMaxSize: 480}

// tinyWebP is an 8x8 lossless WebP (made with cwebp from a gradient).
func tinyWebP(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("UklGRi4AAABXRUJQVlA4TCIAAAAvB8ABALkyRPQ/dhHR/wBhtlFJzh90ryMYjwGMCaB6oP8A")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// riff assembles a WebP file from chunks.
func riff(chunks ...webpChunk) []byte {
	var body []byte
	body = append(body, "WEBP"...)
	for _, c := range chunks {
		body = append(body, c.id...)
		body = binary.LittleEndian.AppendUint32(body, uint32(len(c.payload))) //nolint:gosec // test fixture
		body = append(body, c.payload...)
		if len(c.payload)%2 == 1 {
			body = append(body, 0)
		}
	}
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body))) //nolint:gosec // test fixture
	return append(out, body...)
}

// extendedWebP wraps the tiny image in a VP8X container with the given flags and extra chunks.
func extendedWebP(t *testing.T, flags byte, extra ...webpChunk) []byte {
	t.Helper()
	image, err := readWebPChunks(tinyWebP(t))
	if err != nil || len(image) != 1 {
		t.Fatalf("fixture chunks: %v %v", image, err)
	}
	vp8x := []byte{flags, 0, 0, 0, 7, 0, 0, 7, 0, 0} // 8x8 canvas
	chunks := []webpChunk{{"VP8X", vp8x}, image[0]}
	return riff(append(chunks, extra...)...)
}

func solid(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodeJPEG1(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestProcessWebPStripsMetadataAndKeepsImage(t *testing.T) {
	src := extendedWebP(t, vp8xFlagEXIF|vp8xFlagXMP,
		webpChunk{"EXIF", []byte("Exif\x00\x00gps-data-here!")}, webpChunk{"XMP ", []byte("<x:xmpmeta/>")})
	p := NewProcessor(testLimits, 2)
	res, err := p.Process(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if res.ContentType != "image/webp" || res.Ext != "webp" || res.Width != 8 || res.Height != 8 {
		t.Errorf("result: %+v", res)
	}
	if bytes.Contains(res.Data, []byte("EXIF")) || bytes.Contains(res.Data, []byte("XMP ")) || bytes.Contains(res.Data, []byte("gps-data")) {
		t.Error("metadata still in the stored WebP")
	}
	chunks, err := readWebPChunks(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		if c.id == "VP8X" && c.payload[0]&(vp8xFlagEXIF|vp8xFlagXMP) != 0 {
			t.Error("VP8X still announces EXIF/XMP")
		}
	}
	if _, _, err := image.Decode(bytes.NewReader(res.Data)); err != nil {
		t.Errorf("stripped WebP no longer decodes: %v", err)
	}
	if _, err := webp.Decode(bytes.NewReader(res.Thumb), nil); err != nil {
		t.Errorf("thumb is not a WebP: %v", err)
	}
}

func TestProcessPlainWebPPassesThrough(t *testing.T) {
	res, err := NewProcessor(testLimits, 1).Process(context.Background(), tinyWebP(t))
	if err != nil || res.Ext != "webp" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, _, err := image.Decode(bytes.NewReader(res.Data)); err != nil {
		t.Error(err)
	}
}

func TestProcessJPEGBecomesWebPWithoutExif(t *testing.T) {
	plain := encodeJPEG1(t, solid(40, 30, color.RGBA{200, 10, 10, 255}))
	// Insert an APP1 Exif segment right after SOI.
	exif := append([]byte("Exif\x00\x00"), []byte("GPS-COORDINATES")...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(exif)+2)) //nolint:gosec // test fixture
	withExif := append(append(append([]byte{}, plain[:2]...), seg...), append(exif, plain[2:]...)...)
	if !bytes.Contains(withExif, []byte("Exif")) {
		t.Fatal("fixture has no Exif")
	}

	res, err := NewProcessor(testLimits, 1).Process(context.Background(), withExif)
	if err != nil {
		t.Fatal(err)
	}
	if res.ContentType != "image/webp" || res.Ext != "webp" || res.Width != 40 || res.Height != 30 {
		t.Errorf("result: %+v", res)
	}
	if bytes.Contains(res.Data, []byte("Exif")) || bytes.Contains(res.Data, []byte("GPS")) {
		t.Error("Exif survived the re-encode")
	}
	if cfg, err := webp.DecodeConfig(bytes.NewReader(res.Data), nil); err != nil || cfg.Width != 40 || cfg.Height != 30 {
		t.Errorf("stored picture is not a 40x30 WebP: %+v %v", cfg, err)
	}
	if _, err := webp.Decode(bytes.NewReader(res.Thumb), nil); err != nil {
		t.Errorf("thumb is not a WebP: %v", err)
	}
}

func TestProcessPNGWithAlphaBecomesWebPOnWhite(t *testing.T) {
	src := encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 16, 16))) // fully transparent
	res, err := NewProcessor(testLimits, 1).Process(context.Background(), src)
	if err != nil || res.Ext != "webp" {
		t.Fatalf("%+v %v", res, err)
	}
	got, err := webp.Decode(bytes.NewReader(res.Data), nil)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := got.At(8, 8).RGBA()
	if r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
		t.Errorf("transparent pixel is not white: %d %d %d", r>>8, g>>8, b>>8)
	}
}

func TestProcessRefusals(t *testing.T) {
	p := NewProcessor(Limits{MaxBytes: 4096, MaxDimension: 100, ThumbMaxSize: 50}, 1)
	ctx := context.Background()

	cases := map[string]struct {
		data []byte
		want error
	}{
		"text":           {[]byte("hello, I am not a picture at all"), ErrUnsupported},
		"gif":            {[]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), ErrUnsupported},
		"empty":          {nil, ErrUnsupported},
		"too many bytes": {bytes.Repeat([]byte{0xFF}, 4097), ErrTooLarge},
		"animated flag":  {extendedWebP(t, vp8xFlagAnimation), ErrAnimated},
		"animated anim":  {extendedWebP(t, 0, webpChunk{"ANIM", []byte{0, 0, 0, 0, 0, 0}}), ErrAnimated},
		"too wide":       {encodePNG(t, image.NewRGBA(image.Rect(0, 0, 101, 5))), ErrDimensions},
		"too tall":       {encodeJPEG1(t, solid(5, 101, color.White)), ErrDimensions},
		"truncated jpeg": {encodeJPEG1(t, solid(20, 20, color.White))[:60], ErrCorrupt},
	}
	for name, tc := range cases {
		if _, err := p.Process(ctx, tc.data); !errors.Is(err, tc.want) {
			t.Errorf("%s: err %v, want %v", name, err, tc.want)
		}
	}
}

// A PNG header that claims 10000x10000 is refused from the header, long before any pixel would be decoded.
func TestProcessRefusesDecompressionBombFromHeader(t *testing.T) {
	src := encodePNG(t, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	// IHDR is the first chunk: 8 signature bytes, 4 length, 4 type, then width, height, ...
	binary.BigEndian.PutUint32(src[16:], 10000)
	binary.BigEndian.PutUint32(src[20:], 10000)
	binary.BigEndian.PutUint32(src[29:], crc32.ChecksumIEEE(src[12:29]))
	_, err := NewProcessor(testLimits, 1).Process(context.Background(), src)
	if !errors.Is(err, ErrDimensions) {
		t.Errorf("err = %v, want ErrDimensions", err)
	}
}

func TestThumbnailSize(t *testing.T) {
	p := NewProcessor(testLimits, 1)
	res, err := p.Process(context.Background(), encodePNG(t, solid(1000, 500, color.RGBA{0, 0, 255, 255})))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := webp.DecodeConfig(bytes.NewReader(res.Thumb), nil)
	if err != nil || cfg.Width != 480 || cfg.Height != 240 {
		t.Errorf("thumb %dx%d (%v), want 480x240", cfg.Width, cfg.Height, err)
	}
	// A small picture is not enlarged.
	small, err := p.Process(context.Background(), encodePNG(t, solid(30, 20, color.White)))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = webp.DecodeConfig(bytes.NewReader(small.Thumb), nil)
	if cfg.Width != 30 || cfg.Height != 20 {
		t.Errorf("small thumb %dx%d", cfg.Width, cfg.Height)
	}
}

func TestProcessWaitsForASlotAndHonoursCancellation(t *testing.T) {
	p := NewProcessor(testLimits, 1)
	p.sem <- struct{}{} // the only decode slot is busy
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Process(ctx, tinyWebP(t)); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	<-p.sem
	if _, err := p.Process(context.Background(), tinyWebP(t)); err != nil {
		t.Errorf("free slot: %v", err)
	}
}

func TestStripWebPMetadataRejectsMalformed(t *testing.T) {
	good := tinyWebP(t)
	bad := map[string][]byte{
		"short":       []byte("RIFF"),
		"not riff":    append([]byte("RIFX"), good[4:]...),
		"size beyond": append(append([]byte{}, good[:4]...), append([]byte{0xFF, 0xFF, 0, 0}, good[8:]...)...),
	}
	// A chunk that claims more bytes than the file has.
	chunk := append([]byte{}, good...)
	binary.LittleEndian.PutUint32(chunk[16:], 0xFFFF)
	bad["chunk beyond"] = chunk
	for name, data := range bad {
		if _, err := StripWebPMetadata(data); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: err %v, want ErrCorrupt", name, err)
		}
	}
}

// A stored picture from before the switch (JPEG key) is not touched by this package; processing a legacy JPEG
// upload still yields a WebP whose thumbnail is smaller than the original's long side limit.
func TestProcessPNGThumbIsWebPWithinLimit(t *testing.T) {
	res, err := NewProcessor(testLimits, 1).Process(context.Background(), encodePNG(t, solid(900, 300, color.RGBA{10, 120, 30, 255})))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := webp.DecodeConfig(bytes.NewReader(res.Thumb), nil)
	if err != nil || cfg.Width != 480 || cfg.Height != 160 {
		t.Errorf("thumb %+v %v", cfg, err)
	}
	if !bytes.HasPrefix(res.Thumb, []byte("RIFF")) || string(res.Thumb[8:12]) != "WEBP" {
		t.Error("thumb is not a RIFF/WEBP file")
	}
}
