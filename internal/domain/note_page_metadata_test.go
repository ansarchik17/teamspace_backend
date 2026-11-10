package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotePageMetadataRoundTrip(t *testing.T) {
	var doc ContentDoc
	require.NoError(t, json.Unmarshal([]byte(`{"type":"doc","version":1,"blocks":[],"page":{"icon":"💡","description":"Описание заметки"}}`), &doc))
	require.NoError(t, doc.Validate())
	require.NotNil(t, doc.Page)
	require.Equal(t, "💡", doc.Page.Icon)
	data, err := MarshalContentDoc(&doc)
	require.NoError(t, err)
	stored, err := UnmarshalContentDoc(data)
	require.NoError(t, err)
	require.Equal(t, doc.Page, stored.Page)
	for _, page := range []*NotePageMetadata{
		{Icon: strings.Repeat("x", 257)},
		{Description: strings.Repeat("x", 16385)},
	} {
		doc.Page = page
		require.Error(t, doc.Validate())
	}
}

func TestNoteLegacyDocumentWithoutPage(t *testing.T) {
	doc, err := UnmarshalContentDoc([]byte(`{"type":"doc","version":1,"blocks":[]}`))
	require.NoError(t, err)
	require.NoError(t, doc.Validate())
	require.Nil(t, doc.Page)
	data, err := MarshalContentDoc(doc)
	require.NoError(t, err)
	require.NotContains(t, string(data), `"page"`)
}
