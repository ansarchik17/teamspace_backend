package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type ProfileSectionRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewProfileSectionRepository(db *pgxpool.Pool, logger *zap.Logger) *ProfileSectionRepository {
	return &ProfileSectionRepository{
		db:     db,
		logger: logger.With(zap.String("repository", "profile_sections")),
	}
}

func (r *ProfileSectionRepository) CreateEducation(ctx context.Context, item *domain.UserEducation) error {
	const query = `
		INSERT INTO user_educations (
			user_id, school_name, school_avatar_url, degree, field_of_study, start_date, end_date, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id
	`
	return r.db.QueryRow(ctx, query,
		item.UserID, item.SchoolName, item.SchoolAvatarURL, item.Degree, item.FieldOfStudy, item.StartDate, item.EndDate, item.CreatedAt, item.UpdatedAt,
	).Scan(&item.ID)
}

func (r *ProfileSectionRepository) ListEducationsByUserID(ctx context.Context, userID int64) ([]*domain.UserEducation, error) {
	const query = `
		SELECT id, user_id, school_name, school_avatar_url, degree, field_of_study, start_date, end_date, created_at, updated_at
		FROM user_educations
		WHERE user_id = $1
		ORDER BY start_date DESC, id DESC
	`
	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list educations: %w", err)
	}
	defer rows.Close()

	var out []*domain.UserEducation
	for rows.Next() {
		item := &domain.UserEducation{}
		if err := rows.Scan(
			&item.ID, &item.UserID, &item.SchoolName, &item.SchoolAvatarURL, &item.Degree, &item.FieldOfStudy,
			&item.StartDate, &item.EndDate, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan education: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate educations: %w", err)
	}
	return out, nil
}

func (r *ProfileSectionRepository) GetEducationByIDForUser(ctx context.Context, educationID, userID int64) (*domain.UserEducation, error) {
	const query = `
		SELECT id, user_id, school_name, school_avatar_url, degree, field_of_study, start_date, end_date, created_at, updated_at
		FROM user_educations
		WHERE id = $1 AND user_id = $2
	`
	item := &domain.UserEducation{}
	err := r.db.QueryRow(ctx, query, educationID, userID).Scan(
		&item.ID, &item.UserID, &item.SchoolName, &item.SchoolAvatarURL, &item.Degree, &item.FieldOfStudy,
		&item.StartDate, &item.EndDate, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("education not found")
		}
		return nil, fmt.Errorf("get education: %w", err)
	}
	return item, nil
}

