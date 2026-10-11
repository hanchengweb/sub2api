package service

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// Organization routing can replace ToAPIs with Ark, whose image fields differ.
func normalizeSeedreamArkRequest(body []byte, contentType, baseURL string) ([]byte, error) {
	target, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(target.Hostname(), "ark.cn-beijing.volces.com") || !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return body, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	model, _ := payload["model"].(string)
	if !strings.HasPrefix(model, "doubao-seedream-5-0-pro") {
		return body, nil
	}
	if n, exists := payload["n"]; exists && n != float64(1) {
		return nil, fmt.Errorf("Seedream Pro supports one image per request")
	}
	metadata, _ := payload["metadata"].(map[string]any)
	resolution, _ := metadata["resolution"].(string)
	if resolution == "" {
		resolution, _ = payload["resolution"].(string)
	}
	size, _ := payload["size"].(string)
	if width, height, ratio := strings.Cut(size, ":"); ratio {
		w, wErr := strconv.ParseFloat(width, 64)
		h, hErr := strconv.ParseFloat(height, 64)
		if wErr != nil || hErr != nil || w <= 0 || h <= 0 || math.IsNaN(w/h) || w/h < 1.0/16 || w/h > 16 {
			return nil, fmt.Errorf("Seedream Pro size must use an aspect ratio between 1:16 and 16:1")
		}
		edge := 1024.0
		switch strings.ToUpper(resolution) {
		case "", "1K":
		case "1.5K":
			edge = 1536
		case "2K":
			edge = 2048
		default:
			return nil, fmt.Errorf("Seedream Pro resolution must be 1K, 1.5K or 2K")
		}
		// Ark accepts explicit pixels with 921600..4624220 total pixels.
		pixelWidth := int(math.Round(edge*math.Sqrt(w/h)/16)) * 16
		pixelHeight := int(math.Round(edge/math.Sqrt(w/h)/16)) * 16
		payload["size"] = fmt.Sprintf("%dx%d", pixelWidth, pixelHeight)
	} else if (size == "" || size == "auto") && resolution != "" {
		payload["size"] = strings.ToUpper(resolution)
	}
	if watermark, exists := metadata["watermark"]; exists {
		if _, ok := watermark.(bool); !ok {
			return nil, fmt.Errorf("Seedream watermark must be a boolean")
		}
		payload["watermark"] = watermark
	}
	if _, exists := payload["image"]; !exists {
		if images, ok := payload["image_urls"]; ok {
			payload["image"] = images
		} else if images, ok := payload["reference_images"]; ok {
			payload["image"] = images
		}
	}
	for _, key := range []string{"n", "metadata", "resolution", "quality", "image_urls", "reference_images"} {
		delete(payload, key)
	}
	return json.Marshal(payload)
}
