package main

import (
	"strings"
	"testing"

	"smith/internal/scaffold"
)

// The contract is only useful if it's true. The runtime list is the fact most
// likely to drift (adding a runtime touches six places per CLAUDE.md), and a
// contract that lists a runtime smith can't scaffold — or omits one it can — is
// worse than no contract, because an agent will believe it.
func TestContractRuntimesMatchScaffold(t *testing.T) {
	text := contractText()
	for _, r := range scaffold.Runtimes {
		if !strings.Contains(text, r) {
			t.Errorf("contract omits runtime %q that scaffold.Runtimes supports", r)
		}
	}
	// Guard the other direction with a runtime we deliberately do NOT support:
	// the contract must not imply it's authorable.
	if strings.Contains(text, "| node |") || strings.Contains(text, "node | ") {
		t.Error("contract lists node as an authorable runtime; scaffold.Runtimes has no node")
	}
}

// An agent that reads the contract must come away knowing the sandbox binds the
// self-test, not the tool. Getting this wrong caused a real architecture
// decision: an agent concluded a tool couldn't use a large local model, split
// its project in two to route around it, and cited "can't self-test in that box"
// as a non-negotiable constraint.
func TestContractExplainsSandboxScope(t *testing.T) {
	text := contractText()
	for _, want := range []string{
		"self-test at adopt time only",
		"full privileges",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("contract should make the sandbox's scope explicit; missing %q", want)
		}
	}
}

// The retired v1 model is still described in the repo's historical docs. An
// agent pointed at the repo can read them, so the contract must say not to.
func TestContractWarnsOffRetiredModel(t *testing.T) {
	text := contractText()
	for _, want := range []string{"toolsmith-spec.md", "retired", "promote"} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Errorf("contract should warn off the retired v1 docs; missing %q", want)
		}
	}
}

// Unbuilt limits must be labelled as unbuilt. An agent hardened "no node
// runtime" into a non-negotiable architecture constraint and let it decide the
// language of a component that wasn't even a smith tool.
func TestContractSeparatesLoadBearingFromUnbuilt(t *testing.T) {
	text := contractText()
	if !strings.Contains(text, "Load-bearing") {
		t.Error("contract must mark which limits are load-bearing")
	}
	if !strings.Contains(text, "Not load-bearing") {
		t.Error("contract must mark which limits are NOT load-bearing")
	}
	// The daemon + thin-client escape hatch is what an author needs when their
	// system genuinely doesn't fit one CLI; leaving it out is what makes people
	// think smith forbids heavy systems.
	if !strings.Contains(text, "thin client") {
		t.Error("contract must document the daemon + thin-client pattern")
	}
}

func TestContractRejectsUnknownArgs(t *testing.T) {
	p := testPaths(t)
	if err := cmdContract(p, []string{"nonsense"}); err == nil {
		t.Fatal("want an error for an unknown argument")
	}
}
