package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type WorkspaceAIAudioResponse struct {
	Body        []byte
	ContentType string
}

func (s *WorkspaceAIProvider) Audio(ctx context.Context, userID int64, requestID, provider, operation, contentType, outputFormat string, raw []byte) (*WorkspaceAIAudioResponse, error) {
	ctx, err := aiPaidUserContext(s.paid, s.users, ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > 32*1024*1024 || requestID == "" || len(requestID) > 100 {
		return nil, domain.ErrAIPaidCallInvalidRequest
	}
	base, token, header := "", "", "Authorization"
	switch provider {
	case "nitec":
		base, token = strings.TrimSuffix(s.url, "/chat/completions"), s.token
	case "openai-direct":
		base, token = "https://api.openai.com/v1", s.openAIKey
	case "elevenlabs":
		base, token, header = "https://api.elevenlabs.io/v1", s.elevenLabsKey, "xi-api-key"
	default:
		return nil, domain.ErrAIPaidCallInvalidRequest
	}
	if token == "" {
		return nil, domain.ErrAIAccountingDisabled
	}
	operation = strings.TrimPrefix(operation, "/")
	var model, kind string
	var quantity int64
	if (provider != "elevenlabs" && operation == "transcriptions") || (provider == "elevenlabs" && operation == "speech-to-text") {
		kind = "stt"
		var audio []byte
		model, audio, err = workspaceAIUpload(contentType, raw, provider)
		if err == nil {
			_, err = domain.AICreditRatesForModel(domain.AICreditModelKey{ProviderID: provider, EndpointKind: kind, ModelID: model, PolicyVersion: domain.AIAudioCreditPolicyV1})
		}
		if err == nil {
			quantity, err = s.probeAudio(ctx, audio)
		}
	} else if (provider != "elevenlabs" && operation == "speech") || (provider == "elevenlabs" && regexp.MustCompile(`^text-to-speech/[A-Za-z0-9_-]{1,100}$`).MatchString(operation)) {
		kind = "tts"
		model, quantity, err = workspaceAITTS(raw, provider)
	} else {
		return nil, domain.ErrAIPaidCallInvalidRequest
	}
	if err != nil {
		return nil, err
	}
	key := domain.AICreditModelKey{ProviderID: provider, EndpointKind: kind, ModelID: model, PolicyVersion: domain.AIAudioCreditPolicyV1}
	rates, err := domain.AICreditRatesForModel(key)
	if err != nil {
		return nil, err
	}
	usage := domain.AICreditUsage{Status: "derived", MeasuredUnit: rates.Unit, MeasuredQuantity: &quantity}
	estimate, err := domain.CalculateAICredits(key, usage)
	if err != nil {
		return nil, err
	}
	endpoint := base + "/audio/" + operation
	if provider == "elevenlabs" {
		endpoint = base + "/" + operation
	}
	if len(outputFormat) > 100 {
		return nil, domain.ErrAIPaidCallInvalidRequest
	}
	if outputFormat != "" && kind == "tts" && provider == "elevenlabs" {
		endpoint += "?" + url.Values{"output_format": {outputFormat}}.Encode()
	}
	fingerprint := sha256.Sum256(append([]byte(endpoint+"\n"+contentType+"\n"), raw...))
	source := "workspace_ai_audio"
	adapter := "workspace-" + kind + "-v1"
	params := domain.AIPaidCallParams{IdempotencyKey: "workspace_ai:" + strconv.FormatInt(userID, 10) + ":" + requestID,
		OperationKind: kind, Model: key, UsageAdapterVersion: adapter, Microcredits: estimate.Microcredits,
		Fingerprint: fingerprint, AttemptFingerprint: fingerprint, AttemptNumber: 1, SourceKind: &source}
	result, reservation, _, err := ExecutePaidCallTyped(ctx, s.paid, params, func(ctx context.Context, _ domain.AIReservation) (*WorkspaceAIAudioResponse, domain.AISettleRequest, error) {
		evidence := domain.AISettleRequest{ActualModel: model, UsageAdapterVersion: adapter, UsageSource: "gateway_audio_probe"}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
		if err != nil {
			return nil, evidence, err
		}
		auth := token
		if header == "Authorization" {
			auth = "Bearer " + token
		}
		req.Header.Set(header, auth)
		req.Header.Set("Content-Type", contentType)
		client := *s.client
		if client.Timeout < 300*time.Second {
			client.Timeout = 300 * time.Second
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, evidence, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, evidence, &alem.APIError{StatusCode: resp.StatusCode}
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 24*1024*1024+1))
		if err != nil || len(body) == 0 || len(body) > 24*1024*1024 {
			return nil, evidence, domain.ErrAICreditUnknownUsage
		}
		responseType := resp.Header.Get("Content-Type")
		if kind == "stt" {
			var data struct {
				Text     *string  `json:"text"`
				Duration *float64 `json:"duration"`
				Model    string   `json:"model"`
			}
			if json.Unmarshal(body, &data) != nil || data.Text == nil {
				return nil, evidence, domain.ErrAICreditUnknownUsage
			}
			if data.Model != "" && data.Model != model {
				return nil, evidence, domain.ErrAICreditUnknownModel
			}
			if data.Duration != nil && *data.Duration > float64(quantity) && *data.Duration <= 7200 {
				quantity = int64(math.Ceil(*data.Duration))
			}
			responseType = "application/json"
		} else {
			if !strings.HasPrefix(responseType, "audio/") && !strings.HasPrefix(responseType, "application/octet-stream") {
				return nil, evidence, domain.ErrAICreditUnknownUsage
			}
			evidence.UsageSource = "gateway_submitted_characters"
		}
		evidence.Usage = domain.AICreditUsage{Status: "derived", MeasuredUnit: rates.Unit, MeasuredQuantity: &quantity}
		if id := resp.Header.Get("x-request-id"); id != "" && len(id) <= 300 {
			evidence.ProviderRequestID = &id
		}
		return &WorkspaceAIAudioResponse{Body: body, ContentType: responseType}, evidence, nil
	})
	if err == nil && !reservation.Created {
		return nil, domain.ErrAIReservationConflict
	}
	return result, err
}

