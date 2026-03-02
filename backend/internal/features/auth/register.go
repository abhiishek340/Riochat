package auth

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

const maxUsernameAttempts = 20

// createAccount inserts a user with its credential (and optionally an identity
// link) atomically. nextName yields the username candidate for attempt n
// (0-based); candidates already taken are skipped, up to 20 tries.
func (s *Service) createAccount(ctx context.Context, u User, cred Credential, link *UserLink, nextName func(attempt int) string) (*User, error) {
	for attempt := 0; attempt < maxUsernameAttempts; attempt++ {
		name := nextName(attempt)
		var n int64
		if err := s.conn(ctx).Model(&User{}).Where("username = ?", name).Count(&n).Error; err != nil {
			return nil, err
		}
		if n > 0 {
			continue
		}
		u.Username = name
		u.PublicID = newPublicID()
		row, c, l := u, cred, link
		var userInsertFailed bool
		err := s.conn(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&row).Error; err != nil {
				userInsertFailed = true
				return err
			}
			c.UserID = row.ID
			if err := tx.Create(&c).Error; err != nil {
				return err
			}
			if l != nil {
				lc := *l
				lc.UserID = row.ID
				if err := tx.Create(&lc).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil {
			return &row, nil
		}
		if userInsertFailed && database.IsDuplicate(err) {
			continue // lost a race for the username
		}
		return nil, err
	}
	return nil, ErrUsernameExists.WithStatus(400)
}

type sentResult struct {
	Sent      bool      `json:"sent"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (s *Service) registrationOpen(st Settings) error {
	if !st.EmailLoginEnabled || !st.EmailRegistrationEnabled {
		return ErrEmailRegistrationDisabled
	}
	return nil
}

func (s *Service) emailTaken(ctx context.Context, email string, exceptUser uint) (bool, error) {
	var n int64
	q := s.conn(ctx).Model(&User{}).Where("email = ?", email)
	if exceptUser != 0 {
		q = q.Where("id <> ?", exceptUser)
	}
	if err := q.Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// RegisterStart mails a registration code.
func (s *Service) RegisterStart(ctx context.Context, rawEmail, turnstile string) (sentResult, error) {
	st := s.settings(ctx)
	if err := s.registrationOpen(st); err != nil {
		return sentResult{}, err
	}
	email, err := normalizeEmail(rawEmail)
	if err != nil {
		return sentResult{}, err
	}
	if err := checkEmailPolicy(email, st); err != nil {
		return sentResult{}, err
	}
	if !st.EmailVerificationEnabled {
		return sentResult{}, ErrEmailVerificationDisabled
	}
	if err := s.checkTurnstile(ctx, turnstile); err != nil {
		return sentResult{}, err
	}
	taken, err := s.emailTaken(ctx, email, 0)
	if err != nil {
		return sentResult{}, err
	}
	if taken {
		return sentResult{}, ErrEmailAlreadyExists
	}
	exp, err := s.sendCode(ctx, 0, PurposeRegister, email)
	if err != nil {
		s.event(ctx, eventInput{Type: "register_code", Result: resultFailure, Reason: reasonOf(err),
			Detail: map[string]any{"emailHash": sha256Hex(email)}})
		return sentResult{}, err
	}
	s.event(ctx, eventInput{Type: "register_code", Result: resultSuccess, Detail: map[string]any{"emailHash": sha256Hex(email)}})
	return sentResult{Sent: true, ExpiresAt: exp}, nil
}

type registerInput struct {
	Email     string
	Password  string
	Code      string
	Turnstile string
}

// RegisterComplete creates the account and signs it in.
func (s *Service) RegisterComplete(ctx context.Context, in registerInput) (signIn, error) {
	st := s.settings(ctx)
	if err := s.registrationOpen(st); err != nil {
		return signIn{}, err
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return signIn{}, err
	}
	if err := checkEmailPolicy(email, st); err != nil {
		return signIn{}, err
	}
	pw, err := checkPasswordPolicy(in.Password)
	if err != nil {
		return signIn{}, err
	}
	if !st.EmailVerificationEnabled {
		if err := s.checkTurnstile(ctx, in.Turnstile); err != nil {
			return signIn{}, err
		}
	}
	taken, err := s.emailTaken(ctx, email, 0)
	if err != nil {
		return signIn{}, err
	}
	if taken {
		return signIn{}, ErrEmailAlreadyExists
	}
	if st.EmailVerificationEnabled {
		if err := s.verifyCode(ctx, 0, PurposeRegister, email, strings.TrimSpace(in.Code)); err != nil {
			s.event(ctx, eventInput{Type: "register", Result: resultFailure, Reason: reasonOf(err),
				Detail: map[string]any{"emailHash": sha256Hex(email)}})
			return signIn{}, err
		}
	}
	hash, err := hashPassword(pw)
	if err != nil {
		return signIn{}, err
	}
	now := s.now()
	local, _ := emailParts(email)
	u := User{
		DisplayName: normalizeDisplayName(local), Email: email, Role: RoleUser, Status: StatusActive,
		Timezone: "Etc/UTC", Locale: "en-US", EmailSource: EmailSourceLocalRegister,
	}
	if st.EmailVerificationEnabled {
		u.EmailVerifiedAt = &now
	}
	cred := Credential{
		PasswordHash: hash, PasswordAlgo: "bcrypt", PasswordEnabled: true,
		PasswordUpdatedAt: &now, PasswordSetAt: &now, PasswordOrigin: PasswordOriginLocalRegister,
	}
	base := generatedUsername(local, "user", email)
	created, err := s.createAccount(ctx, u, cred, nil, func(n int) string {
		if n == 0 {
			return base
		}
		return suffixedUsername(base, n+1)
	})
	if err != nil {
		return signIn{}, err
	}
	out, sess, err := s.newSession(ctx, created)
	if err != nil {
		return signIn{}, err
	}
	detail := sessionDetail(sess)
	detail["emailHash"] = sha256Hex(email)
	s.event(ctx, eventInput{Type: "register", Result: resultSuccess, UserID: created.ID, Detail: detail})
	return out, nil
}
