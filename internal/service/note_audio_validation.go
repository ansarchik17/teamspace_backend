package service

import (
	"bytes"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/west6ide/workspace-backend/internal/domain"
)

func audioExtensionForMIME(mime string) string {
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	switch base {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return ".m4a"
	case "audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave":
		return ".wav"
	case "audio/webm", "video/webm":
		return ".webm"
	case "audio/ogg", "application/ogg":
		return ".ogg"
	case "audio/aac", "audio/x-aac":
		return ".aac"
	case "audio/flac", "audio/x-flac":
		return ".flac"
	default:
		return ""
	}
}

func validateAudioFilename(filename string) error {
	if filename == "" || len(filename) > 255 || strings.ContainsAny(filename, `/\`) || filepath.Base(filename) != filename {
		return fmt.Errorf("invalid audio file name")
	}
	for _, r := range filename {
		if unicode.IsControl(r) {
			return fmt.Errorf("invalid audio file name")
		}
	}
	if !domain.IsAllowedAudioExtension(filename) {
		return fmt.Errorf("unsupported audio extension")
	}
	return nil
}

func validateAudioMetadata(duration *float64, waveform []float64) error {
	if duration != nil && (math.IsNaN(*duration) || math.IsInf(*duration, 0) || *duration < 0 || *duration > 24*60*60) {
		return fmt.Errorf("invalid audio duration")
	}
	if len(waveform) > 4096 {
		return fmt.Errorf("audio waveform is too long")
	}
	for _, value := range waveform {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return fmt.Errorf("invalid audio waveform")
		}
	}
	return nil
}

// sniffAudioExtension checks container signatures, not just a client-provided MIME label.
func sniffAudioExtension(header []byte) string {
	if len(header) >= 12 && bytes.Equal(header[:4], []byte("RIFF")) && bytes.Equal(header[8:12], []byte("WAVE")) {
		return ".wav"
	}
	if len(header) >= 12 && bytes.Equal(header[4:8], []byte("ftyp")) {
		return ".m4a"
	}
	if len(header) >= 4 && bytes.Equal(header[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
		return ".webm"
	}
	if len(header) >= 4 && bytes.Equal(header[:4], []byte("OggS")) {
		return ".ogg"
	}
	if len(header) >= 4 && bytes.Equal(header[:4], []byte("fLaC")) {
		return ".flac"
	}
	if len(header) >= 3 && bytes.Equal(header[:3], []byte("ID3")) {
		return ".mp3"
	}
	if len(header) >= 2 && header[0] == 0xff && header[1]&0xf6 == 0xf0 {
		return ".aac"
	}
	if len(header) >= 2 && header[0] == 0xff && header[1]&0xe0 == 0xe0 {
		return ".mp3"
	}
	return ""
}
