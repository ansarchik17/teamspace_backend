package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// ---------------------------------------------------------------------------
// ContentDoc.Validate()
// ---------------------------------------------------------------------------

func TestContentDocValidate_Paragraph_OK(t *testing.T) {
	doc := &domain.ContentDoc{
		Type:    "doc",
		Version: 1,
		Blocks: []domain.ContentBlock{
			{
				ID:   "b1",
				Type: domain.NoteBlockTypeParagraph,
				Runs: []domain.ContentRun{
					{Text: "Hello "},
					{Text: "world", Marks: []string{domain.NoteMarkBold}},
					{Text: " nice", Marks: []string{domain.NoteMarkItalic}},
				},
			},
		},
	}
	assert.NoError(t, doc.Validate())
}

func TestContentDocValidate_Paragraph_UnknownMark(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{
				ID:   "b1",
				Type: domain.NoteBlockTypeParagraph,
				Runs: []domain.ContentRun{
					{Text: "oops", Marks: []string{"underline"}},
				},
			},
		},
	}
	err := doc.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown mark")
	var validationErr *domain.ContentValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, domain.ContentValidationInvalidMark, validationErr.Code)
}

func TestContentDocValidate_Paragraph_EmptyRuns(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{ID: "b1", Type: domain.NoteBlockTypeParagraph},
		},
	}
	err := doc.Validate()
	require.Error(t, err)
	var validationErr *domain.ContentValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, domain.ContentValidationInvalidParagraphRuns, validationErr.Code)
	assert.Contains(t, err.Error(), "at least one run")
}

func TestContentDocValidate_Paragraph_DuplicateMarks(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{
				ID:   "b1",
				Type: domain.NoteBlockTypeParagraph,
				Runs: []domain.ContentRun{{Text: "x", Marks: []string{domain.NoteMarkBold, domain.NoteMarkBold}}},
			},
		},
	}
	err := doc.Validate()
	require.Error(t, err)
	var validationErr *domain.ContentValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, domain.ContentValidationInvalidParagraphRuns, validationErr.Code)
	assert.Contains(t, err.Error(), "duplicate mark")
}

func TestContentDocValidate_Checklist_OK(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{
				ID:   "b2",
				Type: domain.NoteBlockTypeChecklist,
				Items: []domain.ChecklistBlockItem{
					{ID: "i1", Text: "Buy milk", Checked: false},
					{ID: "i2", Text: "Call mom", Checked: true},
				},
			},
		},
	}
	assert.NoError(t, doc.Validate())
}

func TestContentDocValidate_Checklist_ItemTooLong(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{
				ID:    "b2",
				Type:  domain.NoteBlockTypeChecklist,
				Items: []domain.ChecklistBlockItem{{ID: "i1", Text: strings.Repeat("x", 501)}},
			},
		},
	}
	err := doc.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "text exceeds")
}

func TestContentDocValidate_Checklist_ItemExactLimit(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{
				ID:    "b2",
				Type:  domain.NoteBlockTypeChecklist,
				Items: []domain.ChecklistBlockItem{{ID: "i1", Text: strings.Repeat("a", 500)}},
			},
		},
	}
	assert.NoError(t, doc.Validate())
}

func TestContentDocValidate_Table_OK(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{
				ID:   "b3",
				Type: domain.NoteBlockTypeTable,
				Rows: [][]domain.TableCell{
					{{Text: "A1"}, {Text: "B1"}},
					{{Text: "A2"}, {Text: "B2"}},
				},
			},
		},
	}
	assert.NoError(t, doc.Validate())
}

func TestContentDocValidate_Table_Empty(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{ID: "b3", Type: domain.NoteBlockTypeTable, Rows: [][]domain.TableCell{}},
		},
	}
	err := doc.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 1 row")
}

func TestContentDocValidate_Table_TooManyRows(t *testing.T) {
	rows := make([][]domain.TableCell, domain.NoteTableMaxRows+1)
	for i := range rows {
		rows[i] = []domain.TableCell{{Text: "x"}}
	}
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{ID: "b3", Type: domain.NoteBlockTypeTable, Rows: rows},
		},
	}
	err := doc.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds max rows")
}

