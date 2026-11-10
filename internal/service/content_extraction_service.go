package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/unidoc/unipdf/v3/extractor"
	"github.com/unidoc/unipdf/v3/model"
	"github.com/west6ide/workspace-backend/internal/aiaccess"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/xuri/excelize/v2"
	"go.uber.org/zap"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

func init() {
	// Set metering key here if you have one, otherwise it runs in community/trial mode
	// license.SetMeteringKey("YOUR_KEY")
	// Reduce log level to avoid spamming stdout
	// common.SetLogger(common.NewConsoleLogger(common.LogLevelError))
}

type ContentExtractionService struct {
	logger     *zap.Logger
	cfg        config.DocumentParseConfig
	httpClient *http.Client
	parseSlots chan struct{}
}

var errNoExtractableText = errors.New("no extractable text found")
var errNotebookPDFPassword = errors.New("PDF requires a password")
var errNotebookOCRLimit = errors.New("PDF has too many pages requiring OCR")

func ErrNoExtractableText() error {
	return errNoExtractableText
}

func NewContentExtractionService(cfg config.DocumentParseConfig, logger *zap.Logger) *ContentExtractionService {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &ContentExtractionService{
		logger: logger.With(zap.String("service", "content_extraction")),
		cfg:    cfg,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: aiaccess.BlockedTransport{},
		},
		parseSlots: make(chan struct{}, 2),
	}
}

func (s *ContentExtractionService) ExtractText(file *multipart.FileHeader) (string, error) {
	src, err := file.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	buf, err := io.ReadAll(src)
	if err != nil {
		return "", err
	}

	return s.ExtractTextBytes(file.Filename, buf)
}

func (s *ContentExtractionService) ExtractTextBytes(filename string, buf []byte) (string, error) {
	return s.ExtractTextBytesContext(context.Background(), filename, buf)
}

func (s *ContentExtractionService) ExtractTextBytesContext(ctx context.Context, filename string, buf []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(buf) > 50*1024*1024 {
		return "", fmt.Errorf("document exceeds the 50 MB limit")
	}
	if len(buf) == 0 {
		return "", errNoExtractableText
	}
	select {
	case s.parseSlots <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.parseSlots }()
	if ext := strings.ToLower(filepath.Ext(filename)); ext == ".xls" || ext == ".ppt" {
		convertedName, converted, err := convertNotebookOffice(ctx, ext, buf)
		if err != nil {
			return "", err
		}
		filename, buf = convertedName, converted
	}
	text, err := s.extractTextBytes(ctx, filename, buf)
	if err != nil {
		return "", err
	}
	if len([]rune(text)) > 2_000_000 {
		return "", fmt.Errorf("extracted document text exceeds the safe limit")
	}
	return text, nil
}

func (s *ContentExtractionService) extractTextBytes(ctx context.Context, filename string, buf []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == ".docx" || ext == ".xlsx" || ext == ".xlsm" || ext == ".pptx" {
		if err := validateOfficeArchive(buf); err != nil {
			return "", err
		}
	}

	switch ext {
	case ".txt", ".md", ".csv", ".json":
		text := strings.TrimSpace(s.extractFromTextBytes(buf))
		if text == "" {
			return "", errNoExtractableText
		}
		return text, nil
	case ".xlsx", ".xlsm":
		text, err := s.extractOfficeText(ctx, filename, buf, func() (string, error) {
			return s.extractFromExcel(bytes.NewReader(buf))
		})
		return s.withPlainTextFallback(buf, text, err)
	case ".docx":
		text, err := s.extractOfficeText(ctx, filename, buf, func() (string, error) {
			return s.extractFromDocx(bytes.NewReader(buf))
		})
		return s.withPlainTextFallback(buf, text, err)
	case ".pptx":
		text, err := s.extractOfficeText(ctx, filename, buf, func() (string, error) {
			return s.extractFromPptx(bytes.NewReader(buf))
		})
		return s.withPlainTextFallback(buf, text, err)
	case ".pdf":
		text, err := s.extractPdfText(ctx, filename, buf)
		return s.withPdfTextOnly(text, err)
	default:
		return "", fmt.Errorf("unsupported file format: %s", ext)
	}
}

