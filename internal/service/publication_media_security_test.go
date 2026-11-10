package service

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"testing"
)

func TestNWS003MediaOwnership(t *testing.T) {
	for _, raw := range []string{"image/8/photo.png", "javascript:alert(1)", "image/7/../8/photo.png", "https://api.test/api/v1/files/download?object_name=document%2F8%2Fsecret.pdf"} {
		if _, _, err := publicationMediaKey(7, raw); err == nil {
			t.Errorf("accepted unsafe or foreign media: %s", raw)
		}
	}
	key, storage, err := publicationMediaKey(7, "https://api.test/api/v1/files/download?object_name=image%2F7%2Fphoto.png")
	if err != nil || !storage || key != "image/7/photo.png" {
		t.Fatalf("owned download URL not normalized: %q %v %v", key, storage, err)
	}
}

func TestNWS003UploadContents(t *testing.T) {
	var validPNG bytes.Buffer
	if err := png.Encode(&validPNG, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		data    []byte
		gallery bool
		valid   bool
	}{
		{"photo.png", validPNG.Bytes(), true, true},
		{"photo.jpg", validPNG.Bytes(), true, false},
		{"photo.png", []byte("<script>alert(1)</script>"), true, false},
		{"file.pdf", []byte("%PDF-1.7\n%%EOF"), false, true},
		{"file.pdf", []byte("%PDF-1.7\ntruncated"), false, false},
		{"file.pdf", []byte("%PDF-1.7\n%%EOF"), true, false},
		{"file.svg", []byte("<svg/>"), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("files", tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(tc.data); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/", &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			if err := req.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer req.MultipartForm.RemoveAll()
			err = validatePublicationUpload(req.MultipartForm.File["files"][0], tc.gallery)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