func TestContentDocValidate_Table_TooManyCols(t *testing.T) {
	cols := make([]domain.TableCell, domain.NoteTableMaxCols+1)
	for i := range cols {
		cols[i] = domain.TableCell{Text: "c"}
	}
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{ID: "b3", Type: domain.NoteBlockTypeTable, Rows: [][]domain.TableCell{cols}},
		},
	}
	err := doc.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "column count")
}

func TestContentDocValidate_Table_NotRectangular(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{
				ID:   "b3",
				Type: domain.NoteBlockTypeTable,
				Rows: [][]domain.TableCell{
					{{Text: "A1"}, {Text: "B1"}},
					{{Text: "A2"}},
				},
			},
		},
	}
	err := doc.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rectangular")
}

func TestContentDocValidate_Image_OK(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{ID: "b4", Type: domain.NoteBlockTypeImage, AttachmentID: "some-uuid", Alt: "receipt"},
		},
	}
	assert.NoError(t, doc.Validate())
}

func TestContentDocValidate_Image_MissingAttachmentID(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{ID: "b4", Type: domain.NoteBlockTypeImage},
		},
	}
	err := doc.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing attachmentId")
}

func TestContentDocValidate_UnknownBlockType(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{ID: "b99", Type: "video"},
		},
	}
	err := doc.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown block type")
}

func TestContentDocValidate_NilDoc(t *testing.T) {
	var doc *domain.ContentDoc
	assert.NoError(t, doc.Validate())
}

func TestContentDocValidate_MixedBlocks_OK(t *testing.T) {
	doc := &domain.ContentDoc{
		Type:    "doc",
		Version: 1,
		Blocks: []domain.ContentBlock{
			{
				ID:   "b1",
				Type: domain.NoteBlockTypeParagraph,
				Runs: []domain.ContentRun{{Text: "Hello"}, {Text: "bold", Marks: []string{domain.NoteMarkBold}}},
			},
			{
				ID:    "b2",
				Type:  domain.NoteBlockTypeChecklist,
				Items: []domain.ChecklistBlockItem{{ID: "i1", Text: "Task 1", Checked: false}},
			},
			{
				ID:   "b3",
				Type: domain.NoteBlockTypeTable,
				Rows: [][]domain.TableCell{{{Text: "Cell"}}},
			},
			{ID: "b4", Type: domain.NoteBlockTypeImage, AttachmentID: "att-1"},
		},
	}
	assert.NoError(t, doc.Validate())
}

// ---------------------------------------------------------------------------
// ContentDoc.ExtractPlainText()
// ---------------------------------------------------------------------------

func TestExtractPlainText_AllBlockTypes(t *testing.T) {
	doc := &domain.ContentDoc{
		Blocks: []domain.ContentBlock{
			{
				Type: domain.NoteBlockTypeParagraph,
				Runs: []domain.ContentRun{{Text: "Hello"}, {Text: "World"}},
			},
			{
				Type:  domain.NoteBlockTypeChecklist,
				Items: []domain.ChecklistBlockItem{{Text: "Buy eggs"}, {Text: "Call dad"}},
			},
			{
				Type: domain.NoteBlockTypeTable,
				Rows: [][]domain.TableCell{
					{{Text: "Name"}, {Text: "Age"}},
					{{Text: "Alice"}, {Text: "30"}},
				},
			},
			{
				// image blocks don't contribute text
				Type:         domain.NoteBlockTypeImage,
				AttachmentID: "att-1",
			},
		},
	}

	text := doc.ExtractPlainText()
	assert.Contains(t, text, "Hello")
	assert.Contains(t, text, "World")
	assert.Contains(t, text, "Buy eggs")
	assert.Contains(t, text, "Call dad")
	assert.Contains(t, text, "Name")
	assert.Contains(t, text, "Age")
	assert.Contains(t, text, "Alice")
	assert.Contains(t, text, "30")
}

func TestExtractPlainText_NilDoc(t *testing.T) {
	var doc *domain.ContentDoc
	assert.Equal(t, "", doc.ExtractPlainText())
}

