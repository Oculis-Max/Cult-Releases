package main

import (
	"strings"
	"testing"
)

func TestVectorLiteral(t *testing.T) {
	got := vectorLiteral([]float64{1, 0.25, -2.5})
	if got != "[1,0.25,-2.5]" {
		t.Fatalf("vectorLiteral() = %q", got)
	}
}

func TestStripJSONFence(t *testing.T) {
	got := stripJSONFence("```json\n{\"answer\":\"ok\"}\n```")
	if got != "{\"answer\":\"ok\"}" {
		t.Fatalf("stripJSONFence() = %q", got)
	}
}

func TestSkillExplainDetection(t *testing.T) {
	s := &cockroachSkill{content: "ALWAYS run EXPLAIN on every generated SQL query"}
	s.loaded.Store(true)
	if !s.requiresExplain() {
		t.Fatal("expected EXPLAIN guidance to be detected")
	}
}

func TestMemoryPromptFenceDoesNotCreateMemory(t *testing.T) {
	text := stripJSONFence("```json\n{\"answer\":\"hello\",\"should_remember\":false,\"proposed_memory\":\"\"}\n```")
	if !strings.Contains(text, "should_remember") {
		t.Fatalf("unexpected stripped content: %q", text)
	}
}