func (r *ProfileSectionRepository) UpdateEducation(ctx context.Context, item *domain.UserEducation) error {
	const query = `
		UPDATE user_educations
		SET school_name = $1,
		    school_avatar_url = $2,
		    degree = $3,
		    field_of_study = $4,
		    start_date = $5,
		    end_date = $6,
		    updated_at = $7
		WHERE id = $8 AND user_id = $9
	`
	tag, err := r.db.Exec(ctx, query,
		item.SchoolName, item.SchoolAvatarURL, item.Degree, item.FieldOfStudy, item.StartDate, item.EndDate, item.UpdatedAt, item.ID, item.UserID,
	)
	if err != nil {
		return fmt.Errorf("update education: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("education not found")
	}
	return nil
}

func (r *ProfileSectionRepository) DeleteEducation(ctx context.Context, educationID, userID int64) error {
	const query = `DELETE FROM user_educations WHERE id = $1 AND user_id = $2`
	tag, err := r.db.Exec(ctx, query, educationID, userID)
	if err != nil {
		return fmt.Errorf("delete education: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("education not found")
	}
	return nil
}

func (r *ProfileSectionRepository) CreateLicenseCertification(ctx context.Context, item *domain.UserLicenseCertification) error {
	const query = `
		INSERT INTO user_license_certifications (
			user_id, name, issuing_organization, skill_name, avatar_url, issue_date, expiration_date,
			attachment_object_name, attachment_file_name, attachment_content_type, attachment_size_bytes,
			created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id
	`
	return r.db.QueryRow(ctx, query,
		item.UserID, item.Name, item.IssuingOrganization, item.SkillName, item.AvatarURL, item.IssueDate, item.ExpirationDate,
		item.AttachmentObjectName, item.AttachmentFileName, item.AttachmentContentType, item.AttachmentSizeBytes,
		item.CreatedAt, item.UpdatedAt,
	).Scan(&item.ID)
}

func (r *ProfileSectionRepository) ListLicenseCertificationsByUserID(ctx context.Context, userID int64) ([]*domain.UserLicenseCertification, error) {
	const query = `
		SELECT id, user_id, name, issuing_organization, skill_name, avatar_url, issue_date, expiration_date,
		       attachment_object_name, attachment_file_name, attachment_content_type, attachment_size_bytes,
		       created_at, updated_at
		FROM user_license_certifications
		WHERE user_id = $1
		ORDER BY issue_date DESC, id DESC
	`
	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list license certifications: %w", err)
	}
	defer rows.Close()

	var out []*domain.UserLicenseCertification
	for rows.Next() {
		item := &domain.UserLicenseCertification{}
		if err := rows.Scan(
			&item.ID, &item.UserID, &item.Name, &item.IssuingOrganization, &item.SkillName, &item.AvatarURL,
			&item.IssueDate, &item.ExpirationDate, &item.AttachmentObjectName, &item.AttachmentFileName,
			&item.AttachmentContentType, &item.AttachmentSizeBytes, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan license certification: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate license certifications: %w", err)
	}
	return out, nil
}

func (r *ProfileSectionRepository) GetLicenseCertificationByIDForUser(ctx context.Context, itemID, userID int64) (*domain.UserLicenseCertification, error) {
	const query = `
		SELECT id, user_id, name, issuing_organization, skill_name, avatar_url, issue_date, expiration_date,
		       attachment_object_name, attachment_file_name, attachment_content_type, attachment_size_bytes,
		       created_at, updated_at
		FROM user_license_certifications
		WHERE id = $1 AND user_id = $2
	`
	item := &domain.UserLicenseCertification{}
	err := r.db.QueryRow(ctx, query, itemID, userID).Scan(
		&item.ID, &item.UserID, &item.Name, &item.IssuingOrganization, &item.SkillName, &item.AvatarURL,
		&item.IssueDate, &item.ExpirationDate, &item.AttachmentObjectName, &item.AttachmentFileName,
		&item.AttachmentContentType, &item.AttachmentSizeBytes, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("license certification not found")
		}
		return nil, fmt.Errorf("get license certification: %w", err)
	}
	return item, nil
}

func (r *ProfileSectionRepository) UpdateLicenseCertification(ctx context.Context, item *domain.UserLicenseCertification) error {
	const query = `
		UPDATE user_license_certifications
		SET name = $1,
		    issuing_organization = $2,
		    skill_name = $3,
		    avatar_url = $4,
		    issue_date = $5,
		    expiration_date = $6,
		    attachment_object_name = $7,
		    attachment_file_name = $8,
		    attachment_content_type = $9,
		    attachment_size_bytes = $10,
		    updated_at = $11
		WHERE id = $12 AND user_id = $13
	`
	tag, err := r.db.Exec(ctx, query,
		item.Name, item.IssuingOrganization, item.SkillName, item.AvatarURL, item.IssueDate, item.ExpirationDate,
		item.AttachmentObjectName, item.AttachmentFileName, item.AttachmentContentType, item.AttachmentSizeBytes,
		item.UpdatedAt, item.ID, item.UserID,
	)
	if err != nil {
		return fmt.Errorf("update license certification: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("license certification not found")
	}
	return nil
}

func (r *ProfileSectionRepository) DeleteLicenseCertification(ctx context.Context, itemID, userID int64) error {
	const query = `DELETE FROM user_license_certifications WHERE id = $1 AND user_id = $2`
	tag, err := r.db.Exec(ctx, query, itemID, userID)
	if err != nil {
		return fmt.Errorf("delete license certification: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("license certification not found")
	}
	return nil
}
