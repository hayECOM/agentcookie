package cli

import (
	"testing"

	"github.com/mvanhorn/agentcookie/internal/sinkpush"
)

func TestAdapterPushedLine(t *testing.T) {
	cases := []struct {
		r    sinkpush.Result
		want string
	}{
		{sinkpush.Result{Name: "instacart", Pushed: 4}, "adapter instacart pushed 4 cookies"},
		{sinkpush.Result{Name: "instacart", Pushed: 4, Invalid: 1}, "adapter instacart pushed 4 cookies (1 invalid dropped)"},
		// A delta push that sent nothing must say so, not claim the set.
		{sinkpush.Result{Name: "cmux", Unchanged: 312}, "adapter cmux pushed 0 changed of 312 (312 unchanged, 0 rejected)"},
		{sinkpush.Result{Name: "cmux", Pushed: 3, Unchanged: 307, Rejected: 2}, "adapter cmux pushed 3 changed of 312 (307 unchanged, 2 rejected)"},
	}
	for _, c := range cases {
		if got := adapterPushedLine(c.r); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}
