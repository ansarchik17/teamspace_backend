package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestNWS003_ParseVideoEmbed(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		wantProvider string
		wantEmbedURL string
		wantVideoID  string
		wantErr      bool
	}{
		{
			name:         "YouTube watch URL",
			url:          "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			wantProvider: "youtube",
			wantEmbedURL: "https://www.youtube.com/embed/dQw4w9WgXcQ",
			wantVideoID:  "dQw4w9WgXcQ",
			wantErr:      false,
		},
		{
			name:         "YouTube youtu.be short URL",
			url:          "https://youtu.be/dQw4w9WgXcQ",
			wantProvider: "youtube",
			wantEmbedURL: "https://www.youtube.com/embed/dQw4w9WgXcQ",
			wantVideoID:  "dQw4w9WgXcQ",
			wantErr:      false,
		},
		{
			name:         "YouTube shorts URL",
			url:          "https://www.youtube.com/shorts/12345abcde",
			wantProvider: "youtube",
			wantEmbedURL: "https://www.youtube.com/embed/12345abcde",
			wantVideoID:  "12345abcde",
			wantErr:      false,
		},
		{
			name:         "YouTube embed URL",
			url:          "https://www.youtube.com/embed/dQw4w9WgXcQ",
			wantProvider: "youtube",
			wantEmbedURL: "https://www.youtube.com/embed/dQw4w9WgXcQ",
			wantVideoID:  "dQw4w9WgXcQ",
			wantErr:      false,
		},
		{
			name:         "RuTube video URL",
			url:          "https://rutube.ru/video/abcdef1234567890/",
			wantProvider: "rutube",
			wantEmbedURL: "https://rutube.ru/play/embed/abcdef1234567890",
			wantVideoID:  "abcdef1234567890",
			wantErr:      false,
		},
		{
			name:         "Vimeo video URL",
			url:          "https://vimeo.com/123456789",
			wantProvider: "vimeo",
			wantEmbedURL: "https://player.vimeo.com/video/123456789",
			wantVideoID:  "123456789",
			wantErr:      false,
		},
		{
			name:         "Direct MP4 video URL",
			url:          "https://example.com/media/video.mp4",
			wantProvider: "direct",
			wantEmbedURL: "https://example.com/media/video.mp4",
			wantVideoID:  "",
			wantErr:      false,
		},
		{
			name:    "Empty URL returns error",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, embedURL, videoID, err := domain.ParseVideoEmbed(tt.url)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantProvider, provider)
			assert.Equal(t, tt.wantEmbedURL, embedURL)
			assert.Equal(t, tt.wantVideoID, videoID)
		})
	}
}

func TestNWS003_ValidatePublicationContentDoc(t *testing.T) {
	t.Run("valid doc with rich text formatting including underline and code", func(t *testing.T) {
		doc := &domain.ContentDoc{
			Type:    "doc",
			Version: 1,
			Blocks: []domain.ContentBlock{
				{
					ID:   "p1",
					Type: domain.PublicationBlockTypeParagraph,
					Runs: []domain.ContentRun{
						{Text: "Bold text", Marks: []string{"bold"}},
						{Text: "Underlined text", Marks: []string{"underline"}},
						{Text: "Code snippet", Marks: []string{"code"}},
						{Text: "Colored text", Marks: []string{"color:#ff0000"}},
					},
				},
				{
					ID:    "h1",
					Type:  domain.PublicationBlockTypeHeading,
					Level: 2,
					Runs: []domain.ContentRun{
						{Text: "Heading Title", Marks: []string{"bold"}},
					},
				},
			},
		}

		err := domain.ValidatePublicationContentDoc(doc)
		assert.NoError(t, err)
	})

	t.Run("valid doc with gallery, video and file blocks", func(t *testing.T) {
		doc := &domain.ContentDoc{
			Type:    "doc",
			Version: 1,
			Blocks: []domain.ContentBlock{
				{
					ID:      "gal1",
					Type:    domain.PublicationBlockTypeGallery,
					Caption: "Photo gallery of the event",
					Images: []domain.GalleryItem{
						{URL: "https://example.com/img1.jpg", Caption: "Photo 1"},
						{URL: "https://example.com/img2.jpg", Caption: "Photo 2"},
					},
				},
				{
					ID:       "vid1",
					Type:     domain.PublicationBlockTypeVideo,
					EmbedURL: "https://www.youtube.com/embed/dQw4w9WgXcQ",
					Provider: "youtube",
					Caption:  "Video interview",
				},
				{
					ID:       "file1",
					Type:     domain.PublicationBlockTypeFile,
					URL:      "https://example.com/files/report.pdf",
					FileName: "Report2026.pdf",
					FileSize: 1024567,
					MimeType: "application/pdf",
				},
			},
		}

		err := domain.ValidatePublicationContentDoc(doc)
		assert.NoError(t, err)
	})

	t.Run("empty gallery block returns validation error", func(t *testing.T) {
		doc := &domain.ContentDoc{
			Type:    "doc",
			Version: 1,
			Blocks: []domain.ContentBlock{
				{
					ID:     "gal_empty",
					Type:   domain.PublicationBlockTypeGallery,
					Images: []domain.GalleryItem{},
				},
			},
		}

		err := domain.ValidatePublicationContentDoc(doc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must contain at least 1 image")
	})

	t.Run("video block without embed_url or url returns error", func(t *testing.T) {
		doc := &domain.ContentDoc{
			Type:    "doc",
			Version: 1,
			Blocks: []domain.ContentBlock{
				{
					ID:   "vid_empty",
					Type: domain.PublicationBlockTypeVideo,
				},
			},
		}

		err := domain.ValidatePublicationContentDoc(doc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing embed_url or url")
	})

	t.Run("file block without url or attachmentId returns error", func(t *testing.T) {
		doc := &domain.ContentDoc{
			Type:    "doc",
			Version: 1,
			Blocks: []domain.ContentBlock{
				{
					ID:       "file_empty",
					Type:     domain.PublicationBlockTypeFile,
					FileName: "orphan.pdf",
				},
			},
		}

		err := domain.ValidatePublicationContentDoc(doc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing attachmentId or url")
	})
}

func TestNWS003_ExtractPublicationPlainText(t *testing.T) {
	doc := &domain.ContentDoc{
		Type:    "doc",
		Version: 1,
		Blocks: []domain.ContentBlock{
			{
				ID:   "p1",
				Type: domain.PublicationBlockTypeParagraph,
				Runs: []domain.ContentRun{
					{Text: "Welcome to our portal."},
				},
			},
			{
				ID:      "gal1",
				Type:    domain.PublicationBlockTypeGallery,
				Caption: "Opening Ceremony",
				Images: []domain.GalleryItem{
					{Caption: "Ribbon cutting"},
				},
			},
			{
				ID:       "file1",
				Type:     domain.PublicationBlockTypeFile,
				FileName: "Protocol.pdf",
			},
		},
	}

	text := domain.ExtractPublicationPlainText(doc)
	assert.Contains(t, text, "Welcome to our portal.")
	assert.Contains(t, text, "Opening Ceremony")
	assert.Contains(t, text, "Ribbon cutting")
	assert.Contains(t, text, "Protocol.pdf")
}
