package mediatasks

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func ValidMP3(b []byte) bool {
	if len(b) < 4 || len(b) > MaxAudioBytes {
		return false
	}
	offset := 0
	if string(b[:3]) == "ID3" {
		if len(b) < 10 || b[6]&128 != 0 || b[7]&128 != 0 || b[8]&128 != 0 || b[9]&128 != 0 {
			return false
		}
		offset = 10 + int(b[6])<<21 + int(b[7])<<14 + int(b[8])<<7 + int(b[9])
	}
	if offset+4 > len(b) {
		return false
	}
	h := b[offset : offset+4]
	version := (h[1] >> 3) & 3
	layer := (h[1] >> 1) & 3
	rateIndex := (h[2] >> 2) & 3
	bitIndex := h[2] >> 4
	if h[0] != 255 || h[1]&224 != 224 || version == 1 || layer != 1 || rateIndex == 3 || bitIndex == 0 || bitIndex == 15 {
		return false
	}
	rates := []int{44100, 48000, 32000}
	rate := rates[rateIndex]
	bitrates := []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	factor := 144
	if version != 3 {
		bitrates = []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
		rate /= 2
		factor = 72
		if version == 0 {
			rate /= 2
		}
	}
	frame := factor*bitrates[bitIndex]*1000/rate + int((h[2]>>1)&1)
	return frame >= 4 && offset+frame <= len(b)
}

// ValidateGLB requires glTF 2.0 with embedded buffers/images. Traversing every
// extension also prevents an extension from introducing an external URI.
func ValidateGLB(b []byte) error {
	invalid := errors.New("invalid_self_contained_glb")
	if len(b) < 24 || len(b) > MaxModelBytes || string(b[:4]) != "glTF" || binary.LittleEndian.Uint32(b[4:8]) != 2 || int(binary.LittleEndian.Uint32(b[8:12])) != len(b) {
		return invalid
	}
	offset := 12
	var document map[string]any
	binLength := 0
	chunks := 0
	for offset < len(b) {
		if offset+8 > len(b) {
			return invalid
		}
		size := int(binary.LittleEndian.Uint32(b[offset : offset+4]))
		typ := binary.LittleEndian.Uint32(b[offset+4 : offset+8])
		offset += 8
		if size%4 != 0 || size < 0 || size > len(b)-offset {
			return invalid
		}
		if chunks == 0 && typ == 0x4E4F534A {
			if json.Unmarshal(b[offset:offset+size], &document) != nil {
				return invalid
			}
		} else if chunks == 1 && typ == 0x004E4942 {
			binLength = size
		} else {
			return invalid
		}
		offset += size
		chunks++
	}
	if chunks < 1 || document == nil {
		return invalid
	}
	asset, ok := document["asset"].(map[string]any)
	if !ok || asset["version"] != "2.0" {
		return invalid
	}
	var external func(any) bool
	external = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for k, w := range x {
				if (strings.EqualFold(k, "uri") || strings.EqualFold(k, "url")) && w != "" {
					return true
				}
				if external(w) {
					return true
				}
			}
		case []any:
			for _, w := range x {
				if external(w) {
					return true
				}
			}
		}
		return false
	}
	if external(document) {
		return invalid
	}
	buffers, _ := document["buffers"].([]any)
	if len(buffers) > 1 {
		return invalid
	}
	if len(buffers) == 1 {
		m, ok := buffers[0].(map[string]any)
		if !ok {
			return invalid
		}
		n, ok := m["byteLength"].(float64)
		if !ok || n < 0 || n != float64(int64(n)) || int64(n) > int64(binLength) || int64(binLength)-int64(n) > 3 {
			return invalid
		}
	}
	views, _ := document["bufferViews"].([]any)
	for _, v := range views {
		m, ok := v.(map[string]any)
		if !ok || m["buffer"] != float64(0) {
			return invalid
		}
		n, ok := m["byteLength"].(float64)
		if !ok || n < 0 || n != float64(int64(n)) {
			return invalid
		}
		start := float64(0)
		if raw, exists := m["byteOffset"]; exists {
			var yes bool
			start, yes = raw.(float64)
			if !yes {
				return invalid
			}
		}
		if start < 0 || start != float64(int64(start)) || start+n > float64(binLength) {
			return invalid
		}
	}
	images, _ := document["images"].([]any)
	for _, v := range images {
		m, ok := v.(map[string]any)
		if !ok {
			return invalid
		}
		i, ok := m["bufferView"].(float64)
		if !ok || i < 0 || i != float64(int(i)) || int(i) >= len(views) {
			return invalid
		}
	}
	return nil
}

var deniedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("fec0::/10"), netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2002::/16"),
}

func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range deniedNetworks {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func ApprovedModelURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" && u.Port() != "443" || strings.ToLower(u.Hostname()) != "cdn.tripo3d.ai" {
		return nil, errors.New("model_download_host_unapproved")
	}
	return u, nil
}

// DownloadModel pins the verified public IP for the TLS connection. There are
// no credentials, proxies or redirects on the storage request.
func DownloadModel(ctx context.Context, raw string) ([]byte, error) {
	u, err := ApprovedModelURL(raw)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("model_download_dns_failed")
	}
	for _, ip := range addresses {
		if !PublicIP(ip) {
			return nil, errors.New("model_download_address_rejected")
		}
	}
	pinned := addresses[0].String()
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(host, u.Hostname()) || port != strconv.Itoa(443) {
			return nil, errors.New("model_download_destination_changed")
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(pinned, port))
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("model_download_failed")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 || res.ContentLength > MaxModelBytes {
		return nil, errors.New("model_download_rejected")
	}
	content, err := io.ReadAll(io.LimitReader(res.Body, MaxModelBytes+1))
	if err != nil {
		return nil, errors.New("model_download_failed")
	}
	if err = ValidateGLB(content); err != nil {
		return nil, err
	}
	return content, nil
}
