package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

type cultGate struct {
	binary   string
	workflow string
	required bool
	valid    atomic.Bool
}

func newCultGate() (*cultGate, error) {
	required := !strings.EqualFold(env("CULT_REQUIRED", "true"), "false")
	binary := env("CULT_BIN", "cult")
	workflow := env("CULT_WORKFLOW", "workflow.cult")
	if strings.TrimSpace(workflow) == "" {
		return nil, errors.New("CULT_WORKFLOW cannot be empty")
	}
	return &cultGate{binary: binary, workflow: workflow, required: required}, nil
}

func (g *cultGate) ready() bool { return g.valid.Load() }

func (g *cultGate) validate(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, g.binary, "check", g.workflow)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if !g.required {
			g.valid.Store(true)
			return nil
		}
		return fmt.Errorf("%s check %s: %w: %s", g.binary, g.workflow, err, strings.TrimSpace(string(output)))
	}
	g.valid.Store(true)
	return nil
}
