package repository

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"time"
)

type AIChatSection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Color       string    `json:"color"`
	ChatCount   int64     `json:"chat_count"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type AIChatSectionInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Color       *string `json:"color"`
}
type AIChatPatch struct {
	Title            *string
	Pinned, Archived *bool
	SectionSet       bool
	SectionID        *string
}
type AIOrganizedChat struct {
	AIConversation
	Pinned        bool       `json:"pinned"`
	Archived      bool       `json:"archived"`
	ArchivedAt    *time.Time `json:"archived_at"`
	SectionID     *string    `json:"section_id"`
	Preview       *string    `json:"preview"`
	LastMessageAt *time.Time `json:"last_message_at"`
}

const organizedChatColumns = "c.id,c.user_id,c.title,c.model_name,c.created_at,c.updated_at,c.pinned,c.archived,c.archived_at,c.section_id"

func scanOrganizedChat(row pgx.Row) (AIOrganizedChat, error) {
	var c AIOrganizedChat
	err := row.Scan(&c.ID, &c.UserID, &c.Title, &c.ModelName, &c.CreatedAt, &c.UpdatedAt, &c.Pinned, &c.Archived, &c.ArchivedAt, &c.SectionID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = apperrors.NotFound("AI chat")
	}
	return c, err
}
func (r *AIRepository) ListOrganizedChats(ctx context.Context, user int64, includeArchived bool) ([]AIOrganizedChat, error) {
	rows, err := r.db.Query(ctx, `SELECT `+organizedChatColumns+`,m.content,m.created_at FROM ai_conversations c
 LEFT JOIN LATERAL(SELECT content,created_at FROM ai_messages WHERE conversation_id=c.id ORDER BY created_at DESC,id DESC LIMIT 1)m ON true
 WHERE c.user_id=$1 AND ($2 OR NOT c.archived) ORDER BY c.pinned DESC,c.updated_at DESC,c.id`, user, includeArchived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIOrganizedChat{}
	for rows.Next() {
		var c AIOrganizedChat
		err = rows.Scan(&c.ID, &c.UserID, &c.Title, &c.ModelName, &c.CreatedAt, &c.UpdatedAt, &c.Pinned, &c.Archived, &c.ArchivedAt, &c.SectionID, &c.Preview, &c.LastMessageAt)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (r *AIRepository) PatchOrganizedChat(ctx context.Context, user int64, id string, p AIChatPatch) (AIOrganizedChat, error) {
	if p.SectionID != nil {
		var own bool
		err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ai_chat_sections WHERE id=$1 AND user_id=$2)`, *p.SectionID, user).Scan(&own)
		if err != nil {
			return AIOrganizedChat{}, err
		}
		if !own {
			return AIOrganizedChat{}, apperrors.NotFound("AI section")
		}
	}
	chat, err := scanOrganizedChat(r.db.QueryRow(ctx, `UPDATE ai_conversations c SET title=COALESCE($3,title),pinned=COALESCE($4,pinned),
 archived=COALESCE($5,archived),archived_at=CASE WHEN $5::boolean IS NULL THEN archived_at WHEN $5 THEN COALESCE(archived_at,now()) ELSE NULL END,
 section_id=CASE WHEN $6 THEN $7::uuid ELSE section_id END,updated_at=now() WHERE c.id=$1 AND c.user_id=$2 RETURNING `+organizedChatColumns, id, user, p.Title, p.Pinned, p.Archived, p.SectionSet, p.SectionID))
	return chat, aiChatSectionWriteError(err)
}
func (r *AIRepository) CreateConversationInSection(ctx context.Context, user int64, model, title string, section *string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `INSERT INTO ai_conversations(user_id,model_name,title,section_id)
 SELECT $1,$2,$3,$4::uuid WHERE $4::uuid IS NULL OR EXISTS(SELECT 1 FROM ai_chat_sections WHERE id=$4 AND user_id=$1) RETURNING id`, user, model, title, section).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = apperrors.NotFound("AI section")
	}
	return id, aiChatSectionWriteError(err)
}
func (r *AIRepository) ListChatSections(ctx context.Context, user int64) ([]AIChatSection, error) {
	rows, err := r.db.Query(ctx, `SELECT s.id,s.name,s.description,s.color,count(c.id),s.updated_at FROM ai_chat_sections s
 LEFT JOIN ai_conversations c ON c.section_id=s.id AND c.user_id=s.user_id AND NOT c.archived
 WHERE s.user_id=$1 GROUP BY s.id ORDER BY s.created_at,s.id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIChatSection{}
	for rows.Next() {
		var s AIChatSection
		err = rows.Scan(&s.ID, &s.Name, &s.Description, &s.Color, &s.ChatCount, &s.UpdatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (r *AIRepository) SaveChatSection(ctx context.Context, user int64, id string, p AIChatSectionInput) (AIChatSection, error) {
	var s AIChatSection
	var row pgx.Row
	if id == "" {
		row = r.db.QueryRow(ctx, `INSERT INTO ai_chat_sections(user_id,name,description,color) VALUES($1,$2,COALESCE($3,''),COALESCE($4,'#2458D3')) RETURNING id,name,description,color,0::bigint,updated_at`, user, p.Name, p.Description, p.Color)
	} else {
		row = r.db.QueryRow(ctx, `UPDATE ai_chat_sections SET name=COALESCE($3,name),description=COALESCE($4,description),color=COALESCE($5,color),updated_at=now() WHERE id=$1 AND user_id=$2 RETURNING id,name,description,color,(SELECT count(*) FROM ai_conversations WHERE section_id=$1 AND user_id=$2 AND NOT archived),updated_at`, id, user, p.Name, p.Description, p.Color)
	}
	err := row.Scan(&s.ID, &s.Name, &s.Description, &s.Color, &s.ChatCount, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = apperrors.NotFound("AI section")
	}
	return s, err
}
func (r *AIRepository) DeleteChatSection(ctx context.Context, user int64, id string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM ai_chat_sections WHERE id=$1 AND user_id=$2`, id, user)
	if err == nil && tag.RowsAffected() == 0 {
		return apperrors.NotFound("AI section")
	}
	return err
}

func aiChatSectionWriteError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23503" && pg.ConstraintName == "ai_chat_section_owner" {
		return apperrors.NotFound("AI section")
	}
	return err
}
