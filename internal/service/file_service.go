package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	miniocors "github.com/minio/minio-go/v7/pkg/cors"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

const (
	MaxFileSize     = 500 * 1024 * 1024  // 500MB
	MaxImageSize    = 50 * 1024 * 1024   // 50MB
	MaxVideoSize    = 1024 * 1024 * 1024 // 1GB
	MaxDocumentSize = 100 * 1024 * 1024  // 100MB
	MaxAudioSize    = 100 * 1024 * 1024  // 100MB
	MaxVoiceSize    = 25 * 1024 * 1024   // 25MB — voice messages are short clips
)

var (
	AllowedImageTypes    = []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
	AllowedVideoTypes    = []string{".mp4", ".mov", ".avi", ".mkv", ".webm"}
	AllowedDocumentTypes = []string{".pdf", ".doc", ".docx", ".xls", ".xlsx", ".txt", ".jpg", ".jpeg", ".png", ".webp"}
	AllowedAudioTypes    = []string{".mp3", ".ogg", ".m4a", ".wav", ".flac", ".aac", ".webm"}
	AllowedVoiceTypes    = []string{".ogg", ".webm", ".m4a", ".mp3", ".wav"}
)

type FileService struct {
	minioClient   *minio.Client
	presignClient *minio.Client
	driveRepo     *repository.DriveRepository
	bucket        string
	publicURL     string
	publicBaseURL string
	secretKey     string
	logger        *zap.Logger
}

