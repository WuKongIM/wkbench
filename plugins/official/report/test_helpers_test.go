package report

import (
	"sort"
	"testing"

	"github.com/WuKongIM/wkbench/benchkit/contract"
)

func requireKinds(t *testing.T, units []contract.Unit, want []string) {
	t.Helper()
	got := make([]string, 0, len(units))
	for _, unit := range units {
		got = append(got, unit.Definition().Kind)
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("unit kinds = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unit kinds = %#v, want %#v", got, want)
		}
	}
}
