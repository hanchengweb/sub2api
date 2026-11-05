package voiceassets

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"io"
	"os/exec"
	"time"
)

var decodingSlots = make(chan struct{}, 2)

type boundedPCM struct{ b bytes.Buffer }

func (w *boundedPCM) Write(b []byte) (int, error) {
	if w.b.Len()+len(b) > 61*16000*2 {
		return 0, mediatasks.ErrInvalid
	}
	return w.b.Write(b)
}
func DecoderAvailable() bool { _, e := exec.LookPath("ffmpeg"); return e == nil }

// DecodeSample never receives a filesystem path, permits only pipe protocol,
// and emits a metadata-free mono PCM WAV. No original sample is retained.
func DecodeSample(ctx context.Context, audio []byte, format string) ([]byte, error) {
	if !ValidSample(audio, format) {
		return nil, mediatasks.ErrInvalid
	}
	select {
	case decodingSlots <- struct{}{}:
		defer func() { <-decodingSlots }()
	default:
		return nil, mediatasks.ErrBusy
	}
	path, e := exec.LookPath("ffmpeg")
	if e != nil {
		return nil, mediatasks.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-nostdin", "-v", "error", "-xerror", "-err_detect", "explode", "-protocol_whitelist", "pipe", "-threads", "1", "-filter_threads", "1", "-i", "pipe:0", "-map", "0:a:0", "-vn", "-sn", "-dn", "-t", "61", "-ac", "1", "-ar", "16000", "-f", "s16le", "pipe:1")
	cmd.Stdin = bytes.NewReader(audio)
	cmd.Stderr = io.Discard
	var pcm boundedPCM
	cmd.Stdout = &pcm
	if e = cmd.Run(); e != nil {
		return nil, mediatasks.ErrInvalid
	}
	b := pcm.b.Bytes()
	if len(b) < 16000*2 || len(b) > 60*16000*2 {
		return nil, mediatasks.ErrInvalid
	}
	wav := make([]byte, 44+len(b))
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], 1)
	binary.LittleEndian.PutUint32(wav[24:28], 16000)
	binary.LittleEndian.PutUint32(wav[28:32], 32000)
	binary.LittleEndian.PutUint16(wav[32:34], 2)
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:44], uint32(len(b)))
	copy(wav[44:], b)
	return wav, nil
}

// File signatures are checked before any supplier submission. The provider
// performs acoustic training validation; filename, MIME and client duration are
// never treated as evidence of a valid recording.
func ValidSample(b []byte, format string) bool {
	if len(b) == 0 || len(b) > MaxSampleBytes {
		return false
	}
	switch format {
	case "mp3":
		return mediatasks.ValidMP3(b)
	case "m4a":
		if len(b) < 24 || string(b[4:8]) != "ftyp" {
			return false
		}
		size := int(binary.BigEndian.Uint32(b[:4]))
		return size >= 16 && size <= len(b) && bytes.Contains(b[8:size], []byte("M4A "))
	case "wav":
		if len(b) < 44 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(b[4:8]))+8 != uint64(len(b)) {
			return false
		}
		var rate uint32
		var data uint32
		for at := 12; at+8 <= len(b); {
			n := int(binary.LittleEndian.Uint32(b[at+4 : at+8]))
			if n < 0 || n > len(b)-at-8 {
				return false
			}
			part := b[at+8 : at+8+n]
			switch string(b[at : at+4]) {
			case "fmt ":
				if n < 16 || binary.LittleEndian.Uint16(part[:2]) != 1 {
					return false
				}
				channels := binary.LittleEndian.Uint16(part[2:4])
				bits := binary.LittleEndian.Uint16(part[14:16])
				if channels < 1 || channels > 2 || (bits != 16 && bits != 24 && bits != 32) {
					return false
				}
				rate = binary.LittleEndian.Uint32(part[8:12])
			case "data":
				data = uint32(n)
			}
			at += 8 + n + (n % 2)
		}
		return rate > 0 && data > 0 && float64(data)/float64(rate) >= 1 && float64(data)/float64(rate) <= 60
	}
	return false
}
