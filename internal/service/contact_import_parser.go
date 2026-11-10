package service

import (
	"bytes"
	"encoding/csv"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/xuri/excelize/v2"
)

const MaxContactImportBytes = 10 << 20
const MaxContactImportRows = 5000

var contactImportColumns = map[string]bool{"external_id": true, "username": true, "email": true, "first_name": true, "last_name": true, "position": true, "department": true, "manager_username": true, "work_email": true, "phone": true, "is_active": true}

func ParseContactImport(filename string, reader io.Reader) ([]domain.ContactImportRow, []domain.ContactImportIssue, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxContactImportBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) == 0 || len(data) > MaxContactImportBytes {
		return nil, nil, apperrors.Validation("import must be between 1 byte and 10 MiB")
	}
	var records [][]string
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".csv":
		if !utf8.Valid(data) {
			return nil, nil, apperrors.Validation("CSV must use UTF-8")
		}
		data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
		r := csv.NewReader(bytes.NewReader(data))
		r.FieldsPerRecord = -1
		firstLine := strings.SplitN(string(data), "\n", 2)[0]
		if strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
			r.Comma = ';'
		}
		for {
			record, e := r.Read()
			if e == io.EOF {
				break
			}
			if e != nil {
				return nil, nil, apperrors.Validation("invalid CSV syntax")
			}
			if len(records) > MaxContactImportRows {
				return nil, nil, apperrors.Validation("import exceeds 5000 rows")
			}
			records = append(records, record)
		}
	case ".xlsx":
		workbook, e := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: 64 << 20, UnzipXMLSizeLimit: 16 << 20})
		if e != nil {
			return nil, nil, apperrors.Validation("invalid XLSX workbook")
		}
		defer workbook.Close()
		sheets := workbook.GetSheetList()
		if len(sheets) != 1 {
			return nil, nil, apperrors.Validation("XLSX must contain exactly one sheet")
		}
		iterator, e := workbook.Rows(sheets[0])
		if e != nil {
			return nil, nil, apperrors.Validation("invalid XLSX rows")
		}
		defer iterator.Close()
		for iterator.Next() {
			if len(records) > MaxContactImportRows {
				return nil, nil, apperrors.Validation("import exceeds 5000 rows")
			}
			row, e := iterator.Columns()
			if e != nil {
				return nil, nil, apperrors.Validation("invalid XLSX cells")
			}
			if len(row) > len(contactImportColumns) {
				return nil, nil, apperrors.Validation("XLSX has too many columns")
			}
			records = append(records, row)
		}
		if iterator.Error() != nil {
			return nil, nil, apperrors.Validation("invalid XLSX stream")
		}
	default:
		return nil, nil, apperrors.Validation("supported imports: .csv and .xlsx")
	}
	if len(records) < 2 {
		return nil, nil, apperrors.Validation("import requires headers and at least one employee")
	}
	headers := map[string]int{}
	for i, raw := range records[0] {
		name := strings.ToLower(strings.TrimSpace(raw))
		if len(name) > 64 {
			return nil, nil, apperrors.Validation("invalid import header")
		}
		if !contactImportColumns[name] {
			return nil, nil, apperrors.Validation("unknown import column: " + name)
		}
		if _, ok := headers[name]; ok {
			return nil, nil, apperrors.Validation("duplicate import column: " + name)
		}
		headers[name] = i
	}
	for _, required := range []string{"username", "email", "first_name", "last_name"} {
		if _, ok := headers[required]; !ok {
			return nil, nil, apperrors.Validation("missing import column: " + required)
		}
	}
	rows := []domain.ContactImportRow{}
	issues := []domain.ContactImportIssue{}
	for index, record := range records[1:] {
		blank := true
		for _, cell := range record {
			if strings.TrimSpace(cell) != "" {
				blank = false
				break
			}
		}
		if blank {
			continue
		}
		number := index + 2
		if len(record) > len(headers) {
			issues = append(issues, domain.ContactImportIssue{Row: number, Field: "row", Message: "too many cells"})
			continue
		}
		get := func(name string) string {
			pos, ok := headers[name]
			if !ok || pos >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[pos])
		}
		optional := func(name string) *string {
			if _, ok := headers[name]; !ok {
				return nil
			}
			value := get(name)
			return &value
		}
		row := domain.ContactImportRow{Row: number, ExternalID: get("external_id"), Username: get("username"), Email: get("email"), FirstName: get("first_name"), LastName: get("last_name"), Position: optional("position"), Department: optional("department"), ManagerUsername: optional("manager_username"), WorkEmail: optional("work_email"), Phone: optional("phone")}
		if row.ExternalID == "" {
			row.ExternalID = strings.ToLower(row.Username)
		}
		if raw := get("is_active"); raw != "" {
			active, e := strconv.ParseBool(raw)
			if e != nil {
				issues = append(issues, domain.ContactImportIssue{Row: number, Field: "is_active", Message: "use true or false"})
			} else {
				row.IsActive = &active
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, issues, apperrors.Validation("import contains no employees")
	}
	return rows, issues, nil
}

func ValidateContactImport(rows []domain.ContactImportRow) []domain.ContactImportIssue {
	issues := []domain.ContactImportIssue{}
	seenID, seenUsername, seenEmail := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := range rows {
		row := &rows[i]
		row.Email = strings.ToLower(strings.TrimSpace(row.Email))
		add := func(field, message string) {
			issues = append(issues, domain.ContactImportIssue{Row: row.Row, Field: field, Message: message})
		}
		for field, value := range map[string]string{"username": row.Username, "first_name": row.FirstName, "last_name": row.LastName} {
			if value == "" || len([]rune(value)) > 50 || strings.ContainsAny(value, "\x00\r\n") {
				add(field, "required, maximum 50 characters, no control characters")
			}
		}
		if row.ExternalID == "" || len(row.ExternalID) > 512 || strings.ContainsRune(row.ExternalID, 0) {
			add("external_id", "required, maximum 512 bytes")
		}
		if !domain.ValidContactEmail(row.Email) {
			add("email", "invalid email")
		}
		normalized, err := (domain.UpdateContactRequest{WorkEmail: row.WorkEmail, Phone: row.Phone}).Normalize()
		if err != nil {
			add("contacts", "invalid work_email or phone")
		} else {
			row.WorkEmail = normalized.WorkEmail
			row.Phone = normalized.Phone
		}
		for field, value := range map[string]*string{"position": row.Position, "department": row.Department} {
			if value != nil && (len([]rune(*value)) > 255 || strings.ContainsRune(*value, 0)) {
				add(field, "maximum 255 characters, no NUL")
			}
		}
		if row.ManagerUsername != nil && len([]rune(*row.ManagerUsername)) > 50 {
			add("manager_username", "maximum 50 characters")
		}
		for _, pair := range []struct {
			field, key string
			seen       map[string]bool
		}{{"external_id", row.ExternalID, seenID}, {"username", strings.ToLower(row.Username), seenUsername}, {"email", strings.ToLower(row.Email), seenEmail}} {
			if pair.seen[pair.key] {
				add(pair.field, "duplicate in import")
			}
			pair.seen[pair.key] = true
		}
	}
	return issues
}
