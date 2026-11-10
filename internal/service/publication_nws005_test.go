package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestNWS005_ModerationConstants(t *testing.T) {
	if domain.CommentStatusActive != "active" {
		t.Errorf("expected active, got %s", domain.CommentStatusActive)
	}
	if domain.CommentStatusHidden != "hidden" {
		t.Errorf("expected hidden, got %s", domain.CommentStatusHidden)
	}
	if domain.CommentStatusDeletedByModerator != "deleted_by_moderator" {
		t.Errorf("expected deleted_by_moderator, got %s", domain.CommentStatusDeletedByModerator)
	}
	if domain.ModerationActionHide != "hide" {
		t.Errorf("expected hide, got %s", domain.ModerationActionHide)
	}
	if domain.ModerationActionRestore != "restore" {
		t.Errorf("expected restore, got %s", domain.ModerationActionRestore)
	}
	if domain.ModerationActionDelete != "delete" {
		t.Errorf("expected delete, got %s", domain.ModerationActionDelete)
	}
}

func TestNWS005_ModerationPermissions(t *testing.T) {
	svc := &PublicationService{}

	authorID := int64(100)
	otherUserID := int64(200)

	pub := &domain.Publication{
		ID:       uuid.New(),
		AuthorID: authorID,
	}

	superAdmin := &domain.User{ID: 1, Role: domain.RoleSuperAdmin}
	manager := &domain.User{ID: 2, Role: domain.RoleManager}
	author := &domain.User{ID: authorID, Role: domain.RoleUser}
	regularUser := &domain.User{ID: otherUserID, Role: domain.RoleUser}

	if !svc.isModeratorOrAuthor(pub, superAdmin) {
		t.Error("SuperAdmin must be moderator")
	}
	if !svc.isModeratorOrAuthor(pub, manager) {
		t.Error("Manager must be moderator")
	}
	if !svc.isModeratorOrAuthor(pub, author) {
		t.Error("Publication author must be moderator")
	}
	if svc.isModeratorOrAuthor(pub, regularUser) {
		t.Error("Regular user must NOT be moderator")
	}
	if svc.isModeratorOrAuthor(pub, nil) {
		t.Error("nil user must NOT be moderator")
	}
}

func TestNWS005_CommentHierarchyAndMasking(t *testing.T) {
	pubID := uuid.New()
	parentID := uuid.New()
	childID := uuid.New()
	reason := "Spam"

	// Root comment
	root := domain.CommentResponse{
		ID:            parentID,
		PublicationID: pubID,
		Content:       "Отличная новость!",
		Author: domain.UserMini{
			ID:        10,
			FirstName: "Иван",
			LastName:  "Иванов",
		},
		RepliesCount: 1,
		Replies: []domain.CommentResponse{
			{
				ID:               childID,
				PublicationID:    pubID,
				ParentID:         &parentID,
				Content:          "[Комментарий скрыт модератором]",
				IsHidden:         true,
				Status:           domain.CommentStatusHidden,
				ModerationReason: nil, // masked for regular users
				Author: domain.UserMini{
					ID:        20,
					FirstName: "Петр",
					LastName:  "Петров",
				},
				ReplyToUser: &domain.UserMini{
					ID:        10,
					FirstName: "Иван",
					LastName:  "Иванов",
				},
				Reactions: []domain.ReactionSummary{
					{Reaction: "👍", Count: 3, Reacted: true},
					{Reaction: "❤️", Count: 1, Reacted: false},
				},
			},
		},
	}

	data, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("failed to marshal CommentResponse: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	repliesRaw, ok := parsed["replies"].([]interface{})
	if !ok || len(repliesRaw) != 1 {
		t.Fatalf("expected 1 reply in replies array, got %v", parsed["replies"])
	}

	replyMap := repliesRaw[0].(map[string]interface{})
	if replyMap["content"] != "[Комментарий скрыт модератором]" {
		t.Errorf("expected masked content, got %v", replyMap["content"])
	}
	if replyMap["is_hidden"] != true {
		t.Errorf("expected is_hidden true, got %v", replyMap["is_hidden"])
	}
	if replyMap["moderation_reason"] != nil {
		t.Errorf("expected moderation_reason to be nil/omitted, got %v", replyMap["moderation_reason"])
	}

	// Moderator view test: moderation_reason must be visible
	root.Replies[0].Content = "Спам текст"
	root.Replies[0].ModerationReason = &reason

	modData, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("failed to marshal moderator CommentResponse: %v", err)
	}

	var modParsed map[string]interface{}
	if err := json.Unmarshal(modData, &modParsed); err != nil {
		t.Fatalf("failed to unmarshal moderator JSON: %v", err)
	}

	modReplies := modParsed["replies"].([]interface{})
	modReplyMap := modReplies[0].(map[string]interface{})
	if modReplyMap["moderation_reason"] != "Spam" {
		t.Errorf("expected moderation_reason 'Spam', got %v", modReplyMap["moderation_reason"])
	}
}

