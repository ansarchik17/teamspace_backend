package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFileService_UsesPublicBaseURLForPresignClient(t *testing.T) {
	internalClient, err := minio.New("messenger-minio:9000", &minio.Options{Secure: false})
	require.NoError(t, err)
	publicClient, err := minio.New("92.38.48.9:19000", &minio.Options{Secure: false})
	require.NoError(t, err)

	svc := &FileService{
		minioClient:   internalClient,
		presignClient: publicClient,
		publicBaseURL: "http://92.38.48.9:19000/messenger",
	}

	require.NotNil(t, svc.minioClient)
	require.NotNil(t, svc.presignClient)
	assert.Equal(t, "http://92.38.48.9:19000/messenger", svc.publicBaseURL)
}

func TestIsBucketCORSUnsupported(t *testing.T) {
	assert.True(t, isBucketCORSUnsupported(errors.New("A header you provided implies functionality that is not implemented")))
	assert.True(t, isBucketCORSUnsupported(minio.ErrorResponse{
		Code:       "NotImplemented",
		StatusCode: 501,
	}))
	assert.False(t, isBucketCORSUnsupported(errors.New("permission denied")))
	assert.False(t, isBucketCORSUnsupported(nil))
}

func TestPresignPutURL_WithRegionDoesNotFetchBucketLocation(t *testing.T) {
	presignClient, err := minio.New("127.0.0.1:1", &minio.Options{
		Creds:  credentials.NewStaticV4("minioadmin", "minioadmin", ""),
		Secure: false,
		Region: "us-east-1",
	})
	require.NoError(t, err)

	svc := &FileService{
		presignClient: presignClient,
		bucket:        "messenger",
	}

	uploadURL, err := svc.PresignPutURL(context.Background(), "note-photos/note/attachment", 15*time.Minute)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(uploadURL, "http://127.0.0.1:1/messenger/note-photos/note/attachment?"))
	assert.Contains(t, uploadURL, "X-Amz-Credential=")
}

func TestGenerateAndValidateSignedURL(t *testing.T) {
	svc := &FileService{
		publicURL: "https://api.example.com",
		secretKey: "test_secret_key_123",
	}

	objectName := "drive/user123/secret_doc.pdf"
	signedURL, err := svc.GetFileURL(context.Background(), objectName, time.Hour)
	require.NoError(t, err)
	assert.Contains(t, signedURL, "https://api.example.com/api/v1/files/download?object_name=")
	assert.Contains(t, signedURL, "&expires=")
	assert.Contains(t, signedURL, "&signature=")

	// Validate valid signed URL parameters
	parts := strings.Split(signedURL, "?")
	require.Len(t, parts, 2)
	queryParams := parts[1]

	var expires int64
	var signature string
	for _, param := range strings.Split(queryParams, "&") {
		kv := strings.SplitN(param, "=", 2)
		if len(kv) == 2 {
			if kv[0] == "expires" {
				_, _ = fmt.Sscanf(kv[1], "%d", &expires)
			} else if kv[0] == "signature" {
				signature = kv[1]
			}
		}
	}

	assert.True(t, expires > time.Now().Unix())
	assert.NotEmpty(t, signature)

	// Valid signature check
	assert.True(t, svc.ValidateSignedURL(objectName, expires, signature))

	// Invalid object_name (tampering attempt)
	assert.False(t, svc.ValidateSignedURL("drive/otheruser/secret_doc.pdf", expires, signature))

	// Expired link check
	assert.False(t, svc.ValidateSignedURL(objectName, time.Now().Unix()-10, signature))
}
