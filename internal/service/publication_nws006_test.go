package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestNWS006_Constants(t *testing.T) {
	if domain.PublicationImportanceNormal != "normal" {
		t.Errorf("expected normal, got %s", domain.PublicationImportanceNormal)
	}
	if domain.PublicationImportanceImportant != "important" {
		t.Errorf("expected important, got %s", domain.PublicationImportanceImportant)
	}
	if domain.PublicationImportanceUrgent != "urgent" {
		t.Errorf("expected urgent, got %s", domain.PublicationImportanceUrgent)
	}

	if domain.BannerColorUrgent != "urgent" {
		t.Errorf("expected urgent, got %s", domain.BannerColorUrgent)
	}
	if domain.BannerColorWarning != "warning" {
		t.Errorf("expected warning, got %s", domain.BannerColorWarning)
	}
	if domain.BannerColorInfo != "info" {
		t.Errorf("expected info, got %s", domain.BannerColorInfo)
	}
	if domain.BannerColorPrimary != "primary" {
		t.Errorf("expected primary, got %s", domain.BannerColorPrimary)
	}

	if domain.NotificationTypePublicationAlert != "publication_alert" {
		t.Errorf("expected publication_alert, got %s", domain.NotificationTypePublicationAlert)
	}
}

func TestNWS006_PinAndBannerJSONSerialization(t *testing.T) {
	future := time.Now().Add(48 * time.Hour)

	// 1. PinPublicationRequest
	pinReqJSON := `{"pinned_until":"` + future.Format(time.RFC3339) + `"}`
	var pinReq domain.PinPublicationRequest
	if err := json.Unmarshal([]byte(pinReqJSON), &pinReq); err != nil {
		t.Fatalf("failed to unmarshal PinPublicationRequest: %v", err)
	}
	if pinReq.PinnedUntil == nil {
		t.Fatal("expected pinned_until to be non-nil")
	}

	// 2. SetBannerRequest
	actionText := "Ознакомиться"
	actionURL := "https://workspace.alem.kz/regulations"
	bannerReqJSON := `{
		"is_banner": true,
		"banner_until": "` + future.Format(time.RFC3339) + `",
		"banner_action_text": "Ознакомиться",
		"banner_action_url": "https://workspace.alem.kz/regulations",
		"banner_color": "urgent"
	}`
	var bannerReq domain.SetBannerRequest
	if err := json.Unmarshal([]byte(bannerReqJSON), &bannerReq); err != nil {
		t.Fatalf("failed to unmarshal SetBannerRequest: %v", err)
	}
	if !bannerReq.IsBanner {
		t.Error("expected is_banner to be true")
	}
	if bannerReq.BannerActionText == nil || *bannerReq.BannerActionText != actionText {
		t.Errorf("expected action text %s, got %v", actionText, bannerReq.BannerActionText)
	}
	if bannerReq.BannerActionURL == nil || *bannerReq.BannerActionURL != actionURL {
		t.Errorf("expected action url %s, got %v", actionURL, bannerReq.BannerActionURL)
	}
	if bannerReq.BannerColor == nil || *bannerReq.BannerColor != domain.BannerColorUrgent {
		t.Errorf("expected color %s, got %v", domain.BannerColorUrgent, bannerReq.BannerColor)
	}

	// 3. BroadcastPublicationRequest
	broadcastJSON := `{"send_push": true, "send_email": true, "force": true}`
	var broadcastReq domain.BroadcastPublicationRequest
	if err := json.Unmarshal([]byte(broadcastJSON), &broadcastReq); err != nil {
		t.Fatalf("failed to unmarshal BroadcastPublicationRequest: %v", err)
	}
	if broadcastReq.SendPush == nil || !*broadcastReq.SendPush {
		t.Error("expected send_push to be true")
	}
	if broadcastReq.SendEmail == nil || !*broadcastReq.SendEmail {
		t.Error("expected send_email to be true")
	}
	if !broadcastReq.Force {
		t.Error("expected force to be true")
	}

	// 4. PublicationBroadcastStatsResponse
	pubID := uuid.New()
	now := time.Now()
	stats := domain.PublicationBroadcastStatsResponse{
		PublicationID:          pubID,
		Importance:             domain.PublicationImportanceUrgent,
		TotalTargetUsers:       150,
		AcknowledgedCount:      92,
		AcknowledgementPercent: 61.33,
		SentPushCount:          148,
		SentEmailCount:         145,
		LastBroadcastAt:        &now,
		Broadcasts: []domain.BroadcastRecord{
			{
				ID:            uuid.New(),
				PublicationID: pubID,
				TriggeredBy: domain.UserMini{
					ID:        1,
					FirstName: "Admin",
					LastName:  "User",
				},
				Importance:     domain.PublicationImportanceUrgent,
				TargetAudience: "all",
				SentPushCount:  148,
				SentEmailCount: 145,
				SentAt:         now,
				Status:         "completed",
			},
		},
	}

	statsBytes, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("failed to marshal broadcast stats: %v", err)
	}
	var parsedStats map[string]interface{}
	if err := json.Unmarshal(statsBytes, &parsedStats); err != nil {
		t.Fatalf("failed to unmarshal broadcast stats JSON: %v", err)
	}
	if parsedStats["total_target_users"] != float64(150) {
		t.Errorf("expected 150 total target users, got %v", parsedStats["total_target_users"])
	}
	if parsedStats["acknowledged_count"] != float64(92) {
		t.Errorf("expected 92 acknowledged, got %v", parsedStats["acknowledged_count"])
	}

	// 5. PublicationAcknowledgementsResponse
	acks := domain.PublicationAcknowledgementsResponse{
		TotalCount: 1,
		Users: []domain.PublicationAcknowledgementUser{
			{
				User: domain.UserMini{
					ID:        42,
					FirstName: "Алихан",
					LastName:  "Нурланов",
					FullName:  "Алихан Нурланов",
				},
				AcknowledgedAt: time.Now(),
			},
		},
	}
	ackBytes, err := json.Marshal(acks)
	if err != nil {
		t.Fatalf("failed to marshal acknowledgements response: %v", err)
	}
	var parsedAcks map[string]interface{}
	if err := json.Unmarshal(ackBytes, &parsedAcks); err != nil {
		t.Fatalf("failed to unmarshal acknowledgements JSON: %v", err)
	}
	if parsedAcks["total_count"] != float64(1) {
		t.Errorf("expected total_count 1, got %v", parsedAcks["total_count"])
	}
}

