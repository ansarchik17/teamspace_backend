package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"path"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/xuri/excelize/v2"
)

func TestDocumentBlankPackages(t *testing.T) {
	for _, format := range []string{"docx", "xlsx", "pptx"} {
		t.Run(format, func(t *testing.T) {
			data, err := blankOfficeDocument(format)
			require.NoError(t, err)
			archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			require.NoError(t, err)
			parts := map[string][]byte{}
			for _, file := range archive.File {
				reader, err := file.Open()
				require.NoError(t, err)
				parts[file.Name], err = io.ReadAll(reader)
				require.NoError(t, err)
				require.NoError(t, reader.Close())
			}
			require.Contains(t, parts, "[Content_Types].xml")
			require.Contains(t, parts, "_rels/.rels")
			for name, content := range parts {
				if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
					continue
				}
				decoder := xml.NewDecoder(bytes.NewReader(content))
				for {
					_, err := decoder.Token()
					if err == io.EOF {
						break
					}
					require.NoError(t, err, name)
				}
				if strings.HasSuffix(name, ".rels") {
					var rels struct {
						Items []struct {
							Target string `xml:"Target,attr"`
							Mode   string `xml:"TargetMode,attr"`
						} `xml:"Relationship"`
					}
					require.NoError(t, xml.Unmarshal(content, &rels))
					for _, rel := range rels.Items {
						if rel.Mode == "External" {
							continue
						}
						base := path.Dir(path.Dir(name))
						require.Contains(t, parts, path.Clean(path.Join(base, rel.Target)), name)
					}
				}
			}
			switch format {
			case "docx":
				require.Contains(t, string(parts["word/document.xml"]), "<w:p/>")
			case "pptx":
				require.Contains(t, parts, "ppt/theme/theme1.xml")
				require.Contains(t, parts, "ppt/slides/slide1.xml")
				require.Contains(t, parts, "ppt/presProps.xml")
			case "xlsx":
				workbook, err := excelize.OpenReader(bytes.NewReader(data))
				require.NoError(t, err)
				defer workbook.Close()
				require.Equal(t, []string{"Лист1"}, workbook.GetSheetList())
				rows, err := workbook.GetRows("Лист1")
				require.NoError(t, err)
				require.Empty(t, rows)
			}
		})
	}
	_, err := blankOfficeDocument("pdf")
	require.Error(t, err)
}

func TestDocumentNamesAndSelection(t *testing.T) {
	for _, name := range []string{"", "../bad", "x/y", "x\\y", "bad\nname", ".docx", "file.xlsx", strings.Repeat("я", 201)} {
		_, err := documentName(name, "docx")
		require.Error(t, err, name)
	}
	name, err := documentName("  Договор  ", "docx")
	require.NoError(t, err)
	require.Equal(t, "Договор.docx", name)
	name, err = documentName("Отчёт.DOCX", "docx")
	require.NoError(t, err)
	require.Equal(t, "Отчёт.DOCX", name)
	service := &DriveService{}
	id := uuid.New()
	for _, req := range []domain.CreateDocumentRequest{{Name: "empty"}, {Name: "both", Format: "docx", TemplateID: &id}, {Name: "bad", Format: "pdf"}, {Name: "file.xlsx", Format: "docx"}} {
		_, err := service.CreateDocument(context.Background(), 1, req)
		require.Error(t, err)
	}
	_, err = service.CreateDocument(context.Background(), 0, domain.CreateDocumentRequest{Format: "docx", Name: "test"})
	require.Error(t, err)
}
