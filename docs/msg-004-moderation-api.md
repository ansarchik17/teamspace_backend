# MSG-004: message moderation API

All endpoints require `Authorization: Bearer <token>`.

## Rules

- The message author may edit a message for 48 hours after `created_at`.
- The message author may delete a message for everyone for 48 hours after `created_at`.
- Any active personal-chat participant or group member may delete a message only for themselves at any time.
- A group `owner` or `admin` may delete any group message for everyone without a time limit.
- A channel `owner` or `admin` may edit or delete any channel post/reply without a time limit.
- A channel reply author may edit or delete their own reply within the same 48-hour window.
- Deleted messages are removed from reply trees. Globally deleted pinned messages are automatically unpinned.
- The boundary is inclusive: an action at exactly `created_at + 48h` is accepted.

Clients can read the current limits instead of hard-coding them:

```http
GET /api/v1/messages/moderation/rules
```

```json
{
  "edit_window_seconds": 172800,
  "delete_for_everyone_window_seconds": 172800,
  "delete_scopes": ["me", "everyone"]
}
```

## Edit a personal/group message

```http
PUT /api/v1/messages/{message_id}
Content-Type: application/json

{"content":"Updated text"}
```

The existing `message_edited` WebSocket event is sent to every active participant. Its `content` contains the complete updated message view.

## Delete one personal/group message

Delete only for the current user (default, backward compatible):

```http
DELETE /api/v1/messages/{message_id}
```

Delete for every participant:

```http
DELETE /api/v1/messages/{message_id}?scope=everyone
```

## Bulk delete personal/group messages

```http
DELETE /api/v1/messages
Content-Type: application/json

{
  "message_ids": ["uuid-1", "uuid-2"],
  "scope": "everyone"
}
```

`scope` is optional and defaults to `me`. The request is validated completely before any message is deleted; mixed unauthorized/expired selections fail without a partial deletion.

## Channel posts and replies

The existing endpoints remain unchanged:

```http
PUT /api/v1/channels/posts/{post_id}
DELETE /api/v1/channels/posts/{post_id}
```

Channel deletion is always for everyone. The WebSocket `message_deleted` event contains:

```json
{
  "type": "message_deleted",
  "conversation_id": "channel-uuid",
  "message_id": "post-uuid",
  "content": {"is_for_everyone": true}
}
```

For personal/group messages, `message_deleted.content.is_for_everyone` is `false` for a personal hide and `true` for global deletion. `new_last_message` contains the recalculated chat-list preview where available.

## Errors

| HTTP | `error.code` | Meaning |
|---|---|---|
| 400 | `VALIDATION_ERROR` | Invalid ID, body, or delete scope |
| 401 | `UNAUTHORIZED` | Missing/invalid access token |
| 403 | `NOT_PARTICIPANT` / `NOT_GROUP_MEMBER` | User cannot access the chat |
| 403 | `CANNOT_EDIT_MESSAGE` | User is not allowed to edit this message |
| 403 | `CANNOT_DELETE_MESSAGE` | User is not allowed to delete this message for everyone |
| 403 | `EDIT_WINDOW_EXPIRED` | The author edit window expired |
| 403 | `DELETE_WINDOW_EXPIRED` | The author delete-for-everyone window expired |
| 404 | `MESSAGE_NOT_FOUND` | Message does not exist or was globally deleted |