func validateOfficeArchive(buf []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return fmt.Errorf("invalid office archive: %w", err)
	}
	if len(reader.File) > 500 {
		return fmt.Errorf("office archive contains too many entries")
	}
	var expanded uint64
	for _, item := range reader.File {
		clean := filepath.ToSlash(filepath.Clean(item.Name))
		if strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("office archive contains an unsafe path")
		}
		if item.UncompressedSize64 > 50*1024*1024 || expanded > 200*1024*1024-item.UncompressedSize64 {
			return fmt.Errorf("office archive expands beyond the safe limit")
		}
		expanded += item.UncompressedSize64
	}
	return nil
}

func (s *ContentExtractionService) extractPdfText(ctx context.Context, filename string, buf []byte) (string, error) {
	// Retain page boundaries so a mixed text/scan PDF cannot silently lose the
	// scanned pages. OCR runs locally; a missing remote parser is not a blocker.
	if text, err := extractNotebookPDF(ctx, buf); err == nil {
		pages := strings.Split(text, "\f")
		if len(pages) > 1 && strings.TrimSpace(pages[len(pages)-1]) == "" {
			pages = pages[:len(pages)-1]
		}
		missing := false
		for _, page := range pages {
			missing = missing || strings.TrimSpace(page) == ""
		}
		if !missing {
			return notebookPDFPageText(pages, make([]bool, len(pages)))
		}
		if result, ocrErr := extractNotebookPDFOCR(ctx, buf, pages); ocrErr == nil {
			return result, nil
		} else if errors.Is(ocrErr, errNotebookOCRLimit) {
			return "", ocrErr
		}
	} else if errors.Is(err, errNotebookPDFPassword) {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(s.cfg.URL) != "" {
		if text, err := s.extractViaDocumentParse(ctx, filename, buf); err == nil && strings.TrimSpace(text) != "" {
			return text, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.extractFromPdf(bytes.NewReader(buf))
}

func (s *ContentExtractionService) extractOfficeText(ctx context.Context, filename string, buf []byte, fallback func() (string, error)) (string, error) {
	// Native OOXML extraction keeps all cells, pages and slides. Partitioning a
	// spreadsheet into inferred tables otherwise loses notes below the table.
	if text, err := fallback(); err == nil && strings.TrimSpace(text) != "" {
		return text, nil
	}
	if strings.TrimSpace(s.cfg.URL) != "" {
		text, err := s.extractViaDocumentParse(ctx, filename, buf)
		if strings.TrimSpace(text) != "" {
			return text, nil
		}
		if err != nil {
			s.logger.Warn("document parse failed for office file, falling back to legacy extractor",
				zap.String("filename", filename),
				zap.Error(err),
			)
		}
	}
	return fallback()
}

func (s *ContentExtractionService) extractFromTextBytes(buf []byte) string {
	if len(buf) == 0 {
		return ""
	}

	if utf8.Valid(buf) {
		return string(buf)
	}

	decoder := charmap.Windows1251.NewDecoder()
	decoded, _, err := transform.Bytes(decoder, buf)
	if err == nil && len(decoded) > 0 {
		return string(decoded)
	}

	return string(buf)
}

func (s *ContentExtractionService) withPlainTextFallback(buf []byte, text string, err error) (string, error) {
	if normalized := strings.TrimSpace(text); normalized != "" {
		return text, nil
	}
	if fallback, ok := s.bestEffortPlainText(buf); ok {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	return "", errNoExtractableText
}

func (s *ContentExtractionService) withPdfTextOnly(text string, err error) (string, error) {
	if normalized := strings.TrimSpace(text); normalized != "" {
		return text, nil
	}
	if err != nil {
		return "", err
	}
	return "", errNoExtractableText
}

func (s *ContentExtractionService) extractViaDocumentParse(ctx context.Context, filename string, buf []byte) (string, error) {
	if _, blocked := s.httpClient.Transport.(aiaccess.BlockedTransport); blocked {
		return "", aiaccess.ContractUnavailable()
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create multipart file: %w", err)
	}
	if _, err := part.Write(buf); err != nil {
		return "", fmt.Errorf("write multipart file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(s.cfg.URL), &body)
	if err != nil {
		return "", fmt.Errorf("create document parse request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call document parse service: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read document parse response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("document parse service returned status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var payload struct {
		Chunks []struct {
			Text string `json:"text"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return "", fmt.Errorf("decode document parse response: %w", err)
	}

	parts := make([]string, 0, len(payload.Chunks))
	for _, chunk := range payload.Chunks {
		text := strings.TrimSpace(chunk.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func (s *ContentExtractionService) bestEffortPlainText(buf []byte) (string, bool) {
	text := strings.TrimSpace(s.extractFromTextBytes(buf))
	if !looksLikeMeaningfulText(text) {
		return "", false
	}
	return text, true
}

func looksLikeMeaningfulText(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}

	lettersOrDigits := 0
	printable := 0
	for _, r := range text {
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			printable++
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			lettersOrDigits++
		}
	}

	if printable == 0 || lettersOrDigits < 8 {
		return false
	}

	return float64(lettersOrDigits)/float64(printable) >= 0.2
}

func (s *ContentExtractionService) extractFromExcel(r io.Reader) (string, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var sb strings.Builder

	for _, sheet := range f.GetSheetList() {
		sb.WriteString(fmt.Sprintf("--- Sheet: %s ---\n", sheet))
		rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
		if err != nil {
			continue
		}
		for rowIndex, row := range rows {
			for i, colCell := range row {
				if strings.TrimSpace(colCell) == "" {
					continue
				}
				cell, _ := excelize.CoordinatesToCellName(i+1, rowIndex+1)
				fmt.Fprintf(&sb, "%s!%s: %s\n", sheet, cell, colCell)
				// Preserve exact numeric values and the displayed date/currency format.
				if display, displayErr := f.GetCellValue(sheet, cell); displayErr == nil && display != colCell {
					fmt.Fprintf(&sb, "  Отображение: %s\n", display)
				}
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func extractNotebookPDF(ctx context.Context, buf []byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", "-enc", "UTF-8", "-", "-")
	cmd.Stdin = bytes.NewReader(buf)
	output, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.Contains(strings.ToLower(string(exit.Stderr)), "password") {
			return "", errNotebookPDFPassword
		}
		return "", fmt.Errorf("PDF text extraction failed: %w", err)
	}
	return string(output), nil
}

func extractNotebookPDFOCR(ctx context.Context, buf []byte, pages []string) (string, error) {
	missing := 0
	for _, page := range pages {
		if strings.TrimSpace(page) == "" {
			missing++
		}
	}
	// Bound rasterization and CPU work. Never present a truncated scan as full.
	if missing > 32 {
		return "", errNotebookOCRLimit
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "notebook-ocr-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "source.pdf")
	if err := os.WriteFile(input, buf, 0600); err != nil {
		return "", err
	}
	result := append([]string(nil), pages...)
	ocrPages := make([]bool, len(pages))
	for i, page := range pages {
		if strings.TrimSpace(page) != "" {
			continue
		}
		prefix := filepath.Join(dir, fmt.Sprintf("page-%d", i+1))
		cmd := exec.CommandContext(ctx, "pdftoppm", "-f", fmt.Sprint(i+1), "-l", fmt.Sprint(i+1), "-singlefile", "-scale-to", "2000", "-gray", "-png", input, prefix)
		if _, err := cmd.Output(); err != nil {
			return "", fmt.Errorf("PDF page rendering failed: %w", err)
		}
		img := prefix + ".png"
		info, err := os.Stat(img)
		if err != nil || info.Size() > 20*1024*1024 {
			return "", fmt.Errorf("OCR image is unavailable or exceeds the safe limit")
		}
		cmd = exec.CommandContext(ctx, "tesseract", img, "stdout", "-l", "rus+eng", "--psm", "3")
		// Keep OCR CPU use bounded along with the shared extraction semaphore.
		cmd.Env = append(os.Environ(), "OMP_THREAD_LIMIT=1")
		text, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("PDF OCR failed: %w", err)
		}
		result[i] = strings.TrimSpace(string(text))
		ocrPages[i] = true
		_ = os.Remove(img)
	}
	return notebookPDFPageText(result, ocrPages)
}

// Keep actual extraction provenance and page boundaries alongside the text.
// Headers describe our extraction method, not a guess about the visual layout.
// Both answer context and citation reads use this same deterministic text.
func notebookPDFPageText(pages []string, ocrPages []bool) (string, error) {
	if len(pages) != len(ocrPages) {
		return "", fmt.Errorf("PDF page provenance does not match page count")
	}
	parts := make([]string, len(pages))
	readable := false
	for i, page := range pages {
		method := "встроенный текст"
		if ocrPages[i] {
			method = "OCR изображения страницы"
		}
		text := strings.TrimSpace(page)
		readable = readable || text != ""
		parts[i] = fmt.Sprintf("--- PDF: страница %d; способ извлечения: %s ---\n%s", i+1, method, text)
	}
	if !readable {
		return "", errNoExtractableText
	}
	return strings.Join(parts, "\n\n"), nil
}

func convertNotebookOffice(ctx context.Context, ext string, buf []byte) (string, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "notebook-office-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "source"+ext)
	if err := os.WriteFile(input, buf, 0600); err != nil {
		return "", nil, err
	}
	profile := filepath.Join(dir, "profile")
	if err := os.MkdirAll(filepath.Join(profile, "user"), 0700); err != nil {
		return "", nil, err
	}
	// Use an isolated profile, disable macros and external-link updates. Nothing
	// from the document is passed to a shell or executed as code.
	settings := `<?xml version="1.0"?><oor:items xmlns:oor="http://openoffice.org/2001/registry"><item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="MacroSecurityLevel" oor:op="fuse"><value>3</value></prop></item><item oor:path="/org.openoffice.Office.Calc/Content/Update"><prop oor:name="Link" oor:op="fuse"><value>0</value></prop></item></oor:items>`
	if err := os.WriteFile(filepath.Join(profile, "user", "registrymodifications.xcu"), []byte(settings), 0600); err != nil {
		return "", nil, err
	}
	target := "xlsx"
	if ext == ".ppt" {
		target = "pptx"
	}
	profileURL := (&url.URL{Scheme: "file", Path: profile}).String()
	cmd := exec.CommandContext(ctx, "soffice", "-env:UserInstallation="+profileURL, "--headless", "--nologo", "--nodefault", "--nofirststartwizard", "--convert-to", target, "--outdir", dir, input)
	// su-exec changes UID without replacing a root user's HOME. LibreOffice also
	// accesses its home/cache outside UserInstallation, so give this request its
	// own writable home rather than the container's inherited /root.
	cmd.Env = append(os.Environ(), "HOME="+dir, "XDG_CACHE_HOME="+filepath.Join(dir, "cache"))
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", nil, fmt.Errorf("legacy office conversion failed: %w: %.2000s", err, output)
	}
	converted, err := os.ReadFile(filepath.Join(dir, "source."+target))
	if err != nil {
		return "", nil, fmt.Errorf("legacy office conversion produced no file: %w", err)
	}
	if len(converted) > 50*1024*1024 {
		return "", nil, fmt.Errorf("converted office document exceeds the safe limit")
	}
	return "source." + target, converted, nil
}

func (s *ContentExtractionService) extractFromDocx(r io.Reader) (string, error) {
	// zip.NewReader requires ReaderAt.
	// Since we don't know if r is ReaderAt (it likely is, but interface doesn't guarantee),
	// we'll read to memory. Safe for typical documents.
	buf, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	readerAt := bytes.NewReader(buf)

	zr, err := zip.NewReader(readerAt, int64(len(buf)))
	if err != nil {
		return "", err
	}

	var sb strings.Builder

	// Main content is usually in word/document.xml
	// Headers/Footers are in word/headerX.xml, word/footerX.xml
	// We'll prioritize document.xml

	// Helper to extract text from XML content
	extractXML := func(f *zip.File) error {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		decoder := xml.NewDecoder(rc)
		inText := false
		for {
			t, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			switch token := t.(type) {
			case xml.CharData:
				if inText {
					content := string(token)
					if strings.TrimSpace(content) != "" {
						sb.WriteString(content)
						sb.WriteString(" ")
					}
				}
			case xml.StartElement:
				if token.Name.Local == "t" {
					inText = true
				}
				// Add newline for paragraphs in Word (w:p)
				if token.Name.Local == "p" {
					sb.WriteString("\n")
				}
			case xml.EndElement:
				if token.Name.Local == "t" {
					inText = false
				}
			}
		}
		return nil
	}

	// Find word/document.xml
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			if err := extractXML(f); err != nil {
				return "", err
			}
			break
		}
	}

	return sb.String(), nil
}

func (s *ContentExtractionService) extractFromPptx(r io.Reader) (string, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	readerAt := bytes.NewReader(buf)

	zr, err := zip.NewReader(readerAt, int64(len(buf)))
	if err != nil {
		return "", err
	}

	var sb strings.Builder

	// Slides are in ppt/slides/slide1.xml, slide2.xml ...
	// We should sort them naturally if possible.

	var slides []*zip.File
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			slides = append(slides, f)
		}
	}

	// Sort slides by name length then string (to handle slide1, slide10 correctly-ish, or just simple sort)
	// slide1.xml, slide2.xml... slide10.xml. Lexicographical sort puts slide10 before slide2.
	// Let's rely on simple sort for now or better implementation.
	// Actually, we can define a custom sort.
	sort.Slice(slides, func(i, j int) bool {
		// A simple heuristic: shorter length first (slide1 vs slide10), then alphabetical
		if len(slides[i].Name) != len(slides[j].Name) {
			return len(slides[i].Name) < len(slides[j].Name)
		}
		return slides[i].Name < slides[j].Name
	})

	extractXML := func(f *zip.File) error {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		decoder := xml.NewDecoder(rc)
		inText := false
		for {
			t, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			switch token := t.(type) {
			case xml.CharData:
				if inText {
					content := string(token)
					if strings.TrimSpace(content) != "" {
						sb.WriteString(content)
						sb.WriteString(" ")
					}
				}
			case xml.StartElement:
				// in PPTX, text is usually in a:t
				if token.Name.Local == "t" {
					inText = true
				}
				// Paragraphs usually wrapped in a:p
				if token.Name.Local == "p" {
					sb.WriteString("\n")
				}
			case xml.EndElement:
				if token.Name.Local == "t" {
					inText = false
				}
			}
		}
		return nil
	}

	for _, slide := range slides {
		sb.WriteString(fmt.Sprintf("\n--- Slide: %s ---\n", filepath.Base(slide.Name)))
		_ = extractXML(slide)
	}

	return sb.String(), nil
}

func (s *ContentExtractionService) extractFromPdf(r io.Reader) (string, error) {
	// unidoc requires a ReadSeeker usually, but NewPdfReader works with ReaderAt
	// We load to memory as before
	buf, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	readerAt := bytes.NewReader(buf)

	pdfReader, err := model.NewPdfReader(readerAt)
	if err != nil {
		return "", err
	}

	numPages, err := pdfReader.GetNumPages()
	if err != nil {
		return "", err
	}

	var sb strings.Builder

	for i := 1; i <= numPages; i++ {
		page, err := pdfReader.GetPage(i)
		if err != nil {
			s.logger.Warn("Failed to get page", zap.Int("page_num", i), zap.Error(err))
			continue
		}

		ex, err := extractor.New(page)
		if err != nil {
			s.logger.Warn("Failed to create extractor", zap.Int("page_num", i), zap.Error(err))
			continue
		}

		text, err := ex.ExtractText()
		if err != nil {
			s.logger.Warn("Failed to extract text from page", zap.Int("page_num", i), zap.Error(err))
			continue
		}

		sb.WriteString(text)
		sb.WriteString("\n\n")
	}

	return sb.String(), nil
}
