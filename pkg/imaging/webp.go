package imaging

import (
	"bytes"
	"encoding/binary"
)

// RIFF/WebP layout: "RIFF" <u32 le size> "WEBP" then chunks of <4cc> <u32 le size> <payload> <pad to even>.
const (
	riffHeaderBytes  = 12
	chunkHeaderBytes = 8
	// VP8X flag bits (first payload byte).
	vp8xFlagAnimation = 0x02
	vp8xFlagXMP       = 0x04
	vp8xFlagEXIF      = 0x08
)

type webpChunk struct {
	id      string
	payload []byte
}

// readWebPChunks splits a WebP file into its chunks. Sizes pointing beyond the buffer are ErrCorrupt. The size
// declared in the RIFF header decides where the file ends (bytes after it are ignored).
func readWebPChunks(data []byte) ([]webpChunk, error) {
	if len(data) < riffHeaderBytes || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil, ErrCorrupt
	}
	end := int(binary.LittleEndian.Uint32(data[4:8])) + 8
	if end < riffHeaderBytes || end > len(data) {
		return nil, ErrCorrupt
	}
	var chunks []webpChunk
	for off := riffHeaderBytes; off < end; {
		if off+chunkHeaderBytes > end {
			return nil, ErrCorrupt
		}
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		start := off + chunkHeaderBytes
		if size < 0 || start+size > end {
			return nil, ErrCorrupt
		}
		chunks = append(chunks, webpChunk{id: string(data[off : off+4]), payload: data[start : start+size]})
		off = start + size + size%2 // chunks are padded to an even length
	}
	return chunks, nil
}

// isAnimatedWebP reports whether the file holds an animation (VP8X animation flag, or an ANIM/ANMF chunk).
func isAnimatedWebP(chunks []webpChunk) bool {
	for _, c := range chunks {
		switch c.id {
		case "ANIM", "ANMF":
			return true
		case "VP8X":
			if len(c.payload) > 0 && c.payload[0]&vp8xFlagAnimation != 0 {
				return true
			}
		}
	}
	return false
}

// StripWebPMetadata returns the WebP file without its EXIF and XMP chunks (and with the matching VP8X flags
// cleared). The image data is not touched. A file whose chunk sizes do not fit is ErrCorrupt.
func StripWebPMetadata(data []byte) ([]byte, error) {
	chunks, err := readWebPChunks(data)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	body.WriteString("WEBP")
	for _, c := range chunks {
		if c.id == "EXIF" || c.id == "XMP " {
			continue
		}
		payload := c.payload
		if c.id == "VP8X" && len(payload) > 0 {
			payload = append([]byte(nil), payload...)
			payload[0] &^= vp8xFlagEXIF | vp8xFlagXMP
		}
		body.WriteString(c.id)
		_ = binary.Write(&body, binary.LittleEndian, uint32(len(payload))) //nolint:gosec // a chunk of a file under 2 MiB
		body.Write(payload)
		if len(payload)%2 == 1 {
			body.WriteByte(0)
		}
	}
	out := make([]byte, 0, body.Len()+8)
	out = append(out, "RIFF"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(body.Len())) //nolint:gosec // see above
	return append(out, body.Bytes()...), nil
}