func TestNWS006_PublicationResponseFields(t *testing.T) {
	now := time.Now()
	color := domain.BannerColorWarning
	actionText := "Перейти"
	actionURL := "https://workspace.alem.kz"

	pubResp := domain.PublicationResponse{
		ID:                uuid.New(),
		Title:             "Экстренное оповещение",
		Importance:        domain.PublicationImportanceUrgent,
		IsPinned:          true,
		PinnedAt:          &now,
		PinnedBy:          &domain.UserMini{ID: 10, FullName: "Админ"},
		IsBanner:          true,
		BannerColor:       &color,
		BannerActionText:  &actionText,
		BannerActionURL:   &actionURL,
		IsBannerDismissed: false,
	}

	bytes, err := json.Marshal(pubResp)
	if err != nil {
		t.Fatalf("failed to marshal publication response: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if parsed["is_pinned"] != true {
		t.Errorf("expected is_pinned true, got %v", parsed["is_pinned"])
	}
	if parsed["is_banner"] != true {
		t.Errorf("expected is_banner true, got %v", parsed["is_banner"])
	}
	if parsed["banner_color"] != "warning" {
		t.Errorf("expected banner_color warning, got %v", parsed["banner_color"])
	}
	if parsed["banner_action_text"] != "Перейти" {
		t.Errorf("expected banner_action_text Перейти, got %v", parsed["banner_action_text"])
	}
	if parsed["is_banner_dismissed"] != false {
		t.Errorf("expected is_banner_dismissed false, got %v", parsed["is_banner_dismissed"])
	}
}

func TestNWS006_EmailTemplateRendering(t *testing.T) {
	svc := &PublicationService{
		appPublicURL: "https://workspace.alem.kz",
	}

	content := "Всем сотрудникам необходимо ознакомиться с регламентом безопасности."
	htmlContent := "<p>" + content + "</p>"
	pub := &domain.Publication{
		ID:          uuid.New(),
		Title:       "Новый регламент информационной безопасности",
		Content:     &content,
		ContentHTML: htmlContent,
		Importance:  domain.PublicationImportanceUrgent,
	}

	renderedHTML := svc.renderPublicationEmailHTML(pub)

	if !strings.Contains(renderedHTML, "Новый регламент информационной безопасности") {
		t.Error("expected rendered email to contain publication title")
	}
	if !strings.Contains(renderedHTML, content) {
		t.Error("expected rendered email to contain publication body")
	}
	if !strings.Contains(renderedHTML, "https://workspace.alem.kz/publications/"+pub.ID.String()) {
		t.Error("expected rendered email to contain direct publication link")
	}
	if !strings.Contains(renderedHTML, "#ef4444") {
		t.Error("expected urgent email to contain red alert badge color")
	}

	// Test important badge color
	pub.Importance = domain.PublicationImportanceImportant
	renderedImportant := svc.renderPublicationEmailHTML(pub)
	if !strings.Contains(renderedImportant, "#f59e0b") {
		t.Error("expected important email to contain warning alert badge color")
	}
	pub.Title = `<img src="https://attacker.invalid/track">`
	pub.ContentHTML = ""
	unsafeContent := `<script>alert(1)</script>`
	pub.Content = &unsafeContent
	escaped := svc.renderPublicationEmailHTML(pub)
	if strings.Contains(escaped, pub.Title) || strings.Contains(escaped, unsafeContent) {
		t.Fatal("email title and plain content must be HTML-escaped")
	}
}

func TestNWS006BannerActionURLValidation(t *testing.T) {
	for _, valid := range []string{"", "/publications/123", "https://example.com/path"} {
		if !validBannerActionURL(valid) {
			t.Errorf("expected valid URL: %q", valid)
		}
	}
	for _, invalid := range []string{"javascript:alert(1)", "//evil.example", "http://example.com", "/path\\evil", "https://user:pass@example.com"} {
		if validBannerActionURL(invalid) {
			t.Errorf("expected invalid URL: %q", invalid)
		}
	}
}
