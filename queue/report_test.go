package queue

import (
	"context"
	"slices"
	"testing"
	"time"

	"lotusforge.au/api-server/logging"
)

// runFunctionTask runs one function task of the given type through work and returns
// the status it finished with
func runFunctionTask(t *testing.T, task_type string, fn func(context.Context, ...any) (string, map[string]any, error)) string {
	t.Helper()
	fp := &fakePublisher{}
	qm := NewQueue(nil, 1, logging.Basic(), fp)

	w := newWorker(0)
	w.lock()
	task := &Task{Ctx: context.Background(), TaskID: "t1", TaskType: task_type, ExecType: "function", Function: fn, Status: "start", Topic: "table:test"}
	w.TaskActioning = task
	qm.work(w)

	if len(fp.events) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(fp.events))
	}
	return fp.events[0].status
}

// A diff returns its action as the task type and an int success count, as createDiff does
func TestWork_DiffReportsSuccess(t *testing.T) {
	status := runFunctionTask(t, "create", func(context.Context, ...any) (string, map[string]any, error) {
		return "create", map[string]any{"total_count": 1, "success_count": 1, "failed_count": 0}, nil
	})
	if status != "success" {
		t.Errorf("expected success, got %q", status)
	}
}

func TestWork_NothingSucceededReportsFailed(t *testing.T) {
	status := runFunctionTask(t, "create", func(context.Context, ...any) (string, map[string]any, error) {
		return "create", map[string]any{"total_count": 1, "success_count": 0, "failed_count": 1}, nil
	})
	if status != "failed" {
		t.Errorf("expected failed, got %q", status)
	}
}

// The function's type must match the task it was queued as, or the task errors
func TestWork_MismatchedTaskTypeReportsError(t *testing.T) {
	status := runFunctionTask(t, "create", func(context.Context, ...any) (string, map[string]any, error) {
		return "create_diff", map[string]any{"success_count": 1}, nil
	})
	if status != "error" {
		t.Errorf("expected error, got %q", status)
	}
}

// A panicking task reports an error instead of crashing, and the worker carries on
// with the next queued task
func TestWork_PanicReportsErrorAndWorkerContinues(t *testing.T) {
	fp := &fakePublisher{}
	qm := NewQueue(nil, 1, logging.Basic(), fp)

	ok := func(context.Context, ...any) (string, map[string]any, error) {
		return "create", map[string]any{"success_count": int64(1)}, nil
	}
	qm.tasks = append(qm.tasks, &Task{Ctx: context.Background(), TaskID: "t2", TaskType: "create", ExecType: "function", Function: ok, Status: "queued", Topic: "table:test"})

	w := qm.getFreeWorker()
	w.TaskActioning = &Task{Ctx: context.Background(), TaskID: "t1", TaskType: "create", ExecType: "function", Status: "start", Topic: "table:test",
		Function: func(context.Context, ...any) (string, map[string]any, error) { panic("boom") }}
	qm.work(w)

	// t1 errors, then t2 starts and succeeds on the same worker
	want := []string{"error", "start", "success"}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(fp.statuses()) < len(want) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := fp.statuses(); !slices.Equal(got, want) {
		t.Fatalf("expected statuses %v, got %v", want, got)
	}

	// The worker is freed once the queue is empty
	for time.Now().Before(deadline) {
		qm.mu.Lock()
		free := qm.active_workers == 0 && !w.islocked()
		qm.mu.Unlock()
		if free { return }
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("expected the worker to be freed")
}
