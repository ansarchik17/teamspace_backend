package service

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

func contactTestPNG(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestContactAvatarValidation(t *testing.T) {
	var jpegBytes bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil))
	_, jpegExt, jpegErr := normalizeContactAvatar(&jpegBytes)
	require.NoError(t, jpegErr)
	require.Equal(t, ".jpg", jpegExt)
	valid := contactTestPNG(t)
	output, ext, err := normalizeContactAvatar(bytes.NewReader(append(valid, []byte("unwanted trailer")...)))
	require.NoError(t, err)
	require.Equal(t, ".png", ext)
	require.NotContains(t, string(output), "unwanted trailer")
	for _, data := range [][]byte{nil, []byte("<svg></svg>"), []byte("not a jpeg"), bytes.Repeat([]byte{'x'}, MaxContactAvatarBytes+1), valid[:len(valid)/2]} {
		_, _, err = normalizeContactAvatar(bytes.NewReader(data))
		require.Error(t, err)
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2049, 1))))
	_, _, err = normalizeContactAvatar(&buf)
	require.Error(t, err)
}

type contactMemoryStorage struct {
	data        []byte
	deleted     bool
	afterUpload func()
}

func (f *contactMemoryStorage) UploadFile(ctx context.Context, id int64, name, kind string, size int64, r io.Reader) (string, error) {
	f.data, _ = io.ReadAll(r)
	if f.afterUpload != nil {
		f.afterUpload()
	}
	return "image/1/avatar.png", nil
}
func (f *contactMemoryStorage) DeleteFile(context.Context, string) error {
	f.deleted = true
	return nil
}
func (f *contactMemoryStorage) GenerateSignedURL(object string, expiry time.Duration) string {
	return "https://files.example.com/api/v1/files/download?object_name=" + object + "&signature=fresh"
}

func TestContactProfileIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MESSENGER_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := "cnt002_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ctx, `CREATE TABLE organizations(id UUID PRIMARY KEY,status TEXT,owner_id BIGINT,updated_at TIMESTAMPTZ);
 CREATE TABLE teams(id UUID PRIMARY KEY,organization_id UUID,parent_team_id UUID,name TEXT,unit_type TEXT,display_order INT DEFAULT 0,deleted_at TIMESTAMPTZ);
 CREATE TABLE users(id BIGINT PRIMARY KEY,organization_id UUID,role TEXT,deleted_at TIMESTAMPTZ,is_active BOOLEAN DEFAULT TRUE,full_name_local TEXT,first_name TEXT,last_name TEXT,email TEXT,username TEXT,position TEXT,avatar_url TEXT,availability_status TEXT,team_id UUID,manager_id BIGINT,updated_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	org, other, unit := uuid.New(), uuid.New(), uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO organizations VALUES($1,'active',1,now()),($2,'active',9,now());INSERT INTO teams(id,organization_id,name,unit_type) VALUES($3,$1,'Отдел','department');INSERT INTO users(id,organization_id,role,full_name_local,email,username,team_id,manager_id) VALUES
 (1,$1,'user','Ансар','login@example.com','ansar',$3,2),(2,$1,'user','Руководитель','manager@example.com','manager',$3,3),(3,$1,'user','Директор','director@example.com','director',$3,NULL),(4,$1,'user','Сотрудник','report@example.com','report',$3,1),(9,$2,'user','Чужой','foreign@example.com','foreign',NULL,NULL);`, org, other, unit)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../db/migrations/20261001120000_employee_contacts.sql")
	require.NoError(t, err)
	parts := strings.Split(string(migration), "-- +goose Down")
	_, err = db.Exec(ctx, parts[0])
	require.NoError(t, err)
	privacyMigration, err := os.ReadFile("../../db/migrations/20261001180000_contact_privacy.sql")
	require.NoError(t, err)
	privacyParts := strings.Split(string(privacyMigration), "-- +goose Down")
	_, err = db.Exec(ctx, privacyParts[0])
	require.NoError(t, err)
	repo := repository.NewOrganizationRepository(db, zap.NewNop())
	svc := NewOrganizationService(repo, zap.NewNop())
	storage := &contactMemoryStorage{}
	svc.files = storage
	t.Run("personal contact privacy", func(t *testing.T) { testContactPrivacy(t, ctx, db, svc) })
	card, err := svc.GetContactProfile(ctx, 1, 1)
	require.NoError(t, err)
	require.Nil(t, card.PersonalEmail)
	require.Nil(t, card.PersonalPhone)
	require.Equal(t, domain.ContactAdmins, card.Privacy.PersonalEmailVisibility)
	require.Equal(t, "Отдел", *card.UnitName)
	require.Equal(t, int64(2), card.Manager.ID)
	require.Len(t, card.ManagementChain, 2)
	require.Equal(t, int64(3), card.ManagementChain[1].ID)
	require.Equal(t, 1, card.DirectReportsTotal)
	require.False(t, card.Actions.Meeting.Enabled)
	quick, err := svc.ContactActions(ctx, 2, 1)
	require.NoError(t, err)
	require.True(t, quick.Meeting.Enabled)
	require.Equal(t, []int64{1}, quick.Meeting.AttendeeIDs)
	require.Equal(t, "login@example.com", quick.Email.Address)
	require.Equal(t, []int64{1}, quick.Meeting.AttendeeIDs)
	for _, id := range []int64{9, 99} {
		_, err = svc.ContactActions(ctx, 1, id)
		require.Error(t, err)
	}
	for _, id := range []int64{9, 99} {
		_, err = svc.GetContactProfile(ctx, 1, id)
		require.Error(t, err)
	}
	email, phone := " work@example.com ", "+7 (701) 123-45-67"
	require.NoError(t, svc.UpdateContact(ctx, 1, 1, domain.UpdateContactRequest{WorkEmail: &email, Phone: &phone}))
	require.Error(t, svc.UpdateContact(ctx, 1, 2, domain.UpdateContactRequest{Phone: &phone}))
	card, err = svc.GetContactProfile(ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, "work@example.com", *card.WorkEmail)
	require.Equal(t, "work@example.com", card.Actions.Email.Address)
	quick, err = svc.ContactActions(ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, "work@example.com", quick.Email.Address)
	require.Equal(t, "login@example.com", card.Email)
	require.Equal(t, "+77011234567", *card.Phone)
	empty := ""
	require.NoError(t, svc.UpdateContact(ctx, 1, 1, domain.UpdateContactRequest{WorkEmail: &empty}))
	card, err = svc.GetContactProfile(ctx, 1, 1)
	require.NoError(t, err)
	require.Nil(t, card.WorkEmail)
	require.Equal(t, "login@example.com", card.Actions.Email.Address)
	require.NotNil(t, card.Phone)
	reports, err := svc.DirectReports(ctx, 2, 1, domain.ContactFilter{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 1, reports.Total)
	require.Equal(t, int64(4), reports.Items[0].ID)
	_, err = svc.UploadContactAvatar(ctx, 1, 2, bytes.NewReader(contactTestPNG(t)))
	require.Error(t, err)
	require.Nil(t, storage.data)
	card, err = svc.UploadContactAvatar(ctx, 1, 1, bytes.NewReader(contactTestPNG(t)))
	require.NoError(t, err)
	require.NotEmpty(t, storage.data)
	require.Contains(t, *card.AvatarURL, "signature=fresh")
	for _, sql := range []string{`UPDATE users SET manager_id=1 WHERE id=1`, `UPDATE users SET manager_id=1 WHERE id=3`, `UPDATE users SET manager_id=9 WHERE id=1`} {
		_, err = db.Exec(ctx, sql)
		require.Error(t, err, sql)
	}
	_, err = db.Exec(ctx, `UPDATE users SET manager_id=NULL WHERE id=1`)
	require.NoError(t, err)
	require.NoError(t, repo.AssignUser(ctx, 1, 1, domain.AssignUserToUnitRequest{UnitID: &unit, ManagerID: ptrContactID(2)}))

	// Simultaneous opposing assignments cannot both succeed.
	_, err = db.Exec(ctx, `UPDATE users SET manager_id=NULL WHERE id IN (2,3)`)
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, pair := range [][2]int64{{2, 3}, {3, 2}} {
		go func(pair [2]int64) {
			<-start
			_, e := db.Exec(ctx, `UPDATE users SET manager_id=$2 WHERE id=$1`, pair[0], pair[1])
			results <- e
		}(pair)
	}
	close(start)
	first, second := <-results, <-results
	require.True(t, (first == nil) != (second == nil), "exactly one competing assignment must succeed")
	_, err = db.Exec(ctx, `UPDATE users SET manager_id=NULL WHERE id IN (2,3);UPDATE users SET manager_id=3 WHERE id=2`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE users SET organization_id=$1 WHERE id=2`, other)
	require.Error(t, err)
	// Old cyclic rows remain readable without infinite traversal.
	_, err = db.Exec(ctx, `ALTER TABLE users DISABLE TRIGGER employee_manager_guard;UPDATE users SET manager_id=1 WHERE id=3;ALTER TABLE users ENABLE TRIGGER employee_manager_guard;`)
	require.NoError(t, err)
	card, err = svc.GetContactProfile(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, card.HierarchyTruncated)
	require.Len(t, card.ManagementChain, 2)
	storage.afterUpload = func() { _, e := db.Exec(ctx, `UPDATE users SET is_active=FALSE WHERE id=1`); require.NoError(t, e) }
	_, err = svc.UploadContactAvatar(ctx, 1, 1, bytes.NewReader(contactTestPNG(t)))
	require.Error(t, err)
	require.True(t, storage.deleted)
	_, err = svc.ContactActions(ctx, 2, 1)
	require.Error(t, err)
	_, err = db.Exec(ctx, privacyParts[1])
	require.NoError(t, err)
	_, err = db.Exec(ctx, parts[1])
	require.NoError(t, err)
}

func ptrContactID(id int64) *int64 { return &id }
