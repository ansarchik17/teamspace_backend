package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestDocumentImportExportIntegration(t *testing.T) {
	ctx, _, s := newDrivePostgresTest(t)
	var mu sync.Mutex
	objects := map[string][]byte{}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPut {
			var reader io.Reader = r.Body
			if strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") || strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
				reader = httputil.NewChunkedReader(r.Body)
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				http.Error(w, "read", 500)
				return
			}
			objects[r.URL.Path] = data
			w.Header().Set("ETag", `"fixture"`)
			return
		}
		if r.Method == http.MethodDelete {
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
			return
		}
		data, ok := objects[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", `"fixture"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		http.ServeContent(w, r, "file", time.Unix(1, 0), bytes.NewReader(data))
	}))
	defer storage.Close()
	client, err := minio.New(strings.TrimPrefix(storage.URL, "http://"), &minio.Options{Region: "us-east-1"})
	require.NoError(t, err)
	s.fileSvc = &FileService{minioClient: client, bucket: "test", logger: zap.NewNop()}
	t.Setenv("ONLYOFFICE_JWT_SECRET", "conversion-secret")
	var mode string
	var convertHook func()
	var seenKey string
	var expectedSource string
	var expectedVersion int
	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/converter" {
			var body map[string]interface{}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, true, body["async"])
			require.Equal(t, expectedSource, body["filetype"])
			token, err := jwt.Parse(body["token"].(string), func(*jwt.Token) (interface{}, error) { return []byte("conversion-secret"), nil }, jwt.WithValidMethods([]string{"HS256"}))
			require.NoError(t, err)
			require.Equal(t, body["key"], token.Claims.(jwt.MapClaims)["key"])
			u, err := url.Parse(body["url"].(string))
			require.NoError(t, err)
			claims, err := ValidateOnlyOfficeToken(u.Query().Get("token"), "secret")
			require.NoError(t, err)
			require.Equal(t, expectedVersion, claims.Version)
			if mode == "pending" {
				seenKey = body["key"].(string)
				w.Write([]byte(`{"endConvert":false}`))
				return
			}
			if seenKey != "" {
				require.Equal(t, seenKey, body["key"])
			}
			if convertHook != nil {
				convertHook()
			}
			if mode == "error" {
				w.Write([]byte(`{"error":-3}`))
				return
			}
			if mode == "external" {
				w.Write([]byte(`{"endConvert":true,"fileUrl":"http://forbidden.invalid/file"}`))
				return
			}
			w.Write([]byte(`{"endConvert":true,"fileUrl":"/result"}`))
			return
		}
		if mode == "invalid" {
			w.Write([]byte("unexpected html"))
			return
		}
		if expectedSource == "doc" {
			content, err := blankOfficeDocument("docx")
			require.NoError(t, err)
			w.Write(content)
			return
		}
		w.Write([]byte("%PDF-1.7\nmock converted content\n%%EOF"))
	}))
	defer converter.Close()
	t.Setenv("ONLYOFFICE_INTERNAL_URL", converter.URL)
	for _, format := range []string{"docx", "xlsx", "pptx", "pdf", "doc", "xls", "ppt"} {
		t.Run(format, func(t *testing.T) {
			mode = ""
			seenKey = ""
			convertHook = nil
			expectedSource = format
			expectedVersion = 1
			var original []byte
			switch format {
			case "pdf":
				original = []byte("%PDF-1.7\noriginal pdf\n%%EOF")
			case "doc", "xls", "ppt":
				original = append([]byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}, []byte("legacy fixture")...)
			default:
				original, err = blankOfficeDocument(format)
				require.NoError(t, err)
			}
			file, err := s.ImportDocument(ctx, 1, nil, "Styled."+format, int64(len(original)), bytes.NewReader(original))
			require.NoError(t, err)
			require.Equal(t, 1, file.Version)
			require.Equal(t, officeImportMIME(format), file.MimeType)
			exported, err := s.ExportDocument(ctx, 1, file.ID, 1, format, "secret", "https://api.example.com")
			require.NoError(t, err)
			actual, err := io.ReadAll(exported.Reader)
			exported.Reader.Close()
			require.NoError(t, err)
			require.Equal(t, original, actual)
			require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, file.ID, 3, domain.RoleViewer))
			_, err = s.ExportDocument(ctx, 5, file.ID, 1, format, "secret", "https://api.example.com")
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			if format == "pdf" {
				return
			}
			target := "pdf"
			if format == "doc" {
				target = "docx"
			}
			mode = "pending"
			exported, err = s.ExportDocument(ctx, 3, file.ID, 1, target, "secret", "https://api.example.com")
			require.NoError(t, err)
			require.True(t, exported.Pending)
			require.Equal(t, 1, exported.Version)
			mode = ""
			exported, err = s.ExportDocument(ctx, 3, file.ID, 1, target, "secret", "https://api.example.com")
			require.NoError(t, err)
			require.False(t, exported.Pending)
			actual, err = io.ReadAll(exported.Reader)
			require.NoError(t, err)
			exported.Reader.Close()
			require.NotEmpty(t, actual)
			after, err := s.repo.GetFileByID(ctx, file.ID)
			require.NoError(t, err)
			require.Equal(t, 1, after.Version)
			require.Equal(t, file.ObjectName, after.ObjectName)
			for _, bad := range []struct {
				mode   string
				status int
			}{{"error", 422}, {"external", 502}, {"invalid", 502}} {
				mode = bad.mode
				_, err = s.ExportDocument(ctx, 3, file.ID, 1, target, "secret", "https://api.example.com")
				require.Equal(t, bad.status, apperrors.ToAppError(err).StatusCode)
			}
			mode = ""
			convertHook = func() { require.NoError(t, s.RevokePermission(ctx, 1, domain.ResourceFile, file.ID, 3)) }
			_, err = s.ExportDocument(ctx, 3, file.ID, 1, target, "secret", "https://api.example.com")
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
			convertHook = nil
		})
	}
	_, err = s.ImportDocument(ctx, 1, nil, "fake.docx", 20, strings.NewReader("not an office archive"))
	require.Error(t, err)
	_, err = s.ImportDocument(ctx, 1, nil, "large.docx", MaxDocumentSize+1, strings.NewReader(""))
	require.Equal(t, 413, apperrors.ToAppError(err).StatusCode)
	folder, err := s.CreateFolder(ctx, 1, nil, "private imports")
	require.NoError(t, err)
	content, err := blankOfficeDocument("docx")
	require.NoError(t, err)
	_, err = s.ImportDocument(ctx, 3, &folder.ID, "denied.docx", int64(len(content)), bytes.NewReader(content))
	require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
}