func NewFileService(cfg config.MinIOConfig, publicURL string, driveRepo *repository.DriveRepository, logger *zap.Logger) (*FileService, error) {
	// Initialize MinIO client
	minioClient, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create minio client: %w", err)
	}

	publicBaseURL := strings.TrimRight(cfg.PublicBaseURL, "/")
	publicBaseParsed, err := url.Parse(publicBaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse minio public base url: %w", err)
	}
	presignClient, err := minio.New(publicBaseParsed.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: strings.EqualFold(publicBaseParsed.Scheme, "https"),
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create public presign minio client: %w", err)
	}

	service := &FileService{
		minioClient:   minioClient,
		presignClient: presignClient,
		driveRepo:     driveRepo,
		bucket:        cfg.Bucket,
		publicURL:     publicURL,
		publicBaseURL: publicBaseURL,
		secretKey:     cfg.SecretKey,
		logger:        logger.With(zap.String("service", "file")),
	}

	// Ensure bucket exists
	ctx := context.Background()
	exists, err := minioClient.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket: %w", err)
	}

	if !exists {
		err = minioClient.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
		logger.Info("Created MinIO bucket", zap.String("bucket", cfg.Bucket))
	}

	// Calculate and set bucket policy for ai-uploads to be public
	// We need this so that AI models (and users via direct link) can download images without authentication headers
	policy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Sid": "PublicReadAiUploads",
				"Effect": "Allow",
				"Principal": "*",
				"Action": ["s3:GetObject"],
				"Resource": ["arn:aws:s3:::%s/ai-uploads/*"]
			}
		]
	}`, cfg.Bucket)

	if err := minioClient.SetBucketPolicy(ctx, cfg.Bucket, policy); err != nil {
		// Log warning but don't fail, maybe permissions issue or already set differently (though SetBucketPolicy usually overwrites)
		// Actually, if we can't set policy, AI features might break for image inputs.
		// Let's log error and proceed.
		logger.Error("Failed to set bucket policy for ai-uploads", zap.Error(err))
	} else {
		// Log success only if changed? No easy way to check if changed without GetBucketPolicy.
		// Just debug log.
		logger.Debug("Set public bucket policy for ai-uploads")
	}

	corsConfig := miniocors.NewConfig([]miniocors.Rule{
		{
			AllowedMethod: []string{"GET", "HEAD", "PUT"},
			AllowedOrigin: []string{"*"},
			AllowedHeader: []string{"*"},
			ExposeHeader:  []string{"ETag"},
			MaxAgeSeconds: 3600,
		},
	})
	if err := minioClient.SetBucketCors(ctx, cfg.Bucket, corsConfig); err != nil {
		if isBucketCORSUnsupported(err) {
			logger.Debug("Skipping bucket CORS configuration because object storage does not support it",
				zap.String("bucket", cfg.Bucket),
				zap.Error(err),
			)
		} else {
			logger.Warn("Failed to set bucket CORS",
				zap.String("bucket", cfg.Bucket),
				zap.Error(err),
			)
		}
	} else {
		logger.Debug("Set bucket CORS configuration")
	}

	return service, nil
}

func isBucketCORSUnsupported(err error) bool {
	if err == nil {
		return false
	}

	resp := minio.ToErrorResponse(err)
	if resp.Code == "NotImplemented" || resp.StatusCode == http.StatusNotImplemented {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not implemented") ||
		strings.Contains(msg, "a header you provided implies functionality that is not implemented")
}

// UploadFile uploads a file to MinIO
func (s *FileService) UploadFile(ctx context.Context, userID int64, filename string, fileType string, size int64, reader io.Reader) (string, error) {
	// Validate file size
	if err := s.validateFileSize(fileType, size); err != nil {
		return "", err
	}

	// Validate file type
	ext := filepath.Ext(filename)
	if err := s.validateFileType(fileType, ext); err != nil {
		return "", err
	}

	// Generate unique file name
	objectName := s.generateObjectName(userID, fileType, ext)

	// Upload to MinIO
	contentType := s.getContentType(fileType, ext)
	_, err := s.minioClient.PutObject(ctx, s.bucket, objectName, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})

	if err != nil {
		s.logger.Error("Failed to upload file", zap.Error(err))
		return "", apperrors.FileUploadFailed()
	}

	s.logger.Info("File uploaded",
		zap.Int64("user_id", userID),
		zap.String("object", objectName),
		zap.Int64("size", size),
	)

	return objectName, nil
}

// GetFileURL generates a signed URL for file download via API proxy
func (s *FileService) GetFileURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	return s.GenerateSignedURL(objectName, expiry), nil
}

// GenerateSignedURL creates an HMAC-signed download URL for a MinIO object
func (s *FileService) GenerateSignedURL(objectName string, expiry time.Duration) string {
	if expiry <= 0 {
		expiry = 24 * time.Hour
	}
	exp := time.Now().Add(expiry).Unix()
	sig := s.generateHMAC(objectName, exp)
	return fmt.Sprintf("%s/api/v1/files/download?object_name=%s&expires=%d&signature=%s",
		strings.TrimRight(s.publicURL, "/"), url.QueryEscape(objectName), exp, sig)
}

// ValidateSignedURL checks if the download URL HMAC signature and expiration are valid
func (s *FileService) ValidateSignedURL(objectName string, expires int64, signature string) bool {
	if signature == "" || expires <= 0 {
		return false
	}
	if time.Now().Unix() > expires {
		return false
	}
	expectedSig := s.generateHMAC(objectName, expires)
	return hmac.Equal([]byte(signature), []byte(expectedSig))
}

// ValidateUserAccessToObject checks authorization for logged-in user when no signed token is present
func (s *FileService) ValidateUserAccessToObject(ctx context.Context, userID int64, objectName string) error {
	if userID <= 0 {
		return apperrors.Unauthorized("unauthorized")
	}

	clean := strings.TrimPrefix(objectName, "/")
	if isPublicObjectName(clean) {
		return nil
	}

	if s.driveRepo != nil {
		file, err := s.driveRepo.GetFileByObjectName(ctx, objectName)
		if err == nil && file != nil {
			if file.OwnerID == userID || file.IsPublic {
				return nil
			}
			role, err := s.driveRepo.GetPermissionRole(ctx, domain.ResourceFile, file.ID, userID)
			if err == nil && role != nil {
				return nil
			}
			return apperrors.Forbidden("no access to file")
		}
	}

	return nil
}

func isPublicObjectName(objectName string) bool {
	clean := strings.TrimPrefix(objectName, "/")
	return strings.HasPrefix(clean, "ai-uploads/") ||
		strings.HasPrefix(clean, "image/0/") ||
		strings.HasPrefix(clean, "avatars/") ||
		strings.HasPrefix(clean, "public/")
}

func (s *FileService) generateHMAC(objectName string, expires int64) string {
	secret := s.secretKey
	if secret == "" {
		secret = "alem_file_download_secret_key"
	}
	h := hmac.New(sha256.New, []byte(secret+":file_download"))
	h.Write([]byte(fmt.Sprintf("%s:%d", objectName, expires)))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// DownloadFile retrieves a file from MinIO
func (s *FileService) DownloadFile(ctx context.Context, objectName string) (io.ReadCloser, string, int64, error) {
	obj, err := s.minioClient.GetObject(ctx, s.bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		s.logger.Error("Failed to get file", zap.Error(err))
		return nil, "", 0, apperrors.Internal("failed to get file")
	}

	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, "", 0, apperrors.NotFound("file not found")
	}

	return obj, stat.ContentType, stat.Size, nil
}

// OpenObject retrieves an object from MinIO for reading and seeking (supports HTTP range requests).
func (s *FileService) OpenObject(ctx context.Context, objectName string) (*minio.Object, string, int64, time.Time, error) {
	obj, err := s.minioClient.GetObject(ctx, s.bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		s.logger.Error("Failed to get file", zap.Error(err))
		return nil, "", 0, time.Time{}, apperrors.Internal("failed to get file")
	}

	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, "", 0, time.Time{}, apperrors.NotFound("file not found")
	}

	contentType := stat.ContentType
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = s.GetContentTypeByFilename(objectName)
	}

	return obj, contentType, stat.Size, stat.LastModified, nil
}

// DeleteFile deletes a file from MinIO
func (s *FileService) DeleteFile(ctx context.Context, objectName string) error {
	err := s.minioClient.RemoveObject(ctx, s.bucket, objectName, minio.RemoveObjectOptions{})
	if err != nil {
		s.logger.Error("Failed to delete file", zap.Error(err))
		return apperrors.Internal("failed to delete file")
	}

	s.logger.Info("File deleted", zap.String("object", objectName))
	return nil
}

// PresignPutURL generates a presigned PUT URL so the client can upload directly to object storage.
func (s *FileService) PresignPutURL(ctx context.Context, objectName string, expiry time.Duration) (string, error) {
	client := s.presignClient
	if client == nil {
		client = s.minioClient
	}
	u, err := client.PresignedPutObject(ctx, s.bucket, objectName, expiry)
	if err != nil {
		s.logger.Error("Failed to create presigned put URL", zap.Error(err))
		return "", apperrors.AttachmentPresignFailed().WithError(err)
	}
	return u.String(), nil
}

func (s *FileService) ObjectExists(ctx context.Context, objectName string) error {
	_, err := s.minioClient.StatObject(ctx, s.bucket, objectName, minio.StatObjectOptions{})
	if err == nil {
		return nil
	}
	resp := minio.ToErrorResponse(err)
	if resp.Code == "NoSuchKey" || resp.Code == "NoSuchObject" || resp.StatusCode == 404 {
		return apperrors.AttachmentNotUploaded().WithError(err)
	}
	return apperrors.UploadFailed().WithError(err)
}

// CopyObject creates an immutable destination for a completed presigned upload.
func (s *FileService) CopyObject(ctx context.Context, sourceKey, destinationKey string) error {
	_, err := s.minioClient.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: s.bucket, Object: destinationKey, ReplaceMetadata: true, ContentType: s.GetContentTypeByFilename(destinationKey)},
		minio.CopySrcOptions{Bucket: s.bucket, Object: sourceKey},
	)
	if err != nil {
		return apperrors.UploadFailed().WithError(err)
	}
	return nil
}

func (s *FileService) BucketName() string {
	return s.bucket
}

func (s *FileService) Endpoint() string {
	return s.publicBaseURL
}

func (s *FileService) CheckBucketAccess(ctx context.Context) error {
	exists, err := s.minioClient.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("bucket %s does not exist", s.bucket)
	}
	return nil
}

// UploadKanbanFile uploads a file to the kanban bucket (separate from main bucket)
func (s *FileService) UploadKanbanFile(ctx context.Context, userID int64, filename string, objectName string, size int64, reader io.Reader) (string, error) {
	kanbanBucket := "kanban"

	// Ensure kanban bucket exists
	exists, err := s.minioClient.BucketExists(ctx, kanbanBucket)
	if err != nil {
		s.logger.Error("Failed to check kanban bucket", zap.Error(err))
		return "", apperrors.Internal("failed to check storage bucket")
	}

	if !exists {
		err = s.minioClient.MakeBucket(ctx, kanbanBucket, minio.MakeBucketOptions{})
		if err != nil {
			s.logger.Error("Failed to create kanban bucket", zap.Error(err))
			return "", apperrors.Internal("failed to create storage bucket")
		}
		s.logger.Info("Created kanban bucket", zap.String("bucket", kanbanBucket))
	}

	// Detect content type
	contentType := s.GetContentTypeByFilename(filename)

	// Upload to kanban bucket
	_, err = s.minioClient.PutObject(ctx, kanbanBucket, objectName, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})

	if err != nil {
		s.logger.Error("Failed to upload file to kanban bucket", zap.Error(err))
		return "", apperrors.FileUploadFailed()
	}

	s.logger.Info("File uploaded to kanban bucket",
		zap.Int64("user_id", userID),
		zap.String("object", objectName),
		zap.Int64("size", size),
	)

	return objectName, nil
}

// UploadKanbanFileStandalone загружает файл в бакет kanban и сохраняет запись в таблице files
func (s *FileService) UploadKanbanFileStandalone(ctx context.Context, userID int64, filename string, size int64, reader io.Reader) (*domain.DriveFile, error) {
	kanbanBucket := "kanban"

	// Ensure kanban bucket exists
	exists, err := s.minioClient.BucketExists(ctx, kanbanBucket)
	if err != nil {
		s.logger.Error("Failed to check kanban bucket", zap.Error(err))
		return nil, apperrors.Internal("failed to check storage bucket")
	}

	if !exists {
		err = s.minioClient.MakeBucket(ctx, kanbanBucket, minio.MakeBucketOptions{})
		if err != nil {
			s.logger.Error("Failed to create kanban bucket", zap.Error(err))
			return nil, apperrors.Internal("failed to create storage bucket")
		}
		s.logger.Info("Created kanban bucket", zap.String("bucket", kanbanBucket))
	}

	objectName := fmt.Sprintf("uploads/%d/%d_%s", userID, time.Now().Unix(), filename)
	contentType := s.GetContentTypeByFilename(filename)

	// Upload to kanban bucket
	_, err = s.minioClient.PutObject(ctx, kanbanBucket, objectName, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})

	if err != nil {
		s.logger.Error("Failed to upload file to kanban bucket", zap.Error(err))
		return nil, apperrors.FileUploadFailed()
	}

	driveFile := &domain.DriveFile{
		ID:         uuid.New(),
		OwnerID:    userID,
		RealName:   filename,
		ObjectName: objectName,
		Size:       size,
		MimeType:   contentType,
		Version:    1,
		Scope:      "kanban",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if s.driveRepo != nil {
		if err := s.driveRepo.CreateFileWithInitialVersion(ctx, driveFile, userID); err != nil {
			s.logger.Error("Failed to create file record for kanban file", zap.Error(err))
			return nil, apperrors.Internal("failed to save file metadata")
		}
	}

	s.logger.Info("File uploaded to kanban bucket (standalone)",
		zap.Int64("user_id", userID),
		zap.String("object", objectName),
		zap.String("id", driveFile.ID.String()),
	)

	return driveFile, nil
}

// DownloadKanbanFile downloads a file from the kanban bucket
func (s *FileService) DownloadKanbanFile(ctx context.Context, objectName string) (io.ReadCloser, string, int64, error) {
	kanbanBucket := "kanban"

	obj, err := s.minioClient.GetObject(ctx, kanbanBucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		s.logger.Error("Failed to get file from kanban bucket", zap.Error(err))
		return nil, "", 0, apperrors.Internal("failed to get file")
	}

	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, "", 0, apperrors.NotFound("file not found")
	}

	return obj, stat.ContentType, stat.Size, nil
}

// DeleteKanbanFile deletes a file from the kanban bucket
func (s *FileService) DeleteKanbanFile(ctx context.Context, objectName string) error {
	kanbanBucket := "kanban"

	err := s.minioClient.RemoveObject(ctx, kanbanBucket, objectName, minio.RemoveObjectOptions{})
	if err != nil {
		s.logger.Error("Failed to delete file from kanban bucket", zap.Error(err))
		return apperrors.Internal("failed to delete file")
	}

	s.logger.Info("File deleted from kanban bucket", zap.String("object", objectName))
	return nil
}

// GetContentTypeByFilename determines content type based on file extension
func (s *FileService) GetContentTypeByFilename(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	return s.getContentType("", ext)
}

// validateFileSize validates file size based on type
func (s *FileService) validateFileSize(fileType string, size int64) error {
	var maxSize int64

	switch fileType {
	case "image":
		maxSize = MaxImageSize
	case "video":
		maxSize = MaxVideoSize
	case "document":
		maxSize = MaxDocumentSize
	case "audio":
		maxSize = MaxAudioSize
	case "voice":
		maxSize = MaxVoiceSize
	default:
		maxSize = MaxFileSize
	}

	if size > maxSize {
		return apperrors.FileTooBig(maxSize)
	}

	return nil
}

// validateFileType validates file extension
func (s *FileService) validateFileType(fileType string, ext string) error {
	var allowed []string

	switch fileType {
	case "image":
		allowed = AllowedImageTypes
	case "video":
		allowed = AllowedVideoTypes
	case "document":
		allowed = AllowedDocumentTypes
	case "audio":
		allowed = AllowedAudioTypes
	case "voice":
		allowed = AllowedVoiceTypes
	default:
		return nil // Allow all for generic files
	}

	// Normalize extension to lowercase for comparison
	ext = strings.ToLower(ext)

	for _, allowedExt := range allowed {
		if ext == allowedExt {
			return nil
		}
	}

	return apperrors.InvalidFileType(fmt.Sprintf("%v", allowed))
}

// generateObjectName generates a unique object name
func (s *FileService) generateObjectName(userID int64, fileType string, ext string) string {
	timestamp := time.Now().Unix()
	uuid := uuid.New().String()
	return fmt.Sprintf("%s/%d/%d_%s%s", fileType, userID, timestamp, uuid, ext)
}

// getContentType returns content type for file
func (s *FileService) getContentType(fileType string, ext string) string {
	// .webm can be audio or video depending on upload context
	if ext == ".webm" {
		switch fileType {
		case "voice", "audio":
			return "audio/webm"
		default:
			return "video/webm"
		}
	}

	contentTypes := map[string]string{
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",
		".mp4":  "video/mp4",
		".mov":  "video/quicktime",
		".avi":  "video/x-msvideo",
		".mkv":  "video/x-matroska",
		".m4a":  "audio/mp4",
		".ogg":  "audio/ogg",
		".mp3":  "audio/mpeg",
		".wav":  "audio/wav",
		".aac":  "audio/aac",
		".flac": "audio/flac",
		".pdf":  "application/pdf",
		".doc":  "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls":  "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".ppt":  "application/vnd.ms-powerpoint",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		".txt":  "text/plain",
	}

	if ct, ok := contentTypes[ext]; ok {
		return ct
	}

	return "application/octet-stream"
}
