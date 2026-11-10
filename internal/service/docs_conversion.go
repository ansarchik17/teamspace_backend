package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func officeImportMIME(format string) string {
	switch format {
	case "pdf":
		return "application/pdf"
	case "doc":
		return "application/msword"
	case "xls":
		return "application/vnd.ms-excel"
	case "ppt":
		return "application/vnd.ms-powerpoint"
	default:
		return documentMIME(format)
	}
}

// Validate the container without rewriting document XML, styles or media.
func validateOfficeContainer(reader io.ReaderAt, size int64, format string) error {
	invalid := func() error { return apperrors.BadRequest("file contents do not match the office format") }
	header := make([]byte, 8)
	if _, err := reader.ReadAt(header, 0); err != nil {
		return invalid()
	}
	switch format {
	case "pdf":
		if !bytes.HasPrefix(header, []byte("%PDF-")) {
			return invalid()
		}
		return nil
	case "doc", "xls", "ppt":
		if !bytes.Equal(header, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}) {
			return invalid()
		}
		return nil
	}
	main := map[string]string{"docx": "word/document.xml", "xlsx": "xl/workbook.xml", "pptx": "ppt/presentation.xml"}[format]
	if main == "" {
		return invalid()
	}
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return invalid()
	}
	if len(archive.File) > 10000 {
		return invalid()
	}
	found := map[string]bool{}
	for _, f := range archive.File {
		if f.Name == main || f.Name == "[Content_Types].xml" {
			found[f.Name] = !f.FileInfo().IsDir() && f.UncompressedSize64 > 0
		}
	}
	if !found[main] || !found["[Content_Types].xml"] {
		return invalid()
	}
	return nil
}

type temporaryOfficeFile struct{ *os.File }

func (f *temporaryOfficeFile) Close() error {
	err := f.File.Close()
	_ = os.Remove(f.Name())
	return err
}

func bufferOfficeFile(reader io.Reader) (*temporaryOfficeFile, int64, error) {
	f, err := os.CreateTemp("", "docs-conversion-*")
	if err != nil {
		return nil, 0, err
	}
	result := &temporaryOfficeFile{f}
	size, err := io.Copy(f, io.LimitReader(reader, MaxDocumentSize+1))
	if err != nil {
		result.Close()
		return nil, 0, err
	}
	if size > MaxDocumentSize {
		result.Close()
		return nil, 0, apperrors.DriveUploadTooBig(MaxDocumentSize)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		result.Close()
		return nil, 0, err
	}
	return result, size, nil
}

func (s *DriveService) ImportDocument(ctx context.Context, user int64, folder *uuid.UUID, name string, size int64, reader io.Reader) (*domain.DriveFile, error) {
	if user <= 0 {
		return nil, apperrors.Unauthorized("unauthorized")
	}
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if officeImportMIME(format) == "" {
		return nil, apperrors.New(apperrors.ErrCodeInvalidFileType, "supported imports: doc, docx, xls, xlsx, ppt, pptx, pdf", 415)
	}
	name, err := documentName(name, format)
	if err != nil {
		return nil, err
	}
	if size <= 0 {
		return nil, apperrors.BadRequest("empty document")
	}
	if size > MaxDocumentSize {
		return nil, apperrors.DriveUploadTooBig(MaxDocumentSize)
	}
	if folder != nil {
		if _, err = s.canWorkInFolder(ctx, user, *folder, domain.RoleEditor); err != nil {
			return nil, err
		}
	}
	if err = s.checkDriveUpload(ctx, user, size, "file"); err != nil {
		return nil, err
	}
	file, actual, err := bufferOfficeFile(reader)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if actual != size {
		return nil, apperrors.BadRequest("document size mismatch")
	}
	if err = validateOfficeContainer(file, actual, format); err != nil {
		return nil, err
	}
	if folder != nil {
		if _, err = s.canWorkInFolder(ctx, user, *folder, domain.RoleEditor); err != nil {
			return nil, err
		}
	}
	return s.saveDriveFile(ctx, user, folder, name, actual, officeImportMIME(format), file, nil, nil, nil, true)
}

func officeExportAllowed(source, target string) bool {
	if officeImportMIME(source) == "" {
		return false
	}
	if source == target {
		return true
	}
	if source == "pdf" {
		return false
	}
	if target == "pdf" {
		return true
	}
	return (source == "doc" && target == "docx") || (source == "xls" && target == "xlsx") || (source == "ppt" && target == "pptx")
}

type OfficeExport struct {
	Pending bool
	Reader  io.ReadCloser
	Size    int64
	Name    string
	MIME    string
	Version int
}

func conversionServerURL() (*url.URL, error) {
	raw := strings.TrimRight(strings.TrimSpace(os.Getenv("ONLYOFFICE_INTERNAL_URL")), "/")
	if raw == "" {
		raw = strings.TrimRight(strings.TrimSpace(os.Getenv("ONLYOFFICE_PUBLIC_URL")), "/")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, apperrors.New(apperrors.ErrCodeInternal, "configure ONLYOFFICE_INTERNAL_URL for document conversion", 503)
	}
	return u, nil
}

func conversionResultURL(raw string, server *url.URL) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid conversion result URL")
	}
	if !u.IsAbs() {
		u = server.ResolveReference(u)
	}
	if u.Scheme != server.Scheme || !strings.EqualFold(u.Host, server.Host) {
		return nil, errors.New("conversion result must use the configured DocumentServer origin")
	}
	return u, nil
}

