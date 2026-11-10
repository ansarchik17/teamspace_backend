package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestDrivePreviewFormats(t *testing.T) {
	for _, tc := range []struct{ name, mime, kind, canonical string }{
		{"Report.PDF", "application/pdf", "pdf", "application/pdf"},
		{"photo.jpg", "application/octet-stream", "image", "image/jpeg"},
		{"photo.png", "image/png; charset=binary", "image", "image/png"},
		{"clip.webm", "video/webm", "video", "video/webm"},
		{"sound.wav", "audio/x-wav", "audio", "audio/wav"},
		{"presentation.pptx", "application/zip", "office", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"report.docx", "", "office", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"attack.html", "application/pdf", "unsupported", "application/octet-stream"},
		{"attack.svg", "image/svg+xml", "unsupported", "application/octet-stream"},
		{"attack.pdf", "text/html", "unsupported", "application/octet-stream"},
		{"attack.png", "image/svg+xml", "unsupported", "application/octet-stream"},
		{"picture.png", "invalid;", "unsupported", "application/octet-stream"},
		{"archive.zip", "application/zip", "unsupported", "application/octet-stream"},
		{"no-extension", "application/pdf", "unsupported", "application/octet-stream"},
	} {
		t.Run(tc.name+tc.mime, func(t *testing.T) {
			f := classifyDrivePreview(tc.name, tc.mime)
			require.Equal(t, tc.kind, f.kind)
			require.Equal(t, tc.canonical, f.mime)
		})
	}
}

func TestDrivePreviewDescriptor(t *testing.T) {
	s := &DriveService{publicURL: "https://drive.example/"}
	file := &domain.DriveFile{ID: uuid.New(), RealName: "movie.mp4", MimeType: "video/mp4", Size: 1500, Version: 2}
	p := s.describeFilePreview(file)
	require.True(t, p.Supported)
	require.True(t, p.AuthRequired)
	require.Equal(t, "https://drive.example/api/v1/drive/files/"+file.ID.String()+"/preview/content", p.PreviewURL)
	require.NotContains(t, p.PreviewURL, "token")
	file.RealName = "report.doc"
	p = s.describeFilePreview(file)
	require.Contains(t, p.PreviewURL, "/preview/office")
	file.RealName = "page.html"
	p = s.describeFilePreview(file)
	require.False(t, p.Supported)
	require.Empty(t, p.PreviewURL)
	require.Equal(t, "unsupported_format", p.Reason)
	require.NotEmpty(t, p.DownloadURL)
}
