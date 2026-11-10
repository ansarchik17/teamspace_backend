package service

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"net/url"
	"testing"
)

func TestDocumentConversionFormats(t *testing.T) {
	for _, format := range []string{"docx", "xlsx", "pptx"} {
		content, err := blankOfficeDocument(format)
		require.NoError(t, err)
		require.NoError(t, validateOfficeContainer(bytes.NewReader(content), int64(len(content)), format))
		for _, other := range []string{"docx", "xlsx", "pptx"} {
			if other != format {
				require.Error(t, validateOfficeContainer(bytes.NewReader(content), int64(len(content)), other))
			}
		}
		require.True(t, officeExportAllowed(format, "pdf"))
		require.True(t, officeExportAllowed(format, format))
	}
	for _, pair := range [][2]string{{"doc", "docx"}, {"xls", "xlsx"}, {"ppt", "pptx"}, {"pdf", "pdf"}} {
		require.True(t, officeExportAllowed(pair[0], pair[1]))
	}
	for _, pair := range [][2]string{{"pdf", "docx"}, {"docx", "xlsx"}, {"docx", "exe"}, {"zip", "pdf"}, {"pptx", "docx"}} {
		require.False(t, officeExportAllowed(pair[0], pair[1]))
	}
	for _, format := range []string{"docx", "doc", "xlsx", "xls", "pptx", "ppt", "pdf"} {
		require.Error(t, validateOfficeContainer(bytes.NewReader([]byte("not an office file")), 18, format))
	}
}

func TestDocumentConversionURLBoundary(t *testing.T) {
	server, err := url.Parse("https://office.example.com")
	require.NoError(t, err)
	for _, raw := range []string{"https://evil.example/file", "//evil.example/file", "http://office.example.com/file", "https://user:pass@office.example.com/file", "file:///etc/passwd"} {
		_, err := conversionResultURL(raw, server)
		require.Error(t, err)
	}
	for _, raw := range []string{"/cache/file.pdf", "https://office.example.com/cache/file.pdf"} {
		_, err := conversionResultURL(raw, server)
		require.NoError(t, err)
	}
	for _, raw := range []string{"/onlyoffice", "https://office.example.com?secret=x", "file:///tmp", "http://user:pass@office.test"} {
		t.Setenv("ONLYOFFICE_INTERNAL_URL", raw)
		_, err := conversionServerURL()
		require.Error(t, err)
	}
}
