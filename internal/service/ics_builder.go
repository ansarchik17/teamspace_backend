package service

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// ICSMethod defines the iCalendar method (REQUEST, CANCEL, etc.)
type ICSMethod string

const (
	ICSMethodRequest ICSMethod = "REQUEST"
	ICSMethodCancel  ICSMethod = "CANCEL"
)

// ICSAttendee defines an attendee for the event
type ICSAttendee struct {
	Name   string
	Email  string
	Status string // NEEDS-ACTION, ACCEPTED, DECLINED, TENTATIVE
}

// ICSEventData contains data needed to build the VEVENT
type ICSEventData struct {
	UID         string
	Method      ICSMethod
	Organizer   ICSAttendee
	Attendees   []ICSAttendee
	Summary     string
	Description string
	URL         string
	Location    string
	Start       time.Time
	End         time.Time
	Created     time.Time
	Modified    time.Time
	Sequence    int
	Status      string // CONFIRMED, CANCELLED
}

// BuildICS generates an iCalendar string from the given ICSEventData
func BuildICS(data ICSEventData) string {
	var buf bytes.Buffer
	buf.WriteString("BEGIN:VCALENDAR\r\n")
	buf.WriteString("VERSION:2.0\r\n")
	buf.WriteString("PRODID:-//Alem Workspace//Calendar//RU\r\n")
	buf.WriteString("CALSCALE:GREGORIAN\r\n")
	buf.WriteString(fmt.Sprintf("METHOD:%s\r\n", data.Method))

	buf.WriteString("BEGIN:VEVENT\r\n")
	buf.WriteString(fmt.Sprintf("UID:%s\r\n", data.UID))

	// DTSTAMP is required (when the instance of the iCalendar object was created)
	dtstamp := time.Now().UTC().Format("20060102T150405Z")
	buf.WriteString(fmt.Sprintf("DTSTAMP:%s\r\n", dtstamp))

	// DTSTART and DTEND
	buf.WriteString(fmt.Sprintf("DTSTART:%s\r\n", data.Start.UTC().Format("20060102T150405Z")))
	buf.WriteString(fmt.Sprintf("DTEND:%s\r\n", data.End.UTC().Format("20060102T150405Z")))

	if !data.Created.IsZero() {
		buf.WriteString(fmt.Sprintf("CREATED:%s\r\n", data.Created.UTC().Format("20060102T150405Z")))
	}
	if !data.Modified.IsZero() {
		buf.WriteString(fmt.Sprintf("LAST-MODIFIED:%s\r\n", data.Modified.UTC().Format("20060102T150405Z")))
	}

	buf.WriteString(fmt.Sprintf("SUMMARY:%s\r\n", escapeICS(data.Summary)))

	if data.Description != "" {
		buf.WriteString(fmt.Sprintf("DESCRIPTION:%s\r\n", escapeICS(data.Description)))
	}

	if data.Location != "" {
		buf.WriteString(fmt.Sprintf("LOCATION:%s\r\n", escapeICS(data.Location)))
	}

	if data.URL != "" {
		buf.WriteString(fmt.Sprintf("URL:%s\r\n", strings.NewReplacer("\r", "", "\n", "").Replace(data.URL)))
	}

	// Organizer
	if data.Organizer.Email != "" {
		orgName := escapeICS(data.Organizer.Name)
		if orgName == "" {
			buf.WriteString(fmt.Sprintf("ORGANIZER:mailto:%s\r\n", data.Organizer.Email))
		} else {
			buf.WriteString(fmt.Sprintf("ORGANIZER;CN=%s:mailto:%s\r\n", orgName, data.Organizer.Email))
		}
	}

	// Attendees
	for _, att := range data.Attendees {
		if att.Email == "" {
			continue
		}
		status := "NEEDS-ACTION"
		if att.Status != "" {
			status = att.Status
		}
		attName := escapeICS(att.Name)
		if attName == "" {
			buf.WriteString(fmt.Sprintf("ATTENDEE;ROLE=REQ-PARTICIPANT;PARTSTAT=%s;RSVP=TRUE:mailto:%s\r\n", status, att.Email))
		} else {
			buf.WriteString(fmt.Sprintf("ATTENDEE;CN=%s;ROLE=REQ-PARTICIPANT;PARTSTAT=%s;RSVP=TRUE:mailto:%s\r\n", attName, status, att.Email))
		}
	}

	buf.WriteString(fmt.Sprintf("SEQUENCE:%d\r\n", data.Sequence))

	status := "CONFIRMED"
	if data.Status != "" {
		status = data.Status
	}
	buf.WriteString(fmt.Sprintf("STATUS:%s\r\n", status))

	buf.WriteString("END:VEVENT\r\n")
	buf.WriteString("END:VCALENDAR\r\n")

	// The standard requires lines to be folded if longer than 75 octets,
	// but many modern clients accept longer lines. For strictness:
	return foldICSLines(buf.String())
}

func escapeICS(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

func foldICSLines(s string) string {
	var out strings.Builder
	lines := strings.Split(s, "\r\n")
	for i, line := range lines {
		// Ignore the last empty split artifact if it exists
		if i == len(lines)-1 && line == "" {
			continue
		}

		runes := []rune(line)
		if len(runes) <= 75 {
			out.WriteString(line)
			out.WriteString("\r\n")
			continue
		}

		out.WriteString(string(runes[:75]))
		out.WriteString("\r\n")

		rem := runes[75:]
		for len(rem) > 0 {
			out.WriteString(" ")
			chunkSize := 74
			if len(rem) < chunkSize {
				chunkSize = len(rem)
			}
			out.WriteString(string(rem[:chunkSize]))
			out.WriteString("\r\n")
			rem = rem[chunkSize:]
		}
	}
	return out.String()
}
