package service

import (
	"bytes"
	"fmt"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"html/template"
	"strings"
	"time"
)

type NotificationEmailTemplateInput struct {
	Language     i18n.Locale
	Title        string
	GreetingName string
	Message      string
	ActionURL    string
	ActionText   string
	FooterText   string
	CreatedAt    time.Time
	AppName      string
}

type NotificationEmailTemplateData struct{ Subject, HTMLBody, TextBody string }

var notificationEmailHTML = template.Must(template.New("notification_email").Parse(`<!DOCTYPE html>
<html lang="{{.Language}}"><head><meta charset="utf-8"></head>
<body style="margin:0;padding:0;background:#f3f6fb;font-family:Arial,'Helvetica Neue',sans-serif;color:#16324f;">
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="background:#f3f6fb;padding:32px 0;"><tr><td align="center">
<table role="presentation" width="640" cellspacing="0" cellpadding="0" style="width:640px;max-width:640px;background:#ffffff;border-radius:16px;overflow:hidden;border:1px solid #dce6f2;">
<tr><td style="background:#0B63CE;padding:24px 32px;color:#ffffff;font-size:24px;font-weight:700;">{{.AppName}}</td></tr>
<tr><td style="padding:32px;">
<div style="font-size:16px;line-height:24px;margin-bottom:16px;">{{.Greeting}}</div>
<div style="background:#f8fbff;border:1px solid #d7e7fb;border-radius:12px;padding:24px;">
<div style="font-size:22px;line-height:28px;font-weight:700;margin-bottom:12px;">{{.Subject}}</div>
<div style="font-size:16px;line-height:24px;color:#2d4a67;white-space:pre-line;">{{.Message}}</div>
{{if .ActionURL}}<div style="margin-top:24px;"><a href="{{.ActionURL}}" style="display:inline-block;padding:12px 20px;background:#0B63CE;color:#ffffff;text-decoration:none;border-radius:8px;font-weight:600;">{{.ActionText}}</a></div>{{end}}
</div><div style="margin-top:24px;font-size:14px;line-height:22px;color:#5c738d;">{{.Hint}}</div>
<div style="margin-top:20px;font-size:12px;line-height:18px;color:#7f93a8;">{{.Created}}</div></td></tr>
<tr><td style="padding:20px 32px;border-top:1px solid #e5edf6;font-size:12px;line-height:18px;color:#7f93a8;">{{.FooterText}}</td></tr>
</table></td></tr></table></body></html>`))

func BuildNotificationEmailTemplate(input NotificationEmailTemplateInput) NotificationEmailTemplateData {
	locale := i18n.ResolveRecipient(string(input.Language))
	text := func(key string) string { return i18n.Text(locale, "email."+key, nil) }
	appName := firstNonEmpty(input.AppName, "Team Workspace")
	subject := firstNonEmpty(input.Title, text("notification.subject"))
	message := firstNonEmpty(input.Message, text("notification.hint"))
	name := firstNonEmpty(input.GreetingName, text("user"))
	footer := firstNonEmpty(input.FooterText, text("footer"))
	action := firstNonEmpty(input.ActionText, text("open"))
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	greeting := i18n.Text(locale, "email.greeting", map[string]any{"Name": name})
	created := i18n.Text(locale, "email.created", map[string]any{"Time": createdAt.Format("02.01.2006 15:04 MST")})
	data := struct {
		Language                                                                              i18n.Locale
		AppName, Subject, Message, Greeting, ActionURL, ActionText, Hint, Created, FooterText string
	}{locale, appName, subject, message, greeting, strings.TrimSpace(input.ActionURL), action, text("notification.hint"), created, footer}
	var body bytes.Buffer
	if err := notificationEmailHTML.Execute(&body, data); err != nil {
		panic(fmt.Errorf("notification email template: %w", err))
	}
	plain := appName + "\n\n" + greeting + "\n\n" + subject + "\n\n" + message + "\n\n" + data.Hint + "\n"
	if data.ActionURL != "" {
		plain += "\n" + action + ": " + data.ActionURL + "\n"
	}
	plain += "\n" + created + "\n\n" + footer
	return NotificationEmailTemplateData{Subject: subject, HTMLBody: body.String(), TextBody: plain}
}
