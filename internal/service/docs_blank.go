package service

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

// These minimal OOXML packages are blank editor inputs, not zero-byte files.
// No user content is interpolated into XML; filenames live in Drive metadata.
func blankOfficeDocument(format string) ([]byte, error) {
	if format == "xlsx" {
		workbook := excelize.NewFile()
		defer workbook.Close()
		if err := workbook.SetSheetName("Sheet1", "Лист1"); err != nil {
			return nil, err
		}
		data, err := workbook.WriteToBuffer()
		if err != nil {
			return nil, err
		}
		return data.Bytes(), nil
	}
	parts := map[string]string{}
	var main string
	switch format {
	case "docx":
		main = "word/document.xml"
		parts[main] = `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p/><w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134" w:header="708" w:footer="708" w:gutter="0"/></w:sectPr></w:body></w:document>`
		parts["[Content_Types].xml"] = officeContentTypes(`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>`)
	case "pptx":
		main = "ppt/presentation.xml"
		parts[main] = `<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst><p:sldIdLst><p:sldId id="256" r:id="rId2"/></p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`
		tree := `<p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr></p:spTree>`
		namespaces := ` xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
		parts["ppt/slides/slide1.xml"] = `<p:sld` + namespaces + `><p:cSld>` + tree + `</p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
		parts["ppt/slideLayouts/slideLayout1.xml"] = `<p:sldLayout` + namespaces + ` type="blank" preserve="1"><p:cSld name="Blank">` + tree + `</p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`
		parts["ppt/slideMasters/slideMaster1.xml"] = `<p:sldMaster` + namespaces + `><p:cSld>` + tree + `</p:cSld><p:clrMap accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" bg1="lt1" bg2="lt2" folHlink="folHlink" hlink="hlink" tx1="dk1" tx2="dk2"/><p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst><p:txStyles><p:titleStyle/><p:bodyStyle/><p:otherStyle/></p:txStyles></p:sldMaster>`
		parts["ppt/_rels/presentation.xml.rels"] = officeRelationships(officeRelationship("rId1", "slideMaster", "slideMasters/slideMaster1.xml") + officeRelationship("rId2", "slide", "slides/slide1.xml"))
		parts["ppt/slides/_rels/slide1.xml.rels"] = officeRelationships(officeRelationship("rId1", "slideLayout", "../slideLayouts/slideLayout1.xml"))
		parts["ppt/slideLayouts/_rels/slideLayout1.xml.rels"] = officeRelationships(officeRelationship("rId1", "slideMaster", "../slideMasters/slideMaster1.xml"))
		parts["ppt/slideMasters/_rels/slideMaster1.xml.rels"] = officeRelationships(officeRelationship("rId1", "slideLayout", "../slideLayouts/slideLayout1.xml"))
		parts["[Content_Types].xml"] = officeContentTypes(`<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/><Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/><Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>`)
	default:
		return nil, fmt.Errorf("unsupported blank format: %s", format)
	}
	if format == "pptx" {
		// DrawingML themes are shared by SpreadsheetML and PresentationML. Reuse
		// excelize's complete default theme instead of maintaining a partial one.
		workbook, err := blankOfficeDocument("xlsx")
		if err != nil {
			return nil, err
		}
		source, err := zip.NewReader(bytes.NewReader(workbook), int64(len(workbook)))
		if err != nil {
			return nil, err
		}
		for _, file := range source.File {
			if file.Name == "xl/theme/theme1.xml" {
				reader, err := file.Open()
				if err != nil {
					return nil, err
				}
				theme, err := io.ReadAll(reader)
				reader.Close()
				if err != nil {
					return nil, err
				}
				parts["ppt/theme/theme1.xml"] = string(theme)
			}
		}
		if parts["ppt/theme/theme1.xml"] == "" {
			return nil, fmt.Errorf("default theme missing")
		}
		parts["ppt/presProps.xml"] = `<p:presentationPr xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`
		parts["ppt/slideMasters/_rels/slideMaster1.xml.rels"] = officeRelationships(officeRelationship("rId1", "slideLayout", "../slideLayouts/slideLayout1.xml") + officeRelationship("rId2", "theme", "../theme/theme1.xml"))
		parts["ppt/_rels/presentation.xml.rels"] = officeRelationships(officeRelationship("rId1", "slideMaster", "slideMasters/slideMaster1.xml") + officeRelationship("rId2", "slide", "slides/slide1.xml") + officeRelationship("rId3", "presProps", "presProps.xml") + officeRelationship("rId4", "theme", "theme/theme1.xml"))
		parts["[Content_Types].xml"] = strings.Replace(parts["[Content_Types].xml"], "</Types>", `<Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/><Override PartName="/ppt/presProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"/></Types>`, 1)
	}
	parts["_rels/.rels"] = officeRelationships(officeRelationship("rId1", "officeDocument", main))
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	names := make([]string, 0, len(parts))
	for name := range parts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := archive.Create(name)
		if err != nil {
			return nil, err
		}
		xml := parts[name]
		if !strings.HasPrefix(xml, "<?xml") {
			xml = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + xml
		}
		if _, err = entry.Write([]byte(xml)); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func officeContentTypes(overrides string) string {
	return `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>` + overrides + `</Types>`
}
func officeRelationships(entries string) string {
	return `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + entries + `</Relationships>`
}
func officeRelationship(id, kind, target string) string {
	return `<Relationship Id="` + id + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/` + kind + `" Target="` + target + `"/>`
}
