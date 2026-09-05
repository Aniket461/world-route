package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	CreatedAt   time.Time `json:"createdAt"`
}

type authClaims struct {
	UserID string `json:"uid"`
	jwt.RegisteredClaims
}

type contextKey string

const userIDKey contextKey = "userID"

func jwtSecret() []byte {
	s := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if s == "" {
		s = "dev-change-me-world-route"
	}
	return []byte(s)
}

func registerAuthRoutes(mux *http.ServeMux, pool *pgxpool.Pool) {
	mux.HandleFunc("POST /api/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username    string `json:"username"`
			Email       string `json:"email"`
			DisplayName string `json:"displayName"`
			Name        string `json:"name"`
			Password    string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		username := strings.TrimSpace(body.Username)
		email := strings.ToLower(strings.TrimSpace(body.Email))
		display := strings.TrimSpace(body.DisplayName)
		if display == "" {
			display = strings.TrimSpace(body.Name)
		}
		if display == "" {
			display = username
		}
		password := body.Password
		if len(username) < 3 || len(username) > 32 {
			writeErr(w, http.StatusBadRequest, "username must be 3–32 characters")
			return
		}
		if !strings.Contains(email, "@") || len(email) < 5 {
			writeErr(w, http.StatusBadRequest, "valid email is required")
			return
		}
		if len(password) < 6 {
			writeErr(w, http.StatusBadRequest, "password must be at least 6 characters")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not hash password")
			return
		}
		id := uuid.NewString()
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		_, err = pool.Exec(ctx, `
INSERT INTO users (id, username, email, display_name, password_hash)
VALUES ($1, $2, $3, $4, $5)`, id, username, email, display, string(hash))
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				writeErr(w, http.StatusConflict, "username or email already taken")
				return
			}
			writeErr(w, http.StatusInternalServerError, "could not create user")
			return
		}
		token, err := issueToken(id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not issue token")
			return
		}
		user, err := getUserByID(ctx, pool, id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not load user")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"token": token, "user": user})
	})

	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Email    string `json:"email"`
			Login    string `json:"login"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		ident := strings.TrimSpace(body.Login)
		if ident == "" {
			ident = strings.TrimSpace(body.Username)
		}
		if ident == "" {
			ident = strings.TrimSpace(body.Email)
		}
		ident = strings.ToLower(ident)
		if ident == "" || body.Password == "" {
			writeErr(w, http.StatusBadRequest, "username/email and password are required")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		var id, hash string
		err := pool.QueryRow(ctx, `
SELECT id, password_hash FROM users
WHERE lower(username) = $1 OR lower(email) = $1
LIMIT 1`, ident).Scan(&id, &hash)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeErr(w, http.StatusUnauthorized, "invalid credentials")
				return
			}
			writeErr(w, http.StatusInternalServerError, "login failed")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
			writeErr(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		token, err := issueToken(id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not issue token")
			return
		}
		user, err := getUserByID(ctx, pool, id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not load user")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user})
	})

	mux.Handle("GET /api/auth/me", requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := userIDFrom(r.Context())
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		user, err := getUserByID(ctx, pool, uid)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "user not found")
			return
		}
		writeJSON(w, http.StatusOK, user)
	})))
}

func getUserByID(ctx context.Context, pool *pgxpool.Pool, id string) (*User, error) {
	var u User
	err := pool.QueryRow(ctx, `
SELECT id::text, username, email, display_name, created_at
FROM users WHERE id = $1`, id).Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func issueToken(userID string) (string, error) {
	claims := authClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(30 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   userID,
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(jwtSecret())
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, err := parseBearer(r.Header.Get("Authorization"))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userIDKey, uid)))
	})
}

func parseBearer(header string) (string, error) {
	if header == "" {
		return "", errors.New("missing auth")
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("bad auth scheme")
	}
	token, err := jwt.ParseWithClaims(parts[1], &authClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return jwtSecret(), nil
	})
	if err != nil || !token.Valid {
		return "", errors.New("invalid token")
	}
	claims, ok := token.Claims.(*authClaims)
	if !ok || claims.UserID == "" {
		return "", errors.New("invalid claims")
	}
	return claims.UserID, nil
}

func userIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}
