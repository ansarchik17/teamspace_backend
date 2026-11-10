package domain

import (
	"time"

	"github.com/google/uuid"
)

type ExternalPublicationImport struct {
	ID            uuid.UUID `db:"id"`
	SourceName    string    `db:"source_name"`
	ExternalID    string    `db:"external_id"`
	SourceURL     string    `db:"source_url"`
	PublicationID uuid.UUID `db:"publication_id"`
	Title         string    `db:"title"`
	Checksum      string    `db:"checksum"`
	ImportedAt    time.Time `db:"imported_at"`
	UpdatedAt     time.Time `db:"updated_at"`
}
