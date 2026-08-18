package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*
var webFS embed.FS

type app struct {
	store    *memoryStore
	bedrock  *bedrockClient
	cult     *cultGate
	skill    *cockroachSkill
	started  time.Time
}

type chatRequest struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

type chatResponse struct {
	Answer         string   `json:"answer"`
	Proposal       *memory  `json:"proposal,omitempty"`
	Recalled       []memory `json:"recalled"`
	CultValidated  bool     `json:"cult_validated"`
	SkillValidated bool     `json:"skill_validated"`
}

type decisionRequest struct {
	UserID string `json:"user_id"`
}

func main() {
	ctx := context.Background()

	cult, err := newCultGate()
	if err != nil {
		log.Fatalf("cult gate: %v", err)
	}
	if err := cult.validate(ctx); err != nil {
		log.Fatalf("cult workflow validation failed: %v", err)
	}

	store, err := newMemoryStore(ctx)
	if err != nil {
		log.Fatalf("cockroachdb: %v", err)
	}
	defer store.close()

	skill := newCockroachSkill()
	if err := skill.load(ctx); err != nil {
		log.Printf("cockroach skill warning: %v", err)
	} else if err := store.validateRetrievalPlan(ctx, skill); err != nil {
		log.Printf("cockroach skill EXPLAIN warning: %v", err)
	}

	bedrock, err := newBedrockClient()
	if err != nil {
		log.Fatalf("bedrock: %v", err)
	}

	a := &app{store: store, bedrock: bedrock, cult: cult, skill: skill, started: time.Now().UTC()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("POST /api/chat", a.chat)
	mux.HandleFunc("GET /api/memories", a.memories)
	mux.HandleFunc("POST /api/memories/{id}/approve", a.approveMemory)
	mux.HandleFunc("POST /api/memories/{id}/reject", a.rejectMemory)

	static, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(static)))

	port := env("PORT", "8080")
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           requestLog(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	log.Printf("Cult Memory listening on :%s", port)
	log.Fatal(server.ListenAndServe())
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	dbOK := a.store.ping(ctx) == nil
	status := http.StatusOK
	if !dbOK || !a.cult.ready() || !a.bedrock.ready() {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"ok":                    status == http.StatusOK,
		"cult_workflow_valid":   a.cult.ready(),
		"cockroachdb_connected": dbOK,
		"vector_index":          a.store.vectorIndexReady(),
		"agent_skill_loaded":    a.skill.ready(),
		"bedrock_configured":    a.bedrock.ready(),
		"uptime_seconds":        int(time.Since(a.started).Seconds()),
	})
}

func (a *app) chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Message = strings.TrimSpace(req.Message)
	if req.UserID == "" || req.SessionID == "" || req.Message == "" {
		writeError(w, http.StatusBadRequest, errors.New("user_id, session_id and message are required"))
		return
	}
	if len(req.Message) > 8000 {
		writeError(w, http.StatusBadRequest, errors.New("message is too long"))
		return
	}
	if !a.cult.ready() {
		writeError(w, http.StatusServiceUnavailable, errors.New("Cult workflow gate is not validated"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()

	queryVector, err := a.bedrock.embed(ctx, req.Message)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("embed query: %w", err))
		return
	}

	recalled, err := a.store.recall(ctx, req.UserID, queryVector, 5)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("recall memory: %w", err))
		return
	}

	result, err := a.bedrock.respond(ctx, req.Message, recalled)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("agent response: %w", err))
		return
	}

	var proposal *memory
	if result.ShouldRemember && strings.TrimSpace(result.ProposedMemory) != "" {
		proposalVector, err := a.bedrock.embed(ctx, result.ProposedMemory)
		if err != nil {
			writeError(w, http.StatusBadGateway, fmt.Errorf("embed proposed memory: %w", err))
			return
		}
		m, err := a.store.propose(ctx, req.UserID, req.SessionID, result.ProposedMemory, proposalVector)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("propose memory: %w", err))
			return
		}
		proposal = &m
	}

	writeJSON(w, http.StatusOK, chatResponse{
		Answer:         result.Answer,
		Proposal:       proposal,
		Recalled:       recalled,
		CultValidated:  a.cult.ready(),
		SkillValidated: a.skill.ready(),
	})
}

func (a *app) memories(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID == "" {
		writeError(w, http.StatusBadRequest, errors.New("user_id is required"))
		return
	}
	limit := 30
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	items, err := a.store.list(r.Context(), userID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": items})
}

func (a *app) approveMemory(w http.ResponseWriter, r *http.Request) {
	a.decideMemory(w, r, true)
}

func (a *app) rejectMemory(w http.ResponseWriter, r *http.Request) {
	a.decideMemory(w, r, false)
}

func (a *app) decideMemory(w http.ResponseWriter, r *http.Request, approve bool) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("memory id is required"))
		return
	}
	var req decisionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.UserID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("user_id is required"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var (
		m   memory
		err error
	)
	if approve {
		m, err = a.store.approve(ctx, req.UserID, id)
	} else {
		m, err = a.store.reject(ctx, req.UserID, id)
	}
	if err != nil {
		if errors.Is(err, errNotFound) {
			writeError(w, http.StatusNotFound, err)
		} else {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
