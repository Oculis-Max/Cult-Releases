package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNotFound = errors.New("memory not found")

type memory struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
	Content   string    `json:"content"`
	Approved  bool      `json:"approved"`
	CreatedAt time.Time `json:"created_at"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	Distance  *float64  `json:"distance,omitempty"`
}

type memoryStore struct {
	pool        *pgxpool.Pool
	vectorReady atomic.Bool
}

func newMemoryStore(ctx context.Context) (*memoryStore, error) {
	url := strings.TrimSpace(env("COCKROACH_URL", ""))
	if url == "" {
		return nil, errors.New("COCKROACH_URL is required")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse COCKROACH_URL: %w", err)
	}
	cfg.MaxConns = 8
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	s := &memoryStore{pool: pool}
	if err := s.ensureSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *memoryStore) close() { s.pool.Close() }
func (s *memoryStore) ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *memoryStore) vectorIndexReady() bool { return s.vectorReady.Load() }

func (s *memoryStore) ensureSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS cult_memories (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id STRING NOT NULL,
			session_id STRING NOT NULL,
			content STRING NOT NULL,
			embedding VECTOR(256) NOT NULL,
			approved BOOL NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			approved_at TIMESTAMPTZ NULL
		)`,
		`CREATE INDEX IF NOT EXISTS cult_memories_user_status_idx
			ON cult_memories (user_id, approved, created_at DESC)`,
	}
	for _, stmt := range statements {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
	}

	// Prefix columns keep vector search scoped to one user and approval state.
	// On CockroachDB 25.4+ vector indexing is enabled by default.
	_, err := s.pool.Exec(ctx, `CREATE VECTOR INDEX IF NOT EXISTS cult_memories_vector_idx
		ON cult_memories (user_id, approved, embedding)`)
	if err != nil {
		if strings.EqualFold(env("VECTOR_INDEX_STRICT", "false"), "true") {
			return fmt.Errorf("create vector index: %w", err)
		}
		s.vectorReady.Store(false)
		return nil
	}
	s.vectorReady.Store(true)
	return nil
}

func (s *memoryStore) propose(ctx context.Context, userID, sessionID, content string, embedding []float64) (memory, error) {
	if len(embedding) != 256 {
		return memory{}, fmt.Errorf("expected 256-dimensional embedding, got %d", len(embedding))
	}
	var m memory
	err := s.pool.QueryRow(ctx, `
		INSERT INTO cult_memories (user_id, session_id, content, embedding, approved)
		VALUES ($1, $2, $3, $4::VECTOR(256), false)
		RETURNING id::STRING, user_id, session_id, content, approved, created_at`,
		userID, sessionID, strings.TrimSpace(content), vectorLiteral(embedding),
	).Scan(&m.ID, &m.UserID, &m.SessionID, &m.Content, &m.Approved, &m.CreatedAt)
	return m, err
}

func (s *memoryStore) recall(ctx context.Context, userID string, embedding []float64, limit int) ([]memory, error) {
	if len(embedding) != 256 {
		return nil, fmt.Errorf("expected 256-dimensional embedding, got %d", len(embedding))
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::STRING, user_id, session_id, content, approved, created_at, approved_at,
		       embedding <-> $2::VECTOR(256) AS distance
		FROM cult_memories
		WHERE user_id = $1 AND approved = true
		ORDER BY embedding <-> $2::VECTOR(256)
		LIMIT $3`, userID, vectorLiteral(embedding), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]memory, 0, limit)
	for rows.Next() {
		var m memory
		var distance float64
		if err := rows.Scan(&m.ID, &m.UserID, &m.SessionID, &m.Content, &m.Approved, &m.CreatedAt, &m.ApprovedAt, &distance); err != nil {
			return nil, err
		}
		m.Distance = &distance
		items = append(items, m)
	}
	return items, rows.Err()
}

func (s *memoryStore) list(ctx context.Context, userID string, limit int) ([]memory, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::STRING, user_id, session_id, content, approved, created_at, approved_at
		FROM cult_memories
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]memory, 0, limit)
	for rows.Next() {
		var m memory
		if err := rows.Scan(&m.ID, &m.UserID, &m.SessionID, &m.Content, &m.Approved, &m.CreatedAt, &m.ApprovedAt); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

func (s *memoryStore) approve(ctx context.Context, userID, id string) (memory, error) {
	var m memory
	err := s.pool.QueryRow(ctx, `
		UPDATE cult_memories
		SET approved = true, approved_at = now()
		WHERE id = $1::UUID AND user_id = $2 AND approved = false
		RETURNING id::STRING, user_id, session_id, content, approved, created_at, approved_at`, id, userID,
	).Scan(&m.ID, &m.UserID, &m.SessionID, &m.Content, &m.Approved, &m.CreatedAt, &m.ApprovedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return memory{}, errNotFound
	}
	return m, err
}

func (s *memoryStore) reject(ctx context.Context, userID, id string) (memory, error) {
	var m memory
	err := s.pool.QueryRow(ctx, `
		DELETE FROM cult_memories
		WHERE id = $1::UUID AND user_id = $2 AND approved = false
		RETURNING id::STRING, user_id, session_id, content, approved, created_at, approved_at`, id, userID,
	).Scan(&m.ID, &m.UserID, &m.SessionID, &m.Content, &m.Approved, &m.CreatedAt, &m.ApprovedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return memory{}, errNotFound
	}
	return m, err
}

func (s *memoryStore) validateRetrievalPlan(ctx context.Context, skill *cockroachSkill) error {
	if skill == nil || !skill.ready() || !skill.requiresExplain() {
		return nil
	}
	zero := make([]float64, 256)
	rows, err := s.pool.Query(ctx, `EXPLAIN SELECT id, content
		FROM cult_memories
		WHERE user_id = $1 AND approved = true
		ORDER BY embedding <-> $2::VECTOR(256)
		LIMIT 5`, "cult-skill-plan-check", vectorLiteral(zero))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
	}
	return rows.Err()
}

func vectorLiteral(values []float64) string {
	var b strings.Builder
	b.Grow(len(values) * 8)
	b.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(value, 'g', -1, 64))
	}
	b.WriteByte(']')
	return b.String()
}