func TestNWS005_RequestsJSONSerialization(t *testing.T) {
	parentUUID := uuid.New()
	replyToUser := int64(42)

	createJSON := `{
		"content": "Ответ на комментарий",
		"parent_id": "` + parentUUID.String() + `",
		"reply_to_user_id": 42
	}`

	var createReq domain.CreateCommentRequest
	if err := json.Unmarshal([]byte(createJSON), &createReq); err != nil {
		t.Fatalf("failed to unmarshal CreateCommentRequest: %v", err)
	}
	if createReq.Content != "Ответ на комментарий" {
		t.Errorf("expected content, got %s", createReq.Content)
	}
	if createReq.ParentID == nil || *createReq.ParentID != parentUUID {
		t.Errorf("expected parent_id %v, got %v", parentUUID, createReq.ParentID)
	}
	if createReq.ReplyToUserID == nil || *createReq.ReplyToUserID != replyToUser {
		t.Errorf("expected reply_to_user_id 42, got %v", createReq.ReplyToUserID)
	}

	// Moderate comment request
	reason := "Нарушение правил сообщества"
	modJSON := `{
		"action": "hide",
		"reason": "Нарушение правил сообщества"
	}`
	var modReq domain.ModerateCommentRequest
	if err := json.Unmarshal([]byte(modJSON), &modReq); err != nil {
		t.Fatalf("failed to unmarshal ModerateCommentRequest: %v", err)
	}
	if modReq.Action != domain.ModerationActionHide {
		t.Errorf("expected hide, got %s", modReq.Action)
	}
	if modReq.Reason == nil || *modReq.Reason != reason {
		t.Errorf("expected reason %s, got %v", reason, modReq.Reason)
	}

	// Reaction request
	rxJSON := `{"reaction": "🔥"}`
	var rxReq domain.SetReactionRequest
	if err := json.Unmarshal([]byte(rxJSON), &rxReq); err != nil {
		t.Fatalf("failed to unmarshal SetReactionRequest: %v", err)
	}
	if rxReq.Reaction != "🔥" {
		t.Errorf("expected 🔥, got %s", rxReq.Reaction)
	}

	// Lock comments request
	lockReason := "Обсуждение закрыто модератором"
	lockJSON := `{
		"allow_comments": false,
		"reason": "Обсуждение закрыто модератором"
	}`
	var lockReq domain.LockCommentsRequest
	if err := json.Unmarshal([]byte(lockJSON), &lockReq); err != nil {
		t.Fatalf("failed to unmarshal LockCommentsRequest: %v", err)
	}
	if lockReq.AllowComments != false {
		t.Errorf("expected allow_comments false, got %v", lockReq.AllowComments)
	}
	if lockReq.Reason == nil || *lockReq.Reason != lockReason {
		t.Errorf("expected reason %s, got %v", lockReason, lockReq.Reason)
	}
}

func TestNWS005_PublicationReactionsSummary(t *testing.T) {
	pubResp := domain.PublicationResponse{
		ID:         uuid.New(),
		Title:      "Новость с реакциями",
		LikesCount: 5,
		Reactions: []domain.ReactionSummary{
			{
				Reaction: "👍",
				Count:    3,
				Reacted:  true,
				Users: []domain.ReactionUser{
					{ID: 1, FullName: "Алихан Алиханов"},
				},
			},
			{
				Reaction: "🔥",
				Count:    2,
				Reacted:  false,
			},
		},
		UserReactions: []string{"👍"},
	}

	bytes, err := json.Marshal(pubResp)
	if err != nil {
		t.Fatalf("failed to marshal publication response: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal json: %v", err)
	}

	reactions, ok := parsed["reactions"].([]interface{})
	if !ok || len(reactions) != 2 {
		t.Fatalf("expected 2 reaction summaries, got %v", parsed["reactions"])
	}

	userRx, ok := parsed["user_reactions"].([]interface{})
	if !ok || len(userRx) != 1 || userRx[0] != "👍" {
		t.Fatalf("expected user_reactions ['👍'], got %v", parsed["user_reactions"])
	}
}
