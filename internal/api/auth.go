package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName  = "awg_session"
	sessionTTL  = 12 * time.Hour
	ctxAdminKey = ctxKey("admin")
)

type ctxKey string

// signToken builds "adminID|expiry.hmac" signed with the session secret.
func (s *Server) signToken(adminID string) string {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := adminID + "|" + strconv.FormatInt(exp, 10)
	mac := s.hmac(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + mac
}

func (s *Server) hmac(payload string) string {
	m := hmac.New(sha256.New, []byte(s.Secret))
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// verifyToken returns the admin id if the token is valid and unexpired.
func (s *Server) verifyToken(token string) (string, bool) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	payload := string(raw)
	if subtle.ConstantTimeCompare([]byte(s.hmac(payload)), []byte(parts[1])) != 1 {
		return "", false
	}
	fields := strings.SplitN(payload, "|", 2)
	if len(fields) != 2 {
		return "", false
	}
	exp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return fields[0], true
}

// requireAuth is middleware rejecting requests without a valid session cookie.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		adminID, ok := s.verifyToken(c.Value)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxAdminKey, adminID)
		next(w, r.WithContext(ctx))
	}
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleLogin verifies credentials and sets the session cookie.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, hash, err := s.St.GetAdminHash(r.Context(), req.Username)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	// HttpOnly + SameSite are always on. Secure is on unless INSECURE_COOKIES is
	// explicitly set (local, non-TLS development only) — hence it is a variable,
	// not a literal true.
	// #nosec G124 -- Secure is configurable by design (see SecureCookies).
	http.SetCookie(w, &http.Cookie{ // nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
		Name:     cookieName,
		Value:    s.signToken(id.String()),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.SecureCookies,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Clear the session cookie with the same security attributes it was set with,
	// so browsers reliably overwrite the original rather than keeping a laxer twin.
	// #nosec G124 -- Secure is configurable by design (see SecureCookies / handleLogin).
	http.SetCookie(w, &http.Cookie{ // nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.SecureCookies,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// BootstrapAdmin creates the first admin if none exist. Called at startup.
func BootstrapAdmin(ctx context.Context, s *Server, username, password string) error {
	n, err := s.St.AdminCount(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if username == "" || password == "" {
		return fmt.Errorf("no admins exist; set ADMIN_USER and ADMIN_PASSWORD to bootstrap")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.St.CreateAdmin(ctx, username, string(hash))
}