func TestExtractPlainText_Empty(t *testing.T) {
	doc := &domain.ContentDoc{Blocks: []domain.ContentBlock{}}
	assert.Equal(t, "", doc.ExtractPlainText())
}

// ---------------------------------------------------------------------------
// Marshal / Unmarshal round-trip
// ---------------------------------------------------------------------------

func TestMarshalUnmarshalContentDoc_RoundTrip(t *testing.T) {
	original := &domain.ContentDoc{
		Type:    "doc",
		Version: 1,
		Blocks: []domain.ContentBlock{
			{
				ID:   "b1",
				Type: domain.NoteBlockTypeParagraph,
				Runs: []domain.ContentRun{
					{Text: "hello", Marks: []string{domain.NoteMarkBold}},
				},
			},
			{
				ID:   "b2",
				Type: domain.NoteBlockTypeChecklist,
				Items: []domain.ChecklistBlockItem{
					{ID: "i1", Text: "item", Checked: true},
				},
			},
			{
				ID:   "b3",
				Type: domain.NoteBlockTypeTable,
				Rows: [][]domain.TableCell{{{Text: "A"}}, {{Text: "B"}}},
			},
			{
				ID:           "b4",
				Type:         domain.NoteBlockTypeImage,
				AttachmentID: "att-uuid",
				Alt:          "photo",
			},
		},
	}

	data, err := domain.MarshalContentDoc(original)
	require.NoError(t, err)
	assert.True(t, json.Valid(data))

	restored, err := domain.UnmarshalContentDoc(data)
	require.NoError(t, err)
	require.NotNil(t, restored)

	assert.Equal(t, original.Type, restored.Type)
	assert.Equal(t, original.Version, restored.Version)
	assert.Len(t, restored.Blocks, 4)

	// paragraph
	assert.Equal(t, "b1", restored.Blocks[0].ID)
	assert.Equal(t, domain.NoteMarkBold, restored.Blocks[0].Runs[0].Marks[0])

	// checklist
	assert.Equal(t, "i1", restored.Blocks[1].Items[0].ID)
	assert.True(t, restored.Blocks[1].Items[0].Checked)

	// table
	assert.Equal(t, "A", restored.Blocks[2].Rows[0][0].Text)

	// image
	assert.Equal(t, "att-uuid", restored.Blocks[3].AttachmentID)
	assert.Equal(t, "photo", restored.Blocks[3].Alt)
}

func TestMarshalUnmarshalContentDoc_MixedParagraphRunsPreserved(t *testing.T) {
	original := &domain.ContentDoc{
		Type:    "doc",
		Version: 1,
		Blocks: []domain.ContentBlock{
			{
				ID:   "p1",
				Type: domain.NoteBlockTypeParagraph,
				Runs: []domain.ContentRun{
					{Text: "plain"},
					{Text: "bold", Marks: []string{domain.NoteMarkBold}},
					{Text: "italic", Marks: []string{domain.NoteMarkItalic}},
					{Text: "both", Marks: []string{domain.NoteMarkBold, domain.NoteMarkItalic}},
				},
			},
		},
	}

	data, err := domain.MarshalContentDoc(original)
	require.NoError(t, err)
	restored, err := domain.UnmarshalContentDoc(data)
	require.NoError(t, err)
	require.NotNil(t, restored)
	require.Len(t, restored.Blocks, 1)
	require.Len(t, restored.Blocks[0].Runs, 4)
	assert.Equal(t, original.Blocks[0].Runs, restored.Blocks[0].Runs)
}

func TestMarshalContentDoc_Nil(t *testing.T) {
	data, err := domain.MarshalContentDoc(nil)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(data))
}

func TestUnmarshalContentDoc_Empty(t *testing.T) {
	doc, err := domain.UnmarshalContentDoc([]byte("{}"))
	require.NoError(t, err)
	assert.Nil(t, doc)
}

func TestUnmarshalContentDoc_Null(t *testing.T) {
	doc, err := domain.UnmarshalContentDoc([]byte("null"))
	require.NoError(t, err)
	assert.Nil(t, doc)
}

func TestUnmarshalContentDoc_InvalidJSON(t *testing.T) {
	_, err := domain.UnmarshalContentDoc([]byte("{not valid json"))
	require.Error(t, err)
}
