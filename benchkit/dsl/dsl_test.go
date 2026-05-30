package dsl_test

import (
	"strings"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/dsl"
)

func TestParseExpandsWholeValueVariablesWithOriginalType(t *testing.T) {
	scenario, err := dsl.Parse(strings.NewReader(`
version: wkbench/v2
run:
  id: demo
  duration: 15s
vars:
  users: 4096
  rate: 500/s
units:
  identities:
    use: identity.pool
    spec:
      total: ${users}
  traffic:
    use: traffic.group_send
    spec:
      rate: ${rate}
`))
	if err != nil {
		t.Fatalf("parse scenario: %v", err)
	}
	if got := scenario.Units["identities"].Spec["total"]; got != 4096 {
		t.Fatalf("expected integer variable expansion, got %#v", got)
	}
	if got := scenario.Units["traffic"].Spec["rate"]; got != "500/s" {
		t.Fatalf("expected string variable expansion, got %#v", got)
	}
}

func TestParseRejectsUnknownVariable(t *testing.T) {
	_, err := dsl.Parse(strings.NewReader(`
version: wkbench/v2
run:
  id: demo
units:
  traffic:
    use: traffic.group_send
    spec:
      rate: ${missing}
`))
	if err == nil {
		t.Fatal("expected unknown variable error")
	}
}
