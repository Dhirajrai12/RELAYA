package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"relaya/internal/audit"
	"relaya/internal/auth"
	"relaya/internal/httpx"
)

const minPasswordLen = 10

type sessionResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      userView  `json:"user"`
}

type userView struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
		OrgName  string `json:"org_name"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if ok, wait := s.Limits.SignupIP.Allow(httpx.ClientIP(r, s.TrustProxyHeaders)); !ok {
		return httpx.TooManyRequests(w, wait, "too many sign-ups from this address; try again later")
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(in.Email))
	if err != nil || addr.Address != strings.TrimSpace(in.Email) {
		return httpx.BadRequest("a valid email is required")
	}
	if len(in.Password) < minPasswordLen || len(in.Password) > 72 {
		return httpx.BadRequest("password must be %d to 72 characters", minPasswordLen)
	}
	orgName, err := requireName(in.OrgName, "org_name", 100)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}

	var out sessionResponse
	err = s.tx(r.Context(), func(tx pgx.Tx) error {
		u := userView{Email: addr.Address, Name: strings.TrimSpace(in.Name)}
		err := tx.QueryRow(r.Context(),
			`INSERT INTO users (email, name, password_hash) VALUES ($1, $2, $3) RETURNING id`,
			u.Email, u.Name, hash).Scan(&u.ID)
		if isUniqueViolation(err) {
			return httpx.Conflict("an account with this email already exists")
		}
		if err != nil {
			return err
		}
		orgID, err := createOrgTx(r.Context(), tx, orgName, u.ID)
		if err != nil {
			return err
		}
		if err := audit.Record(r.Context(), tx, audit.Entry{
			OrgID: orgID, ActorType: "user", ActorID: u.ID,
			Action: "org.create", TargetType: "organization", TargetID: orgID,
		}); err != nil {
			return err
		}
		out.User = u
		out.Token, out.ExpiresAt, err = s.Auth.CreateSession(r.Context(), tx, u.ID)
		return err
	})
	if err != nil {
		return err
	}
	s.setAuthCookie(w, out.Token, out.ExpiresAt)
	httpx.JSON(w, http.StatusCreated, out)
	return nil
}

// createOrgTx creates an organization with ownerID as its owner and returns its ID.
func createOrgTx(ctx context.Context, tx pgx.Tx, name, ownerID string) (string, error) {
	base := slugify(name)
	slug := base
	var orgID string
	for attempt := 0; ; attempt++ {
		err := tx.QueryRow(ctx, `
			INSERT INTO organizations (name, slug) VALUES ($1, $2)
			ON CONFLICT (slug) DO NOTHING RETURNING id`, name, slug).Scan(&orgID)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) || attempt >= 5 {
			return "", err
		}
		slug = base + "-" + randomSuffix()
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'owner')`, orgID, ownerID)
	return orgID, err
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	// Per IP for floods; per account (failures only) for slow password guessing,
	// so the real user isn't locked out by someone else's successful sign-ins.
	if ok, wait := s.Limits.LoginIP.Allow(httpx.ClientIP(r, s.TrustProxyHeaders)); !ok {
		return httpx.TooManyRequests(w, wait, "too many sign-in attempts; try again shortly")
	}
	account := strings.ToLower(strings.TrimSpace(in.Email))
	if ok, wait := s.Limits.LoginEmail.Check(account); !ok {
		return httpx.TooManyRequests(w, wait, "too many failed sign-ins for this account; try again later")
	}
	var u userView
	var hash string
	err := s.Pool.QueryRow(r.Context(),
		`SELECT id, email, name, password_hash FROM users WHERE lower(email) = lower($1)`,
		strings.TrimSpace(in.Email)).Scan(&u.ID, &u.Email, &u.Name, &hash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !auth.CheckPasswordOrDummy(hash, err == nil, in.Password) {
		s.Limits.LoginEmail.Take(account)
		return httpx.NewError(http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
	}

	out := sessionResponse{User: u}
	if out.Token, out.ExpiresAt, err = s.Auth.CreateSession(r.Context(), s.Pool, u.ID); err != nil {
		return err
	}
	s.setAuthCookie(w, out.Token, out.ExpiresAt)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	token := auth.RequestToken(r)
	if !strings.HasPrefix(token, auth.SessionPrefix) {
		return httpx.BadRequest("logout requires a session (API keys are revoked in Settings → API keys)")
	}
	if err := s.Auth.DeleteSession(r.Context(), token); err != nil {
		return err
	}
	s.clearAuthCookie(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.FromContext(r.Context())
	if p.APIKeyID != "" {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"api_key": map[string]string{"id": p.APIKeyID, "org_id": p.APIKeyOrgID, "role": string(p.APIKeyRole)},
		})
		return nil
	}
	var u userView
	if err := s.Pool.QueryRow(r.Context(),
		`SELECT id, email, name FROM users WHERE id = $1`, p.UserID).Scan(&u.ID, &u.Email, &u.Name); err != nil {
		return err
	}
	orgs, err := s.orgsForUser(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": u, "orgs": orgs})
	return nil
}

// The dashboard's session lives in an httpOnly cookie, so scripts (and XSS) can
// never read it. SameSite=Strict keeps other sites from sending it (no CSRF).
// It is marked Secure whenever the dashboard is served over HTTPS; behind IIS the
// API itself sees plain HTTP, so the dashboard URL decides, not r.TLS.
func (s *Server) sessionCookie(value string, expires time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: auth.SessionCookie, Value: value, Path: "/", Expires: expires, MaxAge: maxAge,
		HttpOnly: true, Secure: strings.HasPrefix(s.DashboardURL, "https://"), SameSite: http.SameSiteStrictMode,
	}
}

func (s *Server) setAuthCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, s.sessionCookie(token, expiresAt, 0))
}

func (s *Server) clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, s.sessionCookie("", time.Time{}, -1))
}
