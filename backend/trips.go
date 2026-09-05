package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SavedTripSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Profile   string    `json:"profile"`
	DurationS float64   `json:"durationS"`
	DistanceM float64   `json:"distanceM"`
	CreatedAt time.Time `json:"createdAt"`
}

type SavedTrip struct {
	SavedTripSummary
	Payload json.RawMessage `json:"payload"`
}

type saveTripRequest struct {
	Title   string          `json:"title"`
	Profile string          `json:"profile"`
	Payload json.RawMessage `json:"payload"`
}

func registerTripRoutes(mux *http.ServeMux, pool *pgxpool.Pool) {
	mux.Handle("GET /api/trips", requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := userIDFrom(r.Context())
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		rows, err := pool.Query(ctx, `
SELECT id::text, title, profile, duration_s, distance_m, created_at
FROM saved_trips
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT 100`, uid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not list trips")
			return
		}
		defer rows.Close()
		out := []SavedTripSummary{}
		for rows.Next() {
			var t SavedTripSummary
			if err := rows.Scan(&t.ID, &t.Title, &t.Profile, &t.DurationS, &t.DistanceM, &t.CreatedAt); err != nil {
				writeErr(w, http.StatusInternalServerError, "could not read trips")
				return
			}
			out = append(out, t)
		}
		writeJSON(w, http.StatusOK, out)
	})))

	mux.Handle("POST /api/trips", requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := userIDFrom(r.Context())
		var body saveTripRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		title := strings.TrimSpace(body.Title)
		if title == "" {
			title = "Saved trip"
		}
		if len(title) > 120 {
			title = title[:120]
		}
		if len(body.Payload) == 0 || !json.Valid(body.Payload) {
			writeErr(w, http.StatusBadRequest, "payload JSON is required")
			return
		}
		var meta struct {
			Result *PlanResponse `json:"result"`
		}
		_ = json.Unmarshal(body.Payload, &meta)
		var duration, distance float64
		profile := strings.TrimSpace(body.Profile)
		if meta.Result != nil {
			duration = meta.Result.DurationS
			distance = meta.Result.DistanceM
			if profile == "" {
				profile = meta.Result.Profile
			}
		}
		id := uuid.NewString()
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		_, err := pool.Exec(ctx, `
INSERT INTO saved_trips (id, user_id, title, profile, payload, duration_s, distance_m)
VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)`,
			id, uid, title, profile, string(body.Payload), duration, distance)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not save trip")
			return
		}
		writeJSON(w, http.StatusCreated, SavedTripSummary{
			ID:        id,
			Title:     title,
			Profile:   profile,
			DurationS: duration,
			DistanceM: distance,
			CreatedAt: time.Now().UTC(),
		})
	})))

	mux.Handle("GET /api/trips/{id}", requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := userIDFrom(r.Context())
		id := r.PathValue("id")
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		var t SavedTrip
		err := pool.QueryRow(ctx, `
SELECT id::text, title, profile, duration_s, distance_m, created_at, payload
FROM saved_trips
WHERE id = $1 AND user_id = $2`, id, uid).Scan(
			&t.ID, &t.Title, &t.Profile, &t.DurationS, &t.DistanceM, &t.CreatedAt, &t.Payload)
		if err != nil {
			if err == pgx.ErrNoRows {
				writeErr(w, http.StatusNotFound, "trip not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "could not load trip")
			return
		}
		writeJSON(w, http.StatusOK, t)
	})))

	mux.Handle("DELETE /api/trips/{id}", requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := userIDFrom(r.Context())
		id := r.PathValue("id")
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		tag, err := pool.Exec(ctx, `DELETE FROM saved_trips WHERE id = $1 AND user_id = $2`, id, uid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not delete trip")
			return
		}
		if tag.RowsAffected() == 0 {
			writeErr(w, http.StatusNotFound, "trip not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
}
