package service

import (
	"bytes"
	"fmt"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"html/template"
	"net/url"
	"strings"
	"time"
)

var calendarEmailHTML = template.Must(template.New("calendar_email").Parse(`<!DOCTYPE html>
<html lang="{{.Language}}"><head><meta charset="utf-8">
<style>body{font-family:Arial,sans-serif;line-height:1.6;color:#333;max-width:600px;margin:0 auto;padding:20px}.header{background:#f8f9fa;padding:20px;border-radius:8px 8px 0 0;border-bottom:2px solid #e9ecef}.content{padding:20px;border:1px solid #e9ecef}.details{background:#f8f9fa;padding:15px;border-radius:6px;margin:20px 0}.btn{display:inline-block;padding:10px 20px;text-decoration:none;border-radius:4px;font-weight:bold;margin:0 5px;color:#fff}.btn-accept{background:#28a745}.btn-decline{background:#dc3545}.btn-tentative{background:#ffc107;color:#212529}.btn-app{background:#007bff;margin-top:15px}.footer{margin-top:20px;font-size:12px;color:#6c757d;text-align:center}</style></head>
<body><div class="header"><h2>Alem Workspace</h2></div><div class="content">
<p>{{.Greeting}}</p><p>{{.Lead}}</p><div class="details">
<p><strong>{{.DateLabel}}</strong> {{.Date}}, Asia/Almaty</p>
<p><strong>{{.OrganizerLabel}}</strong> {{.Organizer}}</p>
<p><strong>{{.LocationLabel}}</strong> {{.Location}}</p>
<p><strong>{{.DescriptionLabel}}</strong> {{.Description}}</p></div>
{{if not .IsCancel}}<p>{{.Actions}}</p><div style="text-align:center;margin:25px 0">
<a href="{{.AcceptURL}}" class="btn btn-accept">{{.Accept}}</a>
<a href="{{.TentativeURL}}" class="btn btn-tentative">{{.Tentative}}</a>
<a href="{{.DeclineURL}}" class="btn btn-decline">{{.Decline}}</a>
<div><a href="{{.EventURL}}" class="btn btn-app">{{.Open}}</a></div></div>{{end}}
</div><div class="footer">{{.Footer}}</div></body></html>`))

func BuildCalendarInviteEmail(input CalendarInviteEmailInput, acceptToken, declineToken, tentativeToken string) NotificationEmailTemplateData {
	locale := i18n.ResolveRecipient(string(input.Language))
	text := func(key string) string { return i18n.Text(locale, "email.calendar."+key, nil) }
	kind := "invite"
	if input.IsCancel {
		kind = "cancel"
	} else if input.IsUpdate {
		kind = "update"
	}
	organizer := firstNonEmpty(input.OrganizerName, i18n.Text(locale, "email.organizer", nil))
	attendee := firstNonEmpty(input.AttendeeName, i18n.Text(locale, "email.user", nil))
	params := map[string]any{"Title": input.EventTitle, "Organizer": organizer}
	subject := i18n.Text(locale, "email.calendar."+kind+".subject", params)
	lead := i18n.Text(locale, "email.calendar."+kind+".body", params)
	greeting := i18n.Text(locale, "email.greeting", map[string]any{"Name": attendee})
	date := input.StartTime.In(time.FixedZone("Asia/Almaty", 5*60*60)).Format("02.01.2006 15:04")
	location := firstNonEmpty(input.Location, text("unspecified"))
	description := firstNonEmpty(input.Description, text("no_description"))
	actionURL := func(token string) string {
		return strings.TrimRight(input.BaseURL, "/") + "/api/v1/calendar/invitations/action?token=" + url.QueryEscape(token)
	}
	data := struct {
		Language                                                                                                                                                                                                             i18n.Locale
		IsCancel                                                                                                                                                                                                             bool
		Greeting, Lead, DateLabel, Date, OrganizerLabel, Organizer, LocationLabel, Location, DescriptionLabel, Description, Actions, Accept, Decline, Tentative, Open, AcceptURL, DeclineURL, TentativeURL, EventURL, Footer string
	}{
		Language: locale, IsCancel: input.IsCancel, Greeting: greeting, Lead: lead, DateLabel: text("date"), Date: date, OrganizerLabel: text("organizer"), Organizer: organizer, LocationLabel: text("location"), Location: location, DescriptionLabel: text("description"), Description: description, Actions: text("actions"), Accept: text("accept"), Decline: text("decline"), Tentative: text("tentative"), Open: text("open"), AcceptURL: actionURL(acceptToken), DeclineURL: actionURL(declineToken), TentativeURL: actionURL(tentativeToken), EventURL: strings.TrimRight(input.BaseURL, "/") + "/calendar/events/" + input.EventID.String(), Footer: text("footer"),
	}
	var body bytes.Buffer
	if err := calendarEmailHTML.Execute(&body, data); err != nil {
		panic(fmt.Errorf("calendar email template: %w", err))
	}
	plain := fmt.Sprintf("%s\n\n%s\n\n%s %s, Asia/Almaty\n%s %s\n%s %s\n%s %s\n\n", greeting, lead, data.DateLabel, date, data.OrganizerLabel, organizer, data.LocationLabel, location, data.DescriptionLabel, description)
	if !input.IsCancel {
		plain += fmt.Sprintf("%s\n%s: %s\n%s: %s\n%s: %s\n%s: %s\n\n", data.Actions, data.Accept, data.AcceptURL, data.Decline, data.DeclineURL, data.Tentative, data.TentativeURL, data.Open, data.EventURL)
	}
	plain += data.Footer + "\n"
	return NotificationEmailTemplateData{Subject: subject, HTMLBody: body.String(), TextBody: plain}
}
