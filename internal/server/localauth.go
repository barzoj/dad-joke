package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/gen/go/controlapi"
	"github.com/barzoj/dad-joke/internal/authctx"
	"github.com/barzoj/dad-joke/internal/password"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/tokenhash"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	sessionCookieName = "dj_session"
	sessionTTL        = 7 * 24 * time.Hour
	minPasswordLen    = 8
)

// Register creates a tenant + user (argon2id) and returns a working session.
func (c *ControlAPI) Register(ctx context.Context, req controlapi.RegisterRequestObject) (controlapi.RegisterResponseObject, error) {
	reqID := uuid.NewString()
	if !c.AllowSignup {
		return controlapi.Register403JSONResponse{
			Code: "forbidden", Message: "signups are disabled", RequestId: reqID,
		}, nil
	}
	if req.Body == nil {
		return controlapi.Register400JSONResponse{
			Code: "bad_request", Message: "missing body", RequestId: reqID,
		}, nil
	}
	email := normalizeEmail(string(req.Body.Email))
	if email == "" || !strings.Contains(email, "@") {
		return controlapi.Register400JSONResponse{
			Code: "bad_request", Message: "email is required", RequestId: reqID,
		}, nil
	}
	if len(req.Body.Password) < minPasswordLen {
		return controlapi.Register400JSONResponse{
			Code: "bad_request", Message: "password too short", RequestId: reqID,
		}, nil
	}

	tenantName := email
	if req.Body.TenantName != nil && strings.TrimSpace(*req.Body.TenantName) != "" {
		tenantName = strings.TrimSpace(*req.Body.TenantName)
	}

	hash, err := password.Hash(req.Body.Password)
	if err != nil {
		return controlapi.Register500JSONResponse{
			Code: "internal_error", Message: "password hash failed", RequestId: reqID,
		}, nil
	}

	now := c.now()
	ten := &store.Tenant{Name: tenantName, CreatedAt: now}
	if err := c.Store.Tenants().Create(ctx, ten); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return controlapi.Register409JSONResponse{
				Code: "conflict", Message: "tenant name already exists", RequestId: reqID,
			}, nil
		}
		return controlapi.Register503JSONResponse{
			Code: "unavailable", Message: "registration backend unavailable", RequestId: reqID,
		}, nil
	}

	user := &store.User{
		TenantID:     ten.ID,
		Email:        email,
		PasswordHash: hash,
		CreatedAt:    now,
	}
	if err := c.Store.Users().Create(ctx, user); err != nil {
		_ = c.Store.Tenants().Delete(ctx, ten.ID) // compensating rollback — no orphan tenant
		if errors.Is(err, store.ErrConflict) {
			return controlapi.Register409JSONResponse{
				Code: "conflict", Message: "email already registered", RequestId: reqID,
			}, nil
		}
		return controlapi.Register503JSONResponse{
			Code: "unavailable", Message: "registration backend unavailable", RequestId: reqID,
		}, nil
	}

	sess, raw, err := c.issueSession(ctx, user)
	if err != nil {
		return controlapi.Register500JSONResponse{
			Code: "internal_error", Message: "session create failed", RequestId: reqID,
		}, nil
	}
	body, err := toAuthSession(user, raw, sess.ExpiresAt)
	if err != nil {
		return controlapi.Register500JSONResponse{
			Code: "internal_error", Message: "session encode failed", RequestId: reqID,
		}, nil
	}
	return controlapi.Register201JSONResponse{
		Body: body,
		Headers: controlapi.Register201ResponseHeaders{
			SetCookie: c.sessionCookie(raw, sess.ExpiresAt),
		},
	}, nil
}

