package plan

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// PlanState stores one soul-local plan mode state.
type PlanState struct {
	Active   bool
	PlanFile string

	mu sync.RWMutex
}

// NewPlanState creates one empty PlanState.
func NewPlanState() *PlanState {
	return &PlanState{}
}

// IsActive reports whether plan mode is currently active.
func (p *PlanState) IsActive() bool {
	if p == nil {
		return false
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Active
}

// Enter activates plan mode and records the current plan file path.
func (p *PlanState) Enter(planFile string) error {
	if p == nil {
		return errors.New("plan state: nil")
	}

	planFile = strings.TrimSpace(planFile)
	if planFile == "" {
		return errors.New("plan state: plan file is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.Active {
		return errors.New("plan state: already active")
	}
	p.Active = true
	p.PlanFile = planFile
	return nil
}

// Exit reads current plan file content and deactivates plan mode.
func (p *PlanState) Exit() (string, error) {
	if p == nil {
		return "", errors.New("plan state: nil")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.Active {
		return "", errors.New("plan state: not active")
	}

	planFile := strings.TrimSpace(p.PlanFile)
	if planFile == "" {
		return "", errors.New("plan state: plan file is not set")
	}

	data, err := os.ReadFile(planFile)
	if err != nil {
		return "", fmt.Errorf("plan state: read %q: %w", planFile, err)
	}

	p.Active = false
	p.PlanFile = ""
	return string(data), nil
}