func workspaceAIUpload(contentType string, raw []byte, provider string) (string, []byte, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return "", nil, domain.ErrAIPaidCallInvalidRequest
	}
	reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	fields := map[string]string{}
	var audio []byte
	allowed := map[string]bool{"model": true, "model_id": true, "language": true, "language_code": true, "prompt": true, "response_format": true, "temperature": true, "timestamp_granularities[]": true, "timestamps_granularity": true, "diarize": true, "tag_audio_events": true}
	for count := 0; ; count++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil || count > 20 {
			return "", nil, domain.ErrAIPaidCallInvalidRequest
		}
		if part.FormName() == "file" && part.FileName() != "" && audio == nil {
			audio, err = io.ReadAll(io.LimitReader(part, 32*1024*1024+1))
			if err != nil || len(audio) > 32*1024*1024 {
				return "", nil, domain.ErrAIPaidCallInvalidRequest
			}
		} else if allowed[part.FormName()] && part.FileName() == "" {
			data, err := io.ReadAll(io.LimitReader(part, 8193))
			if err != nil || len(data) > 8192 {
				return "", nil, domain.ErrAIPaidCallInvalidRequest
			}
			if _, duplicate := fields[part.FormName()]; duplicate {
				return "", nil, domain.ErrAIPaidCallInvalidRequest
			}
			fields[part.FormName()] = string(data)
		} else {
			return "", nil, domain.ErrAIPaidCallInvalidRequest
		}
	}
	model := fields["model"]
	if provider == "elevenlabs" {
		model = fields["model_id"]
	}
	if model == "" || len(audio) == 0 || (fields["response_format"] != "" && fields["response_format"] != "json" && fields["response_format"] != "verbose_json") {
		return "", nil, domain.ErrAIPaidCallInvalidRequest
	}
	return model, audio, nil
}

func workspaceAITTS(raw []byte, provider string) (string, int64, error) {
	var fields map[string]json.RawMessage
	var body struct {
		Model   string `json:"model"`
		ModelID string `json:"model_id"`
		Input   string `json:"input"`
		Text    string `json:"text"`
	}
	if json.Unmarshal(raw, &body) != nil || json.Unmarshal(raw, &fields) != nil {
		return "", 0, domain.ErrAIPaidCallInvalidRequest
	}
	allowed := map[string]bool{"model": true, "input": true, "voice": true, "response_format": true, "speed": true, "instructions": true, "model_id": true, "text": true, "language_code": true, "voice_settings": true, "seed": true, "apply_text_normalization": true}
	for key := range fields {
		if !allowed[key] {
			return "", 0, domain.ErrAIPaidCallInvalidRequest
		}
	}
	model, text := body.Model, body.Input
	if provider == "elevenlabs" {
		model, text = body.ModelID, body.Text
	}
	count := int64(utf8.RuneCountInString(text))
	if model == "" || strings.TrimSpace(text) == "" || !utf8.ValidString(text) || count > 5000 {
		return "", 0, domain.ErrAIPaidCallInvalidRequest
	}
	return model, count, nil
}

func probeWorkspaceAIAudio(ctx context.Context, audio []byte) (int64, error) {
	file, err := os.CreateTemp("", "workspace-ai-audio-*.bin")
	if err != nil {
		return 0, err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(audio)
	closeErr := file.Close()
	if err != nil {
		return 0, err
	}
	if closeErr != nil {
		return 0, closeErr
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body, err := exec.CommandContext(probeCtx, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-show_entries", "format=duration,format_name", "-of", "json", file.Name()).Output()
	if err != nil {
		return 0, domain.ErrAIPaidCallInvalidRequest
	}
	var result struct {
		Format struct {
			Duration string `json:"duration"`
			Name     string `json:"format_name"`
		} `json:"format"`
	}
	if json.Unmarshal(body, &result) != nil {
		return 0, domain.ErrAIPaidCallInvalidRequest
	}
	allowedFormats := map[string]bool{"wav": true, "mp3": true, "ogg": true, "flac": true, "aac": true, "matroska": true, "webm": true, "mov": true, "mp4": true, "m4a": true, "amr": true, "asf": true, "au": true, "ape": true}
	if !allowedFormats[strings.Split(result.Format.Name, ",")[0]] {
		return 0, domain.ErrAIPaidCallInvalidRequest
	}
	duration, err := strconv.ParseFloat(result.Format.Duration, 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > 7200 {
		return 0, domain.ErrAIPaidCallInvalidRequest
	}
	return int64(math.Ceil(duration)), nil
}
