package main

import (
	"github.com/pressly/goose/v3"
	"github.com/west6ide/workspace-backend/internal/domain"
	"math"
	"net/url"
	"strings"
	"testing"
)

func TestBuildPostgresDSNEscapesCredentials(t *testing.T) {
	dsn := buildPostgresDSN("db.internal", "5432", "workspace-user", "p@ss word:/?#[]", "workspace", "require")
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}

	password, ok := parsed.User.Password()
	if !ok {
		t.Fatal("expected password in DSN")
	}
	if password != "p@ss word:/?#[]" {
		t.Fatalf("password was not preserved: %q", password)
	}
	if parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("unexpected sslmode: %q", parsed.Query().Get("sslmode"))
	}
}

func TestMigrationsEnabled(t *testing.T) {
	for _, value := range []string{"", "1", "true", "yes", "on", "unexpected"} {
		if !migrationsEnabled(value) {
			t.Fatalf("expected migrations to be enabled for %q", value)
		}
	}
	for _, value := range []string{"0", "false", "no", "off"} {
		if migrationsEnabled(value) {
			t.Fatalf("expected migrations to be disabled for %q", value)
		}
	}
}

func TestRepositoryMigrationVersionsAreUnique(t *testing.T) {
	defer func() {
		if err := recover(); err != nil {
			t.Fatalf("migration collection panicked: %v", err)
		}
	}()
	if _, err := goose.CollectMigrations("../../db/migrations", 0, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
}

func TestAIMigrationsFollowReleasedIForm(t *testing.T) {
	migrations, err := goose.CollectMigrations("../../db/migrations", 0, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	byVersion := make(map[int64]string)
	for _, migration := range migrations {
		byVersion[migration.Version] = migration.Source
	}
	if !strings.HasSuffix(byVersion[20261019000100], "20261019000100_iform_operation_receipts.sql") {
		t.Fatal("released iForm migration must retain its version")
	}
	previous := int64(20261019000100)
	for _, version := range domain.AIAccountingMigrationVersions {
		if version <= previous || byVersion[version] == "" {
			t.Fatalf("AI migration %d must exist and follow %d", version, previous)
		}
		previous = version
	}
}
