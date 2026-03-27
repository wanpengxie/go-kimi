package subagents

import (
	"strings"
	"testing"
)

func TestLaborMarketRegisterGetRequireList(t *testing.T) {
	t.Parallel()

	market := NewLaborMarket()
	market.Register(&AgentTypeDefinition{
		Name:        "writer",
		Description: "drafts content",
		WhenToUse:   "when wording matters",
		ToolPolicy: ToolPolicy{
			Mode: ToolPolicyInherit,
		},
	})
	market.Register(&AgentTypeDefinition{
		Name:        "planner",
		Description: "plans execution",
		WhenToUse:   "when decomposition is needed",
		ToolPolicy: ToolPolicy{
			Mode:      ToolPolicyAllowlist,
			Allowlist: []string{"shell", "think"},
		},
	})

	got, ok := market.Get(" planner ")
	if !ok {
		t.Fatal("Get(planner) ok = false, want true")
	}
	if got.Name != "planner" {
		t.Fatalf("Get(planner).Name = %q, want planner", got.Name)
	}
	if got.ToolPolicy.Mode != ToolPolicyAllowlist {
		t.Fatalf("Get(planner).ToolPolicy.Mode = %q, want %q", got.ToolPolicy.Mode, ToolPolicyAllowlist)
	}

	got.ToolPolicy.Allowlist[0] = "mutated"
	refetched, ok := market.Get("planner")
	if !ok {
		t.Fatal("Get(planner) second time ok = false, want true")
	}
	if refetched.ToolPolicy.Allowlist[0] != "shell" {
		t.Fatalf("Get(planner) should return cloned allowlist, got %q", refetched.ToolPolicy.Allowlist[0])
	}

	defs := market.List()
	if len(defs) != 2 {
		t.Fatalf("len(List()) = %d, want 2", len(defs))
	}
	if defs[0].Name != "planner" || defs[1].Name != "writer" {
		t.Fatalf("List() names = [%q %q], want [planner writer]", defs[0].Name, defs[1].Name)
	}

	required, err := market.Require("writer")
	if err != nil {
		t.Fatalf("Require(writer) error = %v", err)
	}
	if required.Name != "writer" {
		t.Fatalf("Require(writer).Name = %q, want writer", required.Name)
	}
}

func TestLaborMarketRequireMissing(t *testing.T) {
	t.Parallel()

	market := NewLaborMarket()
	_, err := market.Require("missing")
	if err == nil {
		t.Fatal("Require(missing) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Require(missing) error = %v, want not found", err)
	}
}

func TestLaborMarketRegisterIgnoresInvalidDefinitions(t *testing.T) {
	t.Parallel()

	market := NewLaborMarket()
	market.Register(nil)
	market.Register(&AgentTypeDefinition{Name: "   "})

	if defs := market.List(); len(defs) != 0 {
		t.Fatalf("len(List()) = %d, want 0", len(defs))
	}

	var nilMarket *LaborMarket
	if got := nilMarket.List(); got != nil {
		t.Fatalf("nil market List() = %#v, want nil", got)
	}
	if _, ok := nilMarket.Get("planner"); ok {
		t.Fatal("nil market Get() ok = true, want false")
	}
}
