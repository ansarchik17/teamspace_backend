# Meeting analysis → Notes

Apply `20260922000100_note_meeting_result.sql` before deploying this backend and the corresponding frontend.

Authenticated `POST /api/v1/notes/meeting-result` accepts the usual JSON note fields plus `job_id` (nonblank, max 200 bytes). A nonempty `content_doc` is required; body limit is 4 MiB. The supplied document is a user-authored import, not a trusted provider response. This endpoint does not fetch provider jobs or grant access to them.

Returns the saved note (200). A unique `(user_id, meeting_job_id)` index makes concurrent and repeated saves idempotent. Retries return the existing note without replacing its content, folder, pin, archive state or version. Edit existing notes through the usual versioned endpoints. A result already in trash returns 409 and must be restored explicitly. After permanent deletion a fresh import is possible. Folder ownership is validated with the existing Notes service; user IDs always come from authentication.

The Notes meeting panel now saves through this endpoint, includes tasks/topics/open questions/risks in the document, and supports folder and pin choices for new notes. Sharing first saves/opens the note, then uses the existing chat picker and note-sharing authorization. No message is sent automatically.

The recognition API `/api/tools/meeting-ai/...` is NOT implemented in this repository. Its deployment/source must be supplied to audit job ownership, durable results, retries and cancellation. This change does not introduce a second AI worker or claim that analysis runs locally. The panel's “Сбросить” only clears its selection and does not cancel a server job.

Tests:

```sh
go test ./internal/handler -run TestMeetingResultRejectsInvalidInput
TEST_NOTES_DATABASE_URL=postgres://... go test ./internal/handler -run TestMeetingResultPersistence
```

The integration test creates and drops a uniquely named test schema and applies the actual Notes migrations. Use an isolated disposable database. It covers simultaneous saves, user isolation, unchanged edits, trash conflicts, foreign folders and regular note creation.
