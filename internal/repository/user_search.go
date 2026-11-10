package repository

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

type userSearchFields struct {
	nameParts []string
	fields    []string
	documents []string
}

func normalizeUserSearchQuery(raw string) string {
	return strings.ToLower(strings.Join(strings.FieldsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || isSearchWordSeparator(r)
	}), " "))
}

func isSearchWordSeparator(r rune) bool {
	switch r {
	case '-', '‐', '‑', '‒', '–', '—', '―':
		return true
	default:
		return false
	}
}

func buildUserSearchCondition(rawQuery string, startIndex int, searchFields userSearchFields) (string, []any, bool) {
	normalizedQuery := normalizeUserSearchQuery(rawQuery)
	if normalizedQuery == "" {
		return "", nil, false
	}

	expressions := buildUserSearchExpressions(searchFields)
	if len(expressions) == 0 {
		return "", nil, false
	}

	tokens := strings.Fields(normalizedQuery)
	clauses := make([]string, 0, len(tokens))
	args := make([]any, 0, len(tokens))

	for i, token := range tokens {
		placeholder := startIndex + i
		likes := make([]string, 0, len(expressions))
		for _, expr := range expressions {
			likes = append(likes, fmt.Sprintf("%s LIKE $%d", expr, placeholder))
		}
		clauses = append(clauses, "("+strings.Join(likes, " OR ")+")")
		args = append(args, "%"+token+"%")
	}

	return "(" + strings.Join(clauses, " AND ") + ")", args, true
}

func buildUserSearchExpressions(searchFields userSearchFields) []string {
	expressions := make([]string, 0, len(searchFields.documents)+len(searchFields.nameParts)+len(searchFields.fields)+2)

	for _, document := range searchFields.documents {
		expressions = append(expressions, normalizedSearchExpression(document))
	}

	if len(searchFields.nameParts) > 0 {
		expressions = append(expressions, normalizedSearchExpression(buildNameSearchExpression(searchFields.nameParts)))

		reversed := slices.Clone(searchFields.nameParts)
		slices.Reverse(reversed)
		if !slices.Equal(reversed, searchFields.nameParts) {
			expressions = append(expressions, normalizedSearchExpression(buildNameSearchExpression(reversed)))
		}
	}

	for _, field := range append(searchFields.fields, searchFields.nameParts...) {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		expressions = append(expressions, normalizedSearchExpression(field))
	}

	return compactSearchExpressions(expressions)
}

func buildNameSearchExpression(parts []string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}

	if len(filtered) == 0 {
		return ""
	}

	return "concat_ws(' ', " + strings.Join(filtered, ", ") + ")"
}

func normalizedSearchExpression(expr string) string {
	return fmt.Sprintf("LOWER(regexp_replace(TRIM(regexp_replace(%s, '[‐‑‒–—―-]+', ' ', 'g')), '\\s+', ' ', 'g'))", expr)
}

func compactSearchExpressions(expressions []string) []string {
	result := make([]string, 0, len(expressions))
	seen := make(map[string]struct{}, len(expressions))

	for _, expr := range expressions {
		expr = strings.TrimSpace(expr)
		if expr == "" {
			continue
		}
		if _, ok := seen[expr]; ok {
			continue
		}
		seen[expr] = struct{}{}
		result = append(result, expr)
	}

	return result
}
