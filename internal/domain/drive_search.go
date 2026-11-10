package domain

type DriveSearchQueryRequest struct {
	Question      string   `json:"question" binding:"required,min=1,max=2000"`
	Limit         int      `json:"limit,omitempty" binding:"omitempty,min=1,max=20"`
	ChunksPerFile int      `json:"chunks_per_file,omitempty" binding:"omitempty,min=1,max=8"`
	FileIDs       []string `json:"file_ids,omitempty" binding:"omitempty,max=32,dive,uuid"`
}

type DriveSearchChunk struct {
	Locator       map[string]any `json:"locator,omitempty"`
	ChunkIndex    int            `json:"chunk_index"`
	ChunkCount    int            `json:"chunk_count"`
	SectionIndex  int            `json:"section_index,omitempty"`
	SectionID     string         `json:"section_id,omitempty"`
	ParentContent string         `json:"parent_content,omitempty"`
	Content       string         `json:"content"`
	Highlights    []string       `json:"highlights,omitempty"`
	Score         float64        `json:"score,omitempty"`
	IsContext     bool           `json:"is_context,omitempty"`
}

type DriveSearchSource struct {
	FileID       string             `json:"file_id"`
	FileName     string             `json:"file_name"`
	FolderPath   string             `json:"folder_path,omitempty"`
	UpdatedAt    string             `json:"updated_at,omitempty"`
	Score        float64            `json:"score,omitempty"`
	ChunkIndices []int              `json:"chunk_indices,omitempty"`
	Chunks       []DriveSearchChunk `json:"chunks"`
}

type DriveSearchQueryResponse struct {
	Incomplete bool                `json:"incomplete,omitempty"`
	Indexing   bool                `json:"indexing,omitempty"`
	Warnings   []string            `json:"warnings,omitempty"`
	Question   string              `json:"question"`
	TotalFiles int                 `json:"total_files"`
	Sources    []DriveSearchSource `json:"sources"`
}
