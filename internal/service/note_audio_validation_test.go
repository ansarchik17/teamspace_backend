package service

import (
	"bytes"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAudioFormatSniffing(t *testing.T) {
	cases := []struct {
		name   string
		header []byte
		want   string
	}{
		{"wav", []byte("RIFF1234WAVEfmt "), ".wav"},
		{"m4a", []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}, ".m4a"},
		{"webm", []byte{0x1a, 0x45, 0xdf, 0xa3}, ".webm"},
		{"ogg", []byte("OggS1234"), ".ogg"},
		{"flac", []byte("fLaC1234"), ".flac"},
		{"mp3", []byte("ID3\x04\x00"), ".mp3"},
		{"aac", []byte{0xff, 0xf1}, ".aac"},
		{"html disguised as mp3", []byte("<html><script>"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, sniffAudioExtension(tc.header)) })
	}
	// A valid short header remains recognizable when read via a buffered stream.
	require.Equal(t, ".wav", sniffAudioExtension(bytes.NewBufferString("RIFF1234WAVE").Bytes()))
}

func TestAudioUploadMetadataValidation(t *testing.T) {
	require.NoError(t, validateAudioFilename("recording.m4a"))
	for _, name := range []string{"../recording.mp3", "voice.mp3\r\nX-Header: evil", "malware.html"} {
		require.Error(t, validateAudioFilename(name))
	}
	require.Equal(t, ".m4a", audioExtensionForMIME(" AUDIO/MP4; codecs=mp4a.40.2 "))
	require.Error(t, validateAudioMetadata(nil, []float64{math.NaN()}))
	negative := -1.0
	require.Error(t, validateAudioMetadata(&negative, nil))
	require.NoError(t, validateAudioMetadata(nil, []float64{0, 0.5, 1}))
}