// Login authenticates email/password and returns a working session.
func (c *ControlAPI) Login(ctx context.Context, req controlapi.LoginRequestObject) (controlapi.LoginResponseObject, error) {
	reqID := uuid.NewString()
	if req.Body == nil {
		return controlapi.Login400JSONResponse{
			Code: "bad_request", Message: "missing body", RequestId: reqID,
		}, nil
	}
	email := normalizeEmail(string(req.Body.Email))
	ip := clientIPFromContext(ctx)

	ipKey := "loginfail:ip:" + ip
	emailKey := "loginfail:email:" + email
	if c.LoginFailures != nil {
		if retry, exceeded := c.LoginFailures.Exceeded(ipKey); exceeded {
			return loginTooMany(reqID, retry), nil
		}
		if email != "" {
			if retry, exceeded := c.LoginFailures.Exceeded(emailKey); exceeded {
				return loginTooMany(reqID, retry), nil
			}
		}
	}

	user, err := c.Store.Users().GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		// Store/infra blip: fail closed without burning login-failure budget.
		return controlapi.Login503JSONResponse{
			Code: "unavailable", Message: "authentication backend unavailable", RequestId: reqID,
		}, nil
	}

	hash := ""
	if user != nil {
		hash = user.PasswordHash
	}
	if !password.VerifyOrDummy(hash, req.Body.Password) {
		c.recordLoginFailure(ipKey, emailKey)
		if c.LoginFailures != nil {
			if retry, exceeded := c.LoginFailures.Exceeded(ipKey); exceeded {
				return loginTooMany(reqID, retry), nil
			}
			if email != "" {
				if retry, exceeded := c.LoginFailures.Exceeded(emailKey); exceeded {
					return loginTooMany(reqID, retry), nil
				}
			}
		}
		// Identical 401 for unknown email and wrong password (no enumeration).
		return controlapi.Login401JSONResponse{
			Code: "unauthorized", Message: "invalid email or password", RequestId: reqID,
		}, nil
	}

	sess, raw, err := c.issueSession(ctx, user)
	if err != nil {
		return controlapi.Login500JSONResponse{
			Code: "internal_error", Message: "session create failed", RequestId: reqID,
		}, nil
	}
	body, err := toAuthSession(user, raw, sess.ExpiresAt)
	if err != nil {
		return controlapi.Login500JSONResponse{
			Code: "internal_error", Message: "session encode failed", RequestId: reqID,
		}, nil
	}
	return controlapi.Login200JSONResponse{
		Body: body,
		Headers: controlapi.Login200ResponseHeaders{
			SetCookie: c.sessionCookie(raw, sess.ExpiresAt),
		},
	}, nil
}

// Logout revokes the current user session.
func (c *ControlAPI) Logout(ctx context.Context, _ controlapi.LogoutRequestObject) (controlapi.LogoutResponseObject, error) {
	reqID := uuid.NewString()
	sid, err := authctx.SessionID(ctx)
	if err != nil {
		return controlapi.Logout401JSONResponse{
			Code: "unauthorized", Message: "session required", RequestId: reqID,
		}, nil
	}
	if err := c.Store.Sessions().Revoke(ctx, sid, c.now()); err != nil && !errors.Is(err, store.ErrNotFound) {
		return controlapi.Logout500JSONResponse{
			Code: "internal_error", Message: "logout failed", RequestId: reqID,
		}, nil
	}
	return controlapi.Logout204Response{
		Headers: controlapi.Logout204ResponseHeaders{
			SetCookie: c.clearSessionCookie(),
		},
	}, nil
}

// GetMe returns the authenticated user profile (session) or 401 for PAT-only.
func (c *ControlAPI) GetMe(ctx context.Context, _ controlapi.GetMeRequestObject) (controlapi.GetMeResponseObject, error) {
	reqID := uuid.NewString()
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return controlapi.GetMe401JSONResponse{
			Code: "unauthorized", Message: "missing auth context", RequestId: reqID,
		}, nil
	}
	userID, err := authctx.UserID(ctx)
	if err != nil {
		return controlapi.GetMe401JSONResponse{
			Code: "unauthorized", Message: "user session required", RequestId: reqID,
		}, nil
	}
	user, err := c.Store.Users().GetByID(ctx, tenantID, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return controlapi.GetMe401JSONResponse{
				Code: "unauthorized", Message: "user not found", RequestId: reqID,
			}, nil
		}
		return controlapi.GetMe503JSONResponse{
			Code: "unavailable", Message: "authentication backend unavailable", RequestId: reqID,
		}, nil
	}
	me, err := toUserMe(user)
	if err != nil {
		return controlapi.GetMe500JSONResponse{
			Code: "internal_error", Message: "user encode failed", RequestId: reqID,
		}, nil
	}
	return controlapi.GetMe200JSONResponse(me), nil
}

