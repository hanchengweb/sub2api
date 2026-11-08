package mediatasks

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"strings"

	_ "golang.org/x/image/webp"
)

// modelImageFile accepts only inline bytes. There is no remote fetch or caller-owned supplier token.
// Files API accepts PNG/JPEG; WebP is decoded and converted before quota reservation.
func modelImageFile(raw json.RawMessage) ([]byte, string, error) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, "", ErrImageInvalid
	}
	header, encoded, ok := strings.Cut(value, ",")
	formats := map[string]string{"data:image/png;base64": "png", "data:image/jpeg;base64": "jpeg", "data:image/webp;base64": "webp"}
	format := formats[header]
	if !ok || format == "" || encoded == "" || len(encoded) > base64.StdEncoding.EncodedLen(MaxModelImageBytes) || strings.ContainsAny(encoded, "\r\n\t ") {
		return nil, "", ErrImageInvalid
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > MaxModelImageBytes || http.DetectContentType(data) != "image/"+format {
		return nil, "", ErrImageInvalid
	}
	// The WebP decoder may return its first image without consuming the complete RIFF.
	if format == "webp" && (len(data) < 12 || int64(binary.LittleEndian.Uint32(data[4:8]))+8 != int64(len(data))) {
		return nil, "", ErrImageInvalid
	}
	config, detected, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || detected != format || config.Width < 1 || config.Height < 1 ||
		config.Width > MaxModelImageDimension || config.Height > MaxModelImageDimension ||
		int64(config.Width)*int64(config.Height) > MaxModelImagePixels {
		return nil, "", ErrImageInvalid
	}
	decoded, detected, err := image.Decode(bytes.NewReader(data))
	if err != nil || detected != format {
		return nil, "", ErrImageInvalid
	}
	if format == "webp" {
		var converted modelImageBuffer
		if png.Encode(&converted, decoded) != nil {
			return nil, "", ErrImageInvalid
		}
		return converted.Bytes(), "image/png", nil
	}
	return data, "image/" + format, nil
}

type modelImageBuffer struct{ bytes.Buffer }

func (b *modelImageBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxModelImageBytes-b.Len() {
		return 0, ErrImageInvalid
	}
	return b.Buffer.Write(p)
}
