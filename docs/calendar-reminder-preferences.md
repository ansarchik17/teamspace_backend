# Calendar reminder preferences

`POST /api/v1/calendar/events` and `PUT /api/v1/calendar/events/:id` accept
`reminder_minutes: null` to disable the caller's reminder. A numeric value sets
the reminder; `0` means at the event start, not disabled.

When the field is omitted, creation retains the legacy 15-minute default and
updates retain the existing reminder. Updating the attendee list does not
re-enable reminders disabled by existing attendees. New attendees receive the
legacy default.

The selected `calendar_id` is checked for ownership and returned by the event
details endpoint together with the caller's reminder. Reminder changes during
event editing are committed in the same transaction as the event and attendees.
