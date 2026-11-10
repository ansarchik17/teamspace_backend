# Mobile Guide: Profile Education and Licenses/Certifications

## Purpose

This document explains how the profile section API works for mobile clients.

These endpoints let the app:

- show the current user's education history
- show the current user's licenses and certifications
- show the same profile sections for another user
- create, edit, and delete only the authenticated user's own records
- attach avatars and certificate files that were uploaded earlier through the file API

For a mobile developer, this matters because these endpoints are not just simple CRUD. They also include:

- access control between "my profile" and "another user's profile"
- date validation rules
- file upload integration through `object_name`
- generated download URLs in responses
- stable sort order for rendering timeline-style UI

## API Surface

All routes are protected and require auth.

Own profile:

- `GET /users/profile/education`
- `POST /users/profile/education`
- `GET /users/profile/education/{id}`
- `PUT /users/profile/education/{id}`
- `DELETE /users/profile/education/{id}`
- `GET /users/profile/licenses-certifications`
- `POST /users/profile/licenses-certifications`
- `GET /users/profile/licenses-certifications/{id}`
- `PUT /users/profile/licenses-certifications/{id}`
- `DELETE /users/profile/licenses-certifications/{id}`

Another user's profile:

- `GET /users/{user_id}/profile/education`
- `GET /users/{user_id}/profile/education/{id}`
- `GET /users/{user_id}/profile/licenses-certifications`
- `GET /users/{user_id}/profile/licenses-certifications/{id}`

Mobile implication:

- create, update, delete exist only for the authenticated user's own profile
- reading another user's profile is supported, but still goes through access checks

## How Access Works

The handler gets the authenticated user id from middleware, then passes both:

- `requesterID`: who is calling
- `targetUserID`: whose profile is being requested

If the requester reads their own profile, the service only checks that the user exists.

If the requester reads another user's profile, the service calls scoped access logic through `ensureUserScopedAccess(...)`.

Mobile implication:

- the app should not assume every authenticated user can open every other user's profile
- `403` or `404` style responses must be handled cleanly in UI

## Data Model

### Education

Each education item contains:

- `id`
- `user_id`
- `school_name`
- `school_avatar_url`
- `school_avatar_object_name`
- `degree`
- `field_of_study`
- `start_date`
- `end_date`
- `created_at`
- `updated_at`

### License/Certification

Each item contains:

- `id`
- `user_id`
- `name`
- `issuing_organization`
- `skill_name`
- `avatar_url`
- `avatar_object_name`
- `issue_date`
- `expiration_date`
- `attachment_object_name`
- `attachment_file_name`
- `attachment_content_type`
- `attachment_size_bytes`
- `attachment_download_url`
- `created_at`
- `updated_at`

Mobile implication:

- education is mainly timeline data
- licenses/certifications support both an avatar and a downloadable attachment
- the backend returns both storage references and public/download URLs, so mobile usually renders URLs and stores ids/object names only when editing

## Date Rules

The backend uses `YYYY-MM-DD` only.

Education:

- `start_date` is required
- `end_date` is optional
- if `end_date` is present, it must be `>= start_date`

Licenses/certifications:

- `issue_date` is required
- `expiration_date` is optional
- if `expiration_date` is present, it must be `>= issue_date`

Mobile implication:

- send plain date strings like `2024-09-01`
- do not send full ISO timestamps for these fields
- validate on the client too, otherwise users will hit avoidable `400` validation errors

## File Upload Flow

Profile section endpoints do not upload binary files themselves.

The expected flow is:

1. Upload the file first through `POST /files/upload`
2. Receive or derive the uploaded file `object_name`
3. Pass that `object_name` into the profile request

Used fields:

- education avatar: `school_avatar_object_name`
- certificate avatar: `avatar_object_name`
- certificate file: `attachment_object_name`

The service verifies that the uploaded object exists through `fileService.ObjectExists(...)`.

Mobile implication:

- profile create/update is a two-step flow when files are involved
- do not send local file paths in profile payloads
- upload first, then submit metadata

## URL vs Object Name

The service normalizes asset input.

Behavior:

- if `*_object_name` is present, backend prefers it and clears direct URL usage
- if only URL is sent and it contains `object_name` in query params, backend extracts the object name from the URL
- if only a plain URL is sent, backend stores it as URL

Mobile recommendation:

- prefer sending `*_object_name`
- treat returned URL fields as display/download values, not as the source of truth for future writes

Reason:

- object names are stable storage references
- returned URLs can be generated or time-limited

## Response Enrichment

The backend enriches responses if file storage is available.

Education:

- if `school_avatar_object_name` exists, response may contain a generated `school_avatar_url`

Licenses/certifications:

- if `avatar_object_name` exists, response may contain a generated `avatar_url`
- if `attachment_object_name` exists, response may contain a generated `attachment_download_url`

Mobile implication:

- use response URLs for rendering image/file actions
- do not construct download links manually

## Sorting Rules

List endpoints are already sorted in the database.

Education:

- `ORDER BY start_date DESC, id DESC`

Licenses/certifications:

- `ORDER BY issue_date DESC, id DESC`

Mobile implication:

- you can render list results directly without additional client sorting
- newest/current items will appear first

