package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const defaultCockroachSkillURL = "https://raw.githubusercontent.com/cockroachdb/claude-plugin/6c96c6394a61f366e8ec1b7cec2281e97507cbff/skills/cockroachdb-query-and-schema-design/cockroachdb-sql/SKILL.md"

type cockroachSkill struct {
	url     string
	content string
	loaded  atomic.Bool
}

func newCockroachSkill() *cockroachSkill {
	return &cockroachSkill{url: env("COCKROACH_AGENT_SKILL_URL", defaultCockroachSkillURL)}
}

func (s *cockroachSkill) ready() bool { return s != nil && s.loaded.Load() }

func (s *cockroachSkill) requiresExplain() bool {
	if !s.ready() {
		return false
	}
	return strings.Contains(strings.ToUpper(s.content), "ALWAYS RUN EXPLAIN")
}

func (s *cockroachSkill) load(ctx context.Context) error {
	if strings.TrimSpace(s.url) == "" {
		return errors.New("CockroachDB Agent Skill URL is empty")
	}
	loadCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(loadCtx, http.MethodGet, s.url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CockroachDB Agent Skill returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	content := string(data)
	if !strings.Contains(content, "name: cockroachdb-sql") {
		return errors.New("unexpected CockroachDB Agent Skill content")
	}
	s.content = content
	s.loaded.Store(true)
	return nil
}
