package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
)

type Task struct {
	TaskID       string                                                `json:"task_id"`
	TaskType     string                                                `json:"task_type"`
	ExecType     string                                                `json:"-"`
	Sql          string                                                `json:"sql"`
	StartTime    time.Time                                             `json:"start_time"`
	CompleteTime time.Time                                             `json:"complete_time"`
	Status       string                                                `json:"status"`
	Success      bool                                                  `json:"success"`
	Response     map[string]any                                        `json:"response"`
	Ctx          context.Context                                       `json:"-"`
	Note         string                                                `json:"note"`
	Function     func(context.Context, ...any) (map[string]any, error) `json:"-"`
	Args         []any                                                 `json:"-"`
	Cfg          models.DataModel                                      `json:"-"`
}

type QueueManager struct {
	tasks          []*Task
	workers        []*Worker
	workers_count  int
	active_workers int
	Db             *pgxpool.Pool
	Logger         *slog.Logger
	mu             sync.Mutex
	eventHub       *EventManager
}

type Worker struct {
	ID            int
	Lock          bool
	TaskActioning *Task
}

func newWorker(id int) *Worker {
	return &Worker{ID: id}
}

// NewQueue creates a new async task queue with the given number of workers.
func NewQueue(db *pgxpool.Pool, num_workers int, logger *slog.Logger, evh *EventManager) *QueueManager {
	workers := make([]*Worker, num_workers)
	for i := range num_workers {
		workers[i] = newWorker(i)
	}
	return &QueueManager{
		tasks:          []*Task{},
		Db:             db,
		workers:        workers,
		active_workers: 0,
		workers_count:  num_workers,
		Logger:         logger,
		eventHub:       evh,
	}
}

func (qm *QueueManager) createTask(ctx context.Context, sql string, note string, args ...any) (*Task, error) {
	task, ok := middleware.GetTask(ctx)
	if !ok {
		return nil, fmt.Errorf("Task information was not saved into the context")
	}
	t := &Task{
		TaskID:    task.Id,
		TaskType:  task.Type,
		ExecType:  "exec",
		StartTime: time.Now(),
		Status:    "queued",
		Ctx:       ctx,
		Args:      args,
		Note:      note,
		Sql:       sql,
		Cfg:       *task.Cfg,
	}
	qm.logDatabaseEvent(ctx, "TASK_CREATE", t.logMiniUnsafe())
	return t, nil
}

func (qm *QueueManager) createFunctionTask(ctx context.Context, function func(context.Context, ...any) (map[string]any, error), note string, args ...any) (*Task, error) {
	task, ok := middleware.GetTask(ctx)
	if !ok {
		return nil, fmt.Errorf("Task information was not saved into the context")
	}
	t := &Task{
		TaskID:    task.Id,
		TaskType:  task.Type,
		ExecType:  "function",
		StartTime: time.Now(),
		Status:    "queued",
		Ctx:       ctx,
		Note:      note,
		Args:      args,
		Function:  function,
		Cfg:       *task.Cfg,
	}
	qm.logDatabaseEvent(ctx, "TASK_CREATE", t.logMiniUnsafe())
	return t, nil
}

func (qm *QueueManager) reportWork(w *Worker, status string, res map[string]any) {
	qm.mu.Lock()
	w.TaskActioning.Response = res
	w.TaskActioning.CompleteTime = time.Now()

	switch status {
	case "error":
		w.TaskActioning.Status = "error"
	case "success":
		w.TaskActioning.Status = "complete"
	}

	// Prepare log data
	log, err := w.logTaskUnsafe()
	if err != nil {
		qm.Logger.Error("Failed to build event data", "status", w.TaskActioning.Status, "worker", w.ID, "task_id", w.TaskActioning.TaskID, "task_type", w.TaskActioning.TaskType, "error", w.TaskActioning.Response, "log_error", err, "user", w.TaskActioning.Ctx.Value(middleware.Contextkey("user")).(*models.User).Username)
		qm.mu.Unlock()
	} else {
		qm.mu.Unlock()
		qm.logDatabaseEvent(w.TaskActioning.Ctx, "TASK_COMPLETE", log)
	}

	// Publish notification
	qm.eventHub.PublishNoTimestampPayload(w.TaskActioning.Ctx, w.TaskActioning.TaskType, w.TaskActioning.Status, fmt.Sprintf("table:%s", *w.TaskActioning.Cfg.Table_name), log)

}

