package user

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// fastBcrypt lowers the hashing cost for the duration of a test.
func fastBcrypt(t *testing.T) {
	t.Helper()
	old := bcryptCost
	bcryptCost = bcrypt.MinCost
	t.Cleanup(func() { bcryptCost = old })
}

// newEnv mounts the user area. The identity and billing tables that other areas
// own are migrated from this package's own row mappings, so the tests do not
// depend on those areas.
func newEnv(t *testing.T, ports Ports, opts ...testkit.Option) *testkit.Env {
	t.Helper()
	fastBcrypt(t)
	feature := features.Feature{
		Name: "user",
		Migrations: func() []migrate.Step {
			return append(Migrations(), migrate.AutoMigrate("test_foreign_tables",
				&Account{}, &Credential{}, &AuthEvent{}, &sessionRow{}, &providerRow{}, &linkRow{},
				&planRow{}, &priceRow{}, &subscriptionRow{}, &billingAccountRow{}, &balanceTxRow{}),
				migrate.AutoMigrate("test_audit_table", &audit.Log{}))
		},
		Mount: func(d deps.Deps) error { return MountWith(d, ports) },
	}
	return testkit.New(t, []features.Feature{feature}, opts...)
}

func newAccounts(env *testkit.Env, images ImageChecker) *Accounts {
	return NewAccounts(env.DB, env.Clock, images)
}

// seedAccount inserts an account row directly.
func seedAccount(t *testing.T, db *gorm.DB, username string, mod ...func(*Account)) Account {
	t.Helper()
	a := Account{
		PublicID: newPublicID(), Username: username, DisplayName: username, Role: RoleUser,
		Status: StatusActive, Timezone: DefaultTimezone, Locale: DefaultLocale, EmailSource: EmailSourceAdminSet,
	}
	for _, m := range mod {
		m(&a)
	}
	if err := db.Create(&a).Error; err != nil {
		t.Fatalf("seed account %s: %v", username, err)
	}
	return a
}

func seedCredential(t *testing.T, db *gorm.DB, userID uint) Credential {
	t.Helper()
	c := Credential{UserID: userID, PasswordHash: "old-hash", PasswordAlgo: "bcrypt", PasswordEnabled: true,
		PasswordOrigin: PasswordOriginLocalRegister, FailedLoginCount: 4}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	return c
}

// fakeFiles is a FileReader backed by a map.
type fakeFiles struct {
	mu      sync.Mutex
	uploads map[string]*Upload // "<owner>/<id>"
	opened  []string
	err     error
}

func newFakeFiles() *fakeFiles { return &fakeFiles{uploads: map[string]*Upload{}} }

func (f *fakeFiles) put(owner uint, id string, up Upload) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads[fmt.Sprintf("%d/%s", owner, id)] = &up
}

func (f *fakeFiles) OpenUpload(_ context.Context, owner uint, id string) (*Upload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fmt.Sprintf("%d/%s", owner, id)
	f.opened = append(f.opened, key)
	if f.err != nil {
		return nil, f.err
	}
	up, ok := f.uploads[key]
	if !ok {
		return nil, ErrUploadNotFound
	}
	cp := *up
	cp.Body = io.NopCloser(bytes.NewReader(up.bodyBytes()))
	return &cp, nil
}

// bodyBytes recovers the stored bytes from the template upload.
func (u *Upload) bodyBytes() []byte {
	if u.Body == nil {
		return nil
	}
	if r, ok := u.Body.(interface{ Bytes() []byte }); ok {
		return r.Bytes()
	}
	return nil
}

type bytesBody struct {
	*bytes.Reader
	raw []byte
}

func (b bytesBody) Close() error  { return nil }
func (b bytesBody) Bytes() []byte { return b.raw }

func body(data string) io.ReadCloser {
	raw := []byte(data)
	return bytesBody{bytes.NewReader(raw), raw}
}

// fakeImages is an ImageChecker that accepts configured (owner, upload) pairs.
type fakeImages struct {
	mu    sync.Mutex
	ok    map[string]bool
	calls int
	inTx  bool
	err   error
}

func newFakeImages() *fakeImages { return &fakeImages{ok: map[string]bool{}} }

func (f *fakeImages) allow(owner uint, id string) { f.ok[fmt.Sprintf("%d/%s", owner, id)] = true }

func (f *fakeImages) IsActiveImage(_ context.Context, tx *gorm.DB, owner uint, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.inTx = tx != nil
	if f.err != nil {
		return false, f.err
	}
	return f.ok[fmt.Sprintf("%d/%s", owner, id)], nil
}