## Create and Update Payloads

### Create education

Required:

- `school_name`
- `field_of_study`
- `start_date`

Optional:

- `school_avatar_url`
- `school_avatar_object_name`
- `degree`
- `end_date`

Example:

```json
{
  "school_name": "SDU University",
  "school_avatar_object_name": "profile/avatars/sdu.png",
  "degree": "Bachelor",
  "field_of_study": "Computer Science",
  "start_date": "2020-09-01",
  "end_date": "2024-06-30"
}
```

### Update education

All fields are optional, but if a field is provided it is validated.

To clear optional text/date values, send an empty string or omit based on field semantics. The backend trims values and may normalize empty optional fields to `null`.

### Create license/certification

Required:

- `name`
- `issuing_organization`
- `skill_name`
- `issue_date`

Optional:

- `avatar_url`
- `avatar_object_name`
- `expiration_date`
- `attachment_object_name`
- `attachment_file_name`
- `attachment_content_type`
- `attachment_size_bytes`

Example:

```json
{
  "name": "AWS Certified Developer",
  "issuing_organization": "Amazon Web Services",
  "skill_name": "Cloud",
  "avatar_object_name": "profile/cert-avatars/aws.png",
  "issue_date": "2025-01-10",
  "expiration_date": "2028-01-10",
  "attachment_object_name": "profile/cert-files/aws-cert.pdf",
  "attachment_file_name": "aws-cert.pdf",
  "attachment_content_type": "application/pdf",
  "attachment_size_bytes": 245678
}
```

### Update license/certification

All fields are optional. If `attachment_size_bytes` is provided, it must be `>= 0`.

## Typical Mobile Screen Flows

### 1. Open my profile

Recommended calls:

- `GET /users/profile/education`
- `GET /users/profile/licenses-certifications`

Render:

- education cards/timeline
- certification cards
- image previews from `school_avatar_url` or `avatar_url`
- download/open action from `attachment_download_url`

### 2. Open another user's profile

Recommended calls:

- `GET /users/{user_id}/profile/education`
- `GET /users/{user_id}/profile/licenses-certifications`

UI rule:

- hide edit/delete actions

### 3. Add education with school avatar

Flow:

1. user selects image
2. mobile uploads image through file API
3. mobile receives `object_name`
4. mobile sends `POST /users/profile/education` with `school_avatar_object_name`
5. mobile refreshes list or inserts returned item locally

### 4. Add certification with PDF attachment

Flow:

1. upload avatar if needed
2. upload certificate file
3. send metadata plus `avatar_object_name` and `attachment_object_name`
4. render `attachment_download_url` from response

### 5. Edit existing item

Flow:

1. load existing item
2. keep old values in form
3. upload new files only if user changed them
4. send partial `PUT`
5. update local state from response

## Error Cases Mobile Must Handle

Validation errors:

- invalid JSON body
- missing required fields
- empty required strings after trim
- invalid date format
- end date earlier than start date
- expiration date earlier than issue date
- negative `attachment_size_bytes`

Not found:

- education item not found
- license/certification item not found
- target user not found

Access-related:

- requester cannot view another user's profile

File-related:

- provided `object_name` does not exist in storage
- file service unavailable may affect generated URLs

Mobile implication:

- show validation messages near form fields when possible
- treat `404` as deleted/missing record and refresh list
- treat access errors as a blocked screen state, not a crash

## Backend Behavior That Helps Mobile

This API is useful for mobile because it already centralizes several things:

- ownership checks for write operations
- visibility checks for reading another user's profile
- canonical date validation
- sorting for timeline display
- generation of file URLs for rendering and download
- normalization of empty optional values

Without this backend behavior, mobile would need to duplicate business rules and would become inconsistent with web/backend behavior.

## Practical Recommendations for Mobile Team

- Use list endpoints as the main source for profile section screens.
- Use detail endpoints only when opening a single record screen directly.
- Prefer `object_name` over raw URL when saving avatars or files.
- Store record `id` locally for edit/delete actions.
- Refresh data after create/update/delete instead of guessing derived URLs.
- Expect returned URLs to be generated values and potentially short-lived.
- Validate dates on the client using `YYYY-MM-DD`.
- Do not expose edit actions when viewing another user's profile.

## Code References

Main route registration:

- [internal/server/router.go](d:/alem_project/alem-super-app-backend/internal/server/router.go:219)

HTTP handlers:

- [internal/handler/profile_section_handler.go](d:/alem_project/alem-super-app-backend/internal/handler/profile_section_handler.go:15)

Business logic:

- [internal/service/profile_section_service.go](d:/alem_project/alem-super-app-backend/internal/service/profile_section_service.go:15)

Persistence:

- [internal/repository/profile_section_repository.go](d:/alem_project/alem-super-app-backend/internal/repository/profile_section_repository.go:14)

Data contracts:

- [internal/domain/profile_sections.go](d:/alem_project/alem-super-app-backend/internal/domain/profile_sections.go:1)

Database migration:

- [db/migrations/20260428000200_create_user_profile_sections.sql](d:/alem_project/alem-super-app-backend/db/migrations/20260428000200_create_user_profile_sections.sql:1)