func (qm *QueueManager) getReportableTaskInfo(w *Worker) map[string]any {
	switch w.TaskActioning.Status {
	case "error":
		return map[string]any{
			"task_id": w.TaskActioning.TaskID,
			"task_type": w.TaskActioning.TaskType,
			"task_start": w.TaskActioning.StartTime,
			"task_end": w.TaskActioning.CompleteTime,
			"task_status": w.TaskActioning.Status,
			"task_error": w.TaskActioning.Response,
		}
	case "complete":
		return map[string]any{
			"task_id": w.TaskActioning.TaskID,
			"task_type": w.TaskActioning.TaskType,
			"task_start": w.TaskActioning.StartTime,
			"task_end": w.TaskActioning.CompleteTime,
			"task_status": w.TaskActioning.Status,
			"task_response": w.TaskActioning.Response,
		}
	case "queued":
		return map[string]any{
			"task_id": w.TaskActioning.TaskID,
			"task_type": w.TaskActioning.TaskType,
			"task_start": w.TaskActioning.StartTime,
			"task_status": w.TaskActioning.Status,
		}
	}
	return map[string]any{
		"task_id": w.TaskActioning.TaskID,
		"task_type": w.TaskActioning.TaskType,
		"task_start": w.TaskActioning.StartTime,
		"task_status": w.TaskActioning.Status,
	}
}


func (qm *QueueManager) lookForTask() *Task {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	if len(qm.tasks) < 1 {
		return nil
	}

	t := qm.tasks[0]
	qm.tasks = qm.tasks[1:]

	if len(qm.tasks) > 0 && cap(qm.tasks) > 64 && len(qm.tasks) < cap(qm.tasks)/4 {
		newTasks := make([]*Task, len(qm.tasks))
		copy(newTasks, qm.tasks)
		qm.tasks = newTasks
	}

	return t
}

func (qm *QueueManager) work(w *Worker) {
	qm.mu.Lock()

	if !w.islocked() || w.TaskActioning.Status != "processing" {
		qm.mu.Unlock()
		return
	}

	ctx := w.TaskActioning.Ctx
	sql := w.TaskActioning.Sql
	args := w.TaskActioning.Args
	exec_type := w.TaskActioning.ExecType
	task_func := w.TaskActioning.Function

	qm.mu.Unlock()

	switch exec_type {
	case "exec":
		cmdtag, err := qm.Db.Exec(ctx, sql, args...)
		if err != nil {
			qm.reportWork(w, "error", map[string]any{"error": err.Error()})
		} else {
			bef, _, _ := strings.Cut(cmdtag.String(), " ")
			qm.reportWork(w, "success", map[string]any{
				"action":        bef,
				"rows_affected": cmdtag.RowsAffected(),
			})
		}

	case "function":
		func_response, err := task_func(ctx, args...)
		if err != nil {
			qm.reportWork(w, "error", map[string]any{"error": err.Error()})
		} else {
			qm.reportWork(w, "success", func_response)
		}
	}


	t := qm.lookForTask()
	if t == nil {
		qm.mu.Lock()
		qm.active_workers--
		w.free()
		qm.mu.Unlock()
		return
	}
	qm.assignWorker(w, t)
}

func (qm *QueueManager) getFreeWorker() (w *Worker) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	if qm.active_workers < qm.workers_count {
		for _, worker := range qm.workers {
			if !worker.islocked() {
				worker.lock()
				qm.active_workers++
				return worker
			}
		}
	}
	return nil
}