func (c *ControlAPI) issueSession(ctx context.Context, user *store.User) (*store.Session, string, error) {
	raw, hash, err := tokenhash.GenerateSession()
	if err != nil {
		return nil, "", err
	}
	now := c.now()
	ses := &store.Session{
		UserID:    user.ID,
		TenantID:  user.TenantID,
		Hash:      hash,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionTTL),
	}
	if err := c.Store.Sessions().Create(ctx, ses); err != nil {
		return nil, "", err
	}
	return ses, raw, nil
}

func (c *ControlAPI) recordLoginFailure(keys ...string) {
	if c.LoginFailures == nil {
		return
	}
	for _, k := range keys {
		if k != "" && !strings.HasSuffix(k, ":") {
			c.LoginFailures.Record(k)
		}
	}
}

func (c *ControlAPI) sessionCookie(raw string, expires time.Time) string {
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	parts := []string{
		sessionCookieName + "=" + raw,
		"Path=/",
		"HttpOnly",
		"SameSite=Lax",
		"Max-Age=" + strconv.Itoa(maxAge),
	}
	if c.CookieSecure {
		parts = append(parts, "Secure")
	}
	return strings.Join(parts, "; ")
}

func (c *ControlAPI) clearSessionCookie() string {
	parts := []string{
		sessionCookieName + "=",
		"Path=/",
		"HttpOnly",
		"SameSite=Lax",
		"Max-Age=0",
	}
	if c.CookieSecure {
		parts = append(parts, "Secure")
	}
	return strings.Join(parts, "; ")
}

func loginTooMany(reqID string, retry time.Duration) controlapi.Login429JSONResponse {
	secs := int(retry / time.Second)
	if secs < 1 {
		secs = 1
	}
	return controlapi.Login429JSONResponse{
		Body: controlapi.ErrorResponse{
			Code: "rate_limited", Message: "too many login attempts", RequestId: reqID,
		},
		Headers: controlapi.Login429ResponseHeaders{RetryAfter: strconv.Itoa(secs)},
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func toUserMe(user *store.User) (controlapi.UserMe, error) {
	uid, err := uuid.Parse(user.ID)
	if err != nil {
		return controlapi.UserMe{}, fmt.Errorf("user id: %w", err)
	}
	tid, err := uuid.Parse(user.TenantID)
	if err != nil {
		return controlapi.UserMe{}, fmt.Errorf("tenant id: %w", err)
	}
	return controlapi.UserMe{
		Id:       uid,
		Email:    openapi_types.Email(user.Email),
		TenantId: tid,
	}, nil
}

func toAuthSession(user *store.User, raw string, expires time.Time) (controlapi.AuthSession, error) {
	me, err := toUserMe(user)
	if err != nil {
		return controlapi.AuthSession{}, err
	}
	return controlapi.AuthSession{
		AccessToken: raw,
		TokenType:   controlapi.Bearer,
		ExpiresAt:   expires,
		User:        me,
	}, nil
}

type ctxClientIPKey struct{}

func withClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxClientIPKey{}, ip)
}

func clientIPFromContext(ctx context.Context) string {
	ip, _ := ctx.Value(ctxClientIPKey{}).(string)
	if ip == "" {
		return "unknown"
	}
	return ip
}

// injectClientIP is middleware that stashes RemoteAddr IP for login rate-limits.
func injectClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(withClientIP(r.Context(), clientIP(r))))
	})
}