// ExportDocument pins the saved version. It never saves converted bytes over the source.
func (s *DriveService) ExportDocument(ctx context.Context, user int64, id uuid.UUID, version int, target, secret, publicURL string) (*OfficeExport, error) {
	meta, err := s.canAccessFile(ctx, user, id, domain.RoleViewer)
	if err != nil {
		return nil, err
	}
	target = strings.ToLower(strings.TrimSpace(target))
	source := strings.ToLower(strings.TrimPrefix(filepath.Ext(meta.RealName), "."))
	if !officeExportAllowed(source, target) {
		return nil, apperrors.New(apperrors.ErrCodeInvalidFileType, "unsupported office conversion", 415)
	}
	if version < 0 {
		return nil, apperrors.BadRequest("invalid version")
	}
	if version == 0 {
		version = meta.Version
	}
	revision, err := s.repo.GetFileVersion(ctx, id, version)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, apperrors.NotFound("file version not found")
	}
	name := strings.TrimSuffix(meta.RealName, filepath.Ext(meta.RealName)) + "." + target
	if source == target {
		reader, size, _, err := s.DownloadFileVersion(ctx, user, id, version)
		if err != nil {
			return nil, err
		}
		return &OfficeExport{Reader: reader, Size: size, Name: name, MIME: officeImportMIME(target), Version: version}, nil
	}
	if revision.Size > MaxDocumentSize {
		return nil, apperrors.DriveUploadTooBig(MaxDocumentSize)
	}
	server, err := conversionServerURL()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	token, err := signOnlyOfficeClaims(OnlyOfficeTokenClaims{FileID: id.String(), UserID: user, Mode: "view", Purpose: ooTokenPurpose, Version: version, Exp: time.Now().Add(ooTokenTTL).Unix()}, secret)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(os.Getenv("ONLYOFFICE_DOWNLOAD_BASE_URL"), "/")
	if base == "" {
		base = strings.TrimRight(publicURL, "/")
	}
	key := fmt.Sprintf("export-%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s:%s", id, version, source, target))))
	payload := map[string]interface{}{"async": true, "filetype": source, "outputtype": target, "key": key, "title": name, "url": base + "/api/v1/drive/onlyoffice/download?" + url.Values{"file_id": {id.String()}, "token": {token}}.Encode()}
	if source == "xlsx" || source == "xls" {
		payload["spreadsheetLayout"] = map[string]interface{}{"ignorePrintArea": false}
		payload["region"] = "ru-RU"
	}
	if signingSecret := os.Getenv("ONLYOFFICE_JWT_SECRET"); signingSecret != "" {
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(payload)).SignedString([]byte(signingSecret))
		if err != nil {
			return nil, err
		}
		payload["token"] = signed
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := *server
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/converter"
	endpoint.RawQuery = url.Values{"shardkey": {key}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	// Never forward the signed source URL/JWT to a redirected converter.
	client := &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, conversionHTTPError(ctx)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, conversionHTTPError(ctx)
	}
	var result struct {
		EndConvert bool   `json:"endConvert"`
		FileURL    string `json:"fileUrl"`
		Error      int    `json:"error"`
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 || json.Unmarshal(raw, &result) != nil {
		return nil, conversionHTTPError(ctx)
	}
	if result.Error != 0 {
		return nil, apperrors.New(apperrors.ErrCodeBadRequest, "DocumentServer could not convert this document", 422).WithDetails(map[string]interface{}{"conversion_error": result.Error})
	}
	if !result.EndConvert {
		return &OfficeExport{Pending: true, Version: version}, nil
	}
	resultURL, err := conversionResultURL(result.FileURL, server)
	if err != nil || result.FileURL == "" {
		return nil, conversionHTTPError(ctx)
	}
	download, err := http.NewRequestWithContext(ctx, http.MethodGet, resultURL.String(), nil)
	if err != nil {
		return nil, conversionHTTPError(ctx)
	}
	responseFile, err := client.Do(download)
	if err != nil {
		return nil, conversionHTTPError(ctx)
	}
	defer responseFile.Body.Close()
	if responseFile.StatusCode != 200 {
		return nil, conversionHTTPError(ctx)
	}
	if responseFile.ContentLength > MaxDocumentSize {
		return nil, apperrors.DriveUploadTooBig(MaxDocumentSize)
	}
	file, size, err := bufferOfficeFile(responseFile.Body)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			file.Close()
		}
	}()
	if err = validateOfficeContainer(file, size, target); err != nil {
		return nil, conversionHTTPError(ctx)
	}
	// Access can be revoked or a document deleted while the converter is working.
	current, err := s.canAccessFile(ctx, user, id, domain.RoleViewer)
	if err != nil {
		return nil, err
	}
	if err = s.auditDriveDownload(ctx, user, current, version, false); err != nil {
		return nil, err
	}
	success = true
	return &OfficeExport{Reader: file, Size: size, Name: name, MIME: officeImportMIME(target), Version: version}, nil
}

func conversionHTTPError(ctx context.Context) error {
	if ctx.Err() != nil {
		return apperrors.New(apperrors.ErrCodeInternal, "conversion timed out or was cancelled; retry the same version", 504)
	}
	return apperrors.New(apperrors.ErrCodeInternal, "DocumentServer conversion unavailable or invalid response", 502)
}