func (qm *QueueManager) assignWorker(w *Worker, t *Task) {
	qm.mu.Lock()
	w.TaskActioning = nil
	w.TaskActioning = t
	t.Status = "processing"
	qm.mu.Unlock()
	go qm.work(w)
}

func (qm *QueueManager) logDatabaseEvent(ctx context.Context, event string, log []byte) {
	go func() {
		query := `INSERT INTO events (type, event_log, event_user) VALUES ($1, $2, $3)`
		_, err := qm.Db.Exec(ctx, query, event, log, ctx.Value(middleware.Contextkey("user")).(*models.User).Username)
		if err != nil && qm.Logger != nil {
			qm.Logger.Error("Failed to log event", "error", err, "event", event, "user", ctx.Value(middleware.Contextkey("user")).(*models.User).Username, "log", log)
		}
	}()
}

func (qm *QueueManager) queue(t *Task) (string, error) {
	worker := qm.getFreeWorker()

	qm.mu.Lock()
	if worker != nil {
		if t != nil {
			qm.mu.Unlock()
			qm.assignWorker(worker, t)
		} else {
			qm.Logger.Error("Queue was called without a valid task")
			qm.mu.Unlock()
			return "", errors.New("No valid task was supplied to queue")
		}
	} else {
		qm.tasks = append(qm.tasks, t)
		qm.mu.Unlock()
	}
	return t.TaskID, nil
}

// QueueFunction queues a function to run asynchronously. Returns a 32-character task ID.
func (qm *QueueManager) QueueFunction(ctx context.Context, function func(context.Context, ...any) (map[string]any, error), note string, args ...any) (string, error) {
	t, err := qm.createFunctionTask(ctx, function, note, args...)
	if err != nil {
		return "", err
	}
	return qm.queue(t)
}

// QueueExec queues a SQL exec to run asynchronously. Returns a 32-character task ID.
func (qm *QueueManager) QueueExec(ctx context.Context, sql string, note string, args ...any) (string, error) {
	t, err := qm.createTask(ctx, sql, note, args...)
	if err != nil {
		return "", err
	}
	return qm.queue(t)
}

func (qm *QueueManager) getTaskPosUnsafe(identifer string) (int, bool) {
	for index, item := range qm.tasks {
		if item.TaskID == identifer {
			return index, true
		}
	}
	return 0, false
}

func (qm *QueueManager) GetTaskStatus(identifier string) (string, bool) {
	task_pos, exists := qm.getTaskPosUnsafe(identifier)
	if exists {
		return qm.tasks[task_pos].Status, true
	}
	return "", false
}

func (w *Worker) lock()     { w.Lock = true }
func (w *Worker) islocked() bool { return w.Lock }
func (w *Worker) free() {
	w.TaskActioning = nil
	w.Lock = false
}

func (t *Task) logMiniUnsafe() []byte {
	logData := map[string]any{
		"task": map[string]any{
			"task_id":    t.TaskID,
			"task_type":  t.TaskType,
			"status":     t.Status,
			"start_time": t.StartTime,
			"note":       t.Note,
		},
	}
	log, _ := json.Marshal(logData)
	return log
}

func (w *Worker) logTaskUnsafe() ([]byte, error) {
	logsData := map[string]interface{}{
		"worker": w.ID,
		"task": map[string]interface{}{
			"task_id":       w.TaskActioning.TaskID,
			"task_type":     w.TaskActioning.TaskType,
			"status":        w.TaskActioning.Status,
			"start_time":    w.TaskActioning.StartTime,
			"complete_time": w.TaskActioning.CompleteTime,
			"num_args":      len(w.TaskActioning.Args),
			"note":          w.TaskActioning.Note,
			"response":      w.TaskActioning.Response,
		},
	}
	return json.Marshal(logsData)
}
