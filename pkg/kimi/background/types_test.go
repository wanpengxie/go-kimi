package background

import "testing"

func TestTaskStatusIsTerminal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		status TaskStatus
		want   bool
	}{
		{status: TaskCreated, want: false},
		{status: TaskStarting, want: false},
		{status: TaskRunning, want: false},
		{status: TaskAwaitingApproval, want: false},
		{status: TaskCompleted, want: true},
		{status: TaskFailed, want: true},
		{status: TaskKilled, want: true},
		{status: TaskStatus("custom"), want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.status), func(t *testing.T) {
			t.Parallel()
			if got := tc.status.IsTerminal(); got != tc.want {
				t.Fatalf("IsTerminal(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
