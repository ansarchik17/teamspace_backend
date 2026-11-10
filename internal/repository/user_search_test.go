package repository

import (
	"strings"
	"testing"
)

func TestNormalizeUserSearchQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "first last", input: "Тамерлан Картаев", want: "тамерлан картаев"},
		{name: "last first", input: "Картаев Тамерлан", want: "картаев тамерлан"},
		{name: "extra spaces", input: "  Картаев   Тамерлан  ", want: "картаев тамерлан"},
		{name: "hyphens become word separators", input: "Куанышбек—Есекеев", want: "куанышбек есекеев"},
		{name: "mixed case", input: "ТаМеРлАн", want: "тамерлан"},
		{name: "empty", input: "   ", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeUserSearchQuery(tt.input); got != tt.want {
				t.Fatalf("normalizeUserSearchQuery(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildUserSearchCondition(t *testing.T) {
	condition, args, ok := buildUserSearchCondition("  Картаев   Тамерлан  ", 3, userSearchFields{
		nameParts: []string{"u.first_name", "u.last_name"},
		fields:    []string{"u.username", "u.email", "u.phone"},
	})
	if !ok {
		t.Fatalf("expected search condition")
	}

	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}

	if args[0] != "%картаев%" || args[1] != "%тамерлан%" {
		t.Fatalf("unexpected args: %#v", args)
	}

	for _, want := range []string{
		"$3",
		"$4",
		"concat_ws(' ', u.first_name, u.last_name)",
		"concat_ws(' ', u.last_name, u.first_name)",
		"u.username",
		"u.email",
		"u.phone",
		"AND",
	} {
		if !strings.Contains(condition, want) {
			t.Fatalf("condition %q does not contain %q", condition, want)
		}
	}
}

func TestBuildUserSearchConditionSupportsMiddleNameAndExternalIdentifiers(t *testing.T) {
	condition, args, ok := buildUserSearchCondition("TaMerLaN Example user@example.com", 1, userSearchFields{
		nameParts: []string{"hp.first_name", "hp.last_name", "hp.middle_name"},
		fields:    []string{"u.username", "u.email", "u.phone"},
	})
	if !ok {
		t.Fatalf("expected search condition")
	}

	wantArgs := []any{"%tamerlan%", "%example%", "%user@example.com%"}
	if len(args) != len(wantArgs) {
		t.Fatalf("expected %d args, got %d", len(wantArgs), len(args))
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Fatalf("arg %d = %#v, want %#v", i, args[i], wantArgs[i])
		}
	}

	for _, want := range []string{
		"hp.middle_name",
		"u.email",
		"u.phone",
		"$1",
		"$2",
		"$3",
	} {
		if !strings.Contains(condition, want) {
			t.Fatalf("condition %q does not contain %q", condition, want)
		}
	}
}

func TestBuildUserSearchConditionSupportsBothNameOrdersAndPartialInput(t *testing.T) {
	fields := userSearchFields{
		documents: []string{
			"COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '') || ' ' || COALESCE(u.full_name_local, '') || ' ' || COALESCE(u.username, '') || ' ' || COALESCE(u.email, '')",
		},
		fields: []string{"u.first_name", "u.last_name", "u.full_name_local", "u.username", "u.email"},
	}

	for _, tt := range []struct {
		query string
		want  []any
	}{
		{query: "Куанышбек Есекеев", want: []any{"%куанышбек%", "%есекеев%"}},
		{query: "Есекеев Куанышбек", want: []any{"%есекеев%", "%куанышбек%"}},
		{query: "Есек Куан", want: []any{"%есек%", "%куан%"}},
	} {
		t.Run(tt.query, func(t *testing.T) {
			condition, args, ok := buildUserSearchCondition(tt.query, 1, fields)
			if !ok {
				t.Fatal("expected a search condition")
			}
			if len(args) != len(tt.want) {
				t.Fatalf("expected %d token arguments, got %#v", len(tt.want), args)
			}
			for i := range tt.want {
				if args[i] != tt.want[i] {
					t.Fatalf("argument %d = %#v, want %#v", i, args[i], tt.want[i])
				}
			}
			if !strings.Contains(condition, "u.full_name_local") || !strings.Contains(condition, "u.email") {
				t.Fatalf("search condition does not include all calendar name sources: %q", condition)
			}
		})
	}
}

func TestBuildUserSearchConditionEmptyQuery(t *testing.T) {
	condition, args, ok := buildUserSearchCondition("   ", 5, userSearchFields{
		nameParts: []string{"first_name", "last_name"},
		fields:    []string{"username", "email"},
	})

	if ok {
		t.Fatalf("expected empty query to be ignored")
	}
	if condition != "" {
		t.Fatalf("expected empty condition, got %q", condition)
	}
	if len(args) != 0 {
		t.Fatalf("expected no args, got %#v", args)
	}
}
