package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"mime/multipart"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestWorkspaceAIProbeRealAudio(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is installed in the production/test runtime")
	}
	const samples = 8800
	wav := make([]byte, 44+samples*2)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 8000)
	binary.LittleEndian.PutUint32(wav[28:], 16000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], samples*2)
	seconds, err := probeWorkspaceAIAudio(context.Background(), wav)
	require.NoError(t, err)
	require.EqualValues(t, 2, seconds)
	_, err = probeWorkspaceAIAudio(context.Background(), []byte("not an audio file"))
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidRequest)
}

func TestWorkspaceAIAudioAccountsSecondsWithoutTokens(t *testing.T) {
	store := &aiAccountingStub{reservation: domain.AIReservation{Created: true, AttemptID: uuid.New()}}
	s, _ := workspaceAIProviderTest(t, store, "")
	s.probeAudio = func(context.Context, []byte) (int64, error) { return 3, nil }
	s.client.Transport = workspaceAITransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "https://provider.example/v1/audio/transcriptions", r.URL.String())
		require.Equal(t, "Bearer server-secret", r.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"text":"Сәлем","duration":3.1}`))}, nil
	})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "openai/whisper-large-v3-turbo"))
	file, err := writer.CreateFormFile("file", "meeting.wav")
	require.NoError(t, err)
	_, err = file.Write([]byte("audio"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	result, err := s.Audio(context.Background(), 42, "audio:1", "nitec", "transcriptions", writer.FormDataContentType(), "", body.Bytes())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, store.completed, 1)
	usage := store.completed[0].Usage
	require.Equal(t, "audio_seconds", usage.MeasuredUnit)
	require.EqualValues(t, 4, *usage.MeasuredQuantity)
	require.Nil(t, usage.InputTokens)
	require.Nil(t, usage.OutputTokens)
}

func TestWorkspaceAITTSCredentialsAndCharacterCount(t *testing.T) {
	store := &aiAccountingStub{reservation: domain.AIReservation{Created: true, AttemptID: uuid.New()}}
	s, _ := workspaceAIProviderTest(t, store, "")
	s.SetSpeechCredentials("openai-secret", "eleven-secret", strings.Repeat("k", 32))
	s.client.Transport = workspaceAITransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "https://api.elevenlabs.io/v1/text-to-speech/voice123?output_format=mp3_44100_128", r.URL.String())
		require.Equal(t, "eleven-secret", r.Header.Get("xi-api-key"))
		require.Empty(t, r.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"audio/mpeg"}}, Body: io.NopCloser(strings.NewReader("audio"))}, nil
	})
	_, err := s.Audio(context.Background(), 42, "tts:1", "elevenlabs", "text-to-speech/voice123", "application/json", "mp3_44100_128", []byte(`{"model_id":"eleven_multilingual_v2","text":"Сәлем"}`))
	require.NoError(t, err)
	require.Len(t, store.completed, 1)
	require.EqualValues(t, 5, *store.completed[0].Usage.MeasuredQuantity)
	require.Equal(t, "text_characters", store.completed[0].Usage.MeasuredUnit)
}

func TestWorkspaceAIJobGrantCannotChangeJobOrCompany(t *testing.T) {
	store := &aiAccountingStub{}
	s, calls := workspaceAIProviderTest(t, store, workspaceAIOutput)
	s.SetSpeechCredentials("", "", strings.Repeat("k", 32))
	jobID := uuid.NewString()
	token, err := s.IssueJobGrant(context.Background(), 42, jobID)
	require.NoError(t, err)
	ctx, user, err := s.AuthenticateJobGrant(context.Background(), token, jobID)
	require.NoError(t, err)
	require.EqualValues(t, 42, user)
	_, _, err = s.AuthenticateJobGrant(context.Background(), token, uuid.NewString())
	require.Error(t, err)
	_, _, err = s.AuthenticateJobGrant(context.Background(), token+"tampered", jobID)
	require.Error(t, err)
	other := uuid.New()
	s.users = alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &other, IsActive: true}}
	_, err = s.Complete(ctx, 42, jobID+":1", []byte(workspaceAIInput), nil)
	require.ErrorIs(t, err, domain.ErrAIPaidCallContextMismatch)
	require.Zero(t, *calls)
}

func TestWorkspaceAIPersonalCallerAndJobGrant(t *testing.T) {
	store := &aiAccountingStub{reservation: domain.AIReservation{Created: true, AttemptID: uuid.New()}}
	s, calls := workspaceAIProviderTest(t, store, workspaceAIOutput)
	s.users = alemPayerStub{user: &domain.User{ID: 42, IsActive: true}}
	s.SetSpeechCredentials("", "", strings.Repeat("k", 32))
	jobID := uuid.NewString()
	grant, err := s.IssueJobGrant(context.Background(), 42, jobID)
	require.NoError(t, err)
	ctx, _, err := s.AuthenticateJobGrant(context.Background(), grant, jobID)
	require.NoError(t, err)
	_, err = s.Complete(ctx, 42, jobID+":1", []byte(workspaceAIInput), nil)
	require.NoError(t, err)
	require.Equal(t, 1, *calls)
	require.Equal(t, uuid.Nil, store.completed[0].OrganizationID)
}
