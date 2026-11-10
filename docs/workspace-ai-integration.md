# Workspace AI gateway

The Python `workspace-ai` tools use the Go API's authenticated provider gateway.
Every physical model request resolves the current Workspace user/company, reserves
their shared budget, performs exactly one provider attempt and settles actual
usage. Python cannot submit a payer, price, token count or provider URL.

## Endpoints and units

- `POST /api/v1/ai/provider/chat/completions`: raw OpenAI-compatible JSON;
  existing `credits-v1` token weights, provider-reported usage. By default the
  gateway uses the existing verified non-streaming usage contract, then emits
  the complete response in SDK-compatible SSE after settlement. This preserves
  tool calls and the frontend protocol while avoiding an assumption that NITEC
  emits terminal streaming usage. Live token streaming can be enabled only after
  verifying that contract (`WORKSPACE_AI_STREAMING_USAGE_CONFIRMED=true`);
  it requires a final usage chunk and withholds `[DONE]` until settlement commits.
- `POST /api/v1/ai/provider/audio/transcriptions` and `/speech`: NITEC/OpenAI.
- `POST /api/v1/ai/provider/audio/speech-to-text` and `/text-to-speech/{voice}`:
  ElevenLabs. The provider header selects a fixed server-owned endpoint/key.
- `POST /api/v1/ai/provider/jobs`: issues a model-only, job-scoped 12-hour grant.
  It cannot authenticate to Workspace business APIs. Worker calls use the
  separate `/api/internal/ai/jobs/{id}/provider/...` namespace. Current user
  activity/company is checked against the original payer before each call.

`audio-credits-v1` introduces explicit `audio_seconds` and `text_characters`.
Token columns remain NULL for audio; they are never populated with durations.
These are **internal product quota weights**, not provider invoice prices:

| Operation | Quota weight |
| --- | --- |
| STT | 20,000 microcredits per started second (1.2 credits/minute) |
| TTS | 1,000 microcredits per submitted Unicode character (1 credit/1,000 characters) |

The gateway measures audio duration with ffprobe before dispatch and uses a
larger valid provider-reported duration if present. TTS counts the submitted text.
Unknown/unsupported models and missing measurement evidence fail closed. A lost
response/cancellation retains the reservation for reconciliation; it is not
treated as a zero-cost call. Product weights are pinned with the policy version.

## Deployment order

1. Deploy **workspace-backend**. Its normal deploy applies migration
   `20261022100600_ai_audio_measurements.sql`, installs ffprobe, imports missing
   OpenAI/ElevenLabs credentials from the running AI container into the protected
   backend env, and creates a dedicated job signing key. Existing backend keys
   are preserved. The original env backup is mode 0600; credentials are not
   printed. The script is idempotent and does not rotate configured keys.
2. Deploy **workspace-ai**. Both production Compose variants force authentication
   and accounting, use the internal Go Kanban API, and use priced default text
   models. Speech credentials now belong to Go (`WORKSPACE_AI_OPENAI_API_KEY`,
   `WORKSPACE_AI_ELEVENLABS_API_KEY`); NITEC uses Go's existing `ALEM_API_KEY`.
   The dedicated signing key must remain stable for queued jobs.
3. Deploy **workspace-front**. Nginx checks the Python accounting readiness marker
   before forwarding `/api/ask` and `/api/tools`. An old/unaccounted AI image
   remains inaccessible even if the frontend is deployed first.

Python persists only the narrow job grant, never the user's login token or a
provider credential, and erases it when the job finishes. Jobs created before
the migration have no authorization and fail safely with a request to resubmit.
Expired grants likewise require resubmission; they cannot create an unmetered
retry. Provider retries are disabled and job step identities survive restart.
Background jobs can wait for a confirmed pre-dispatch rate/concurrency rejection
using the same step identity. Network errors and uncertain paid attempts are
never retried this way.

The experimental direct Realtime WebSocket endpoints remain unavailable in
accounted mode because their long-lived session usage is a separate contract.
Upload-based voice, speech recognition, TTS and meeting jobs use the gateway.
Speaker separation uses the existing remote LLM mode in production, avoiding
local Whisper/pyannote workloads on the application VPS. The optional self-hosted
Spark/Silero endpoints retain their existing local processing behavior.

## Verification

- Go service/domain/handler/server tests and `TestAIAudioSettlementIntegration`
  against disposable PostgreSQL: correct units, budget gates, idempotent
  settlement, immutable results, old text accounting, job/company binding.
- `TestWorkspaceAIProbeRealAudio` with ffprobe: real WAV duration and invalid file.
- Python `tests/test_accounting.py` and the existing tool tests: payer isolation,
  provider-secret removal, multipart/SSE preservation, budget error propagation,
  private job grants and cleanup.
- Frontend `python3 scripts/test-ai-ingress.py --image nginx:1.27-alpine`: isolated
  old/new AI upstreams, path rewriting, methods, authorization and private marker.

Before claiming production readiness, verify authenticated text generation,
speech recognition and a meeting job through the site and confirm the resulting
attempts/settlements in the shared journal. `/health` alone is insufficient.
