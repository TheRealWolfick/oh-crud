package tools

import (
	"context"
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

// -----------------------------------------------------------------
//              Queue data types
// -----------------------------------------------------------------

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
	Topic        string                                                `json:"-"`
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

// -----------------------------------------------------------------
//              Task Creation and Management
// -----------------------------------------------------------------

// This creates a task to execute a sql command asyncronously.
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
		Topic:     fmt.Sprintf("table:%s", *task.Cfg.Table_name),
	}
	return t, nil
}

// This creates a task to execute an arbitrary function asyncronously.
// The scheduled function must return a payload of data in the form of a map as well as an error status
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
		Topic:     fmt.Sprintf("table:%s", *task.Cfg.Table_name),
	}
	return t, nil
}

// This function is used to look for an available task. If one is found, it will be returned.
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

// UNSAFE - Returns the position of a task based on its id
func (qm *QueueManager) getTaskPosUnsafe(identifer string) (int, bool) {
	for index, item := range qm.tasks {
		if item.TaskID == identifer {
			return index, true
		}
	}
	return 0, false
}

// Get the status of a current task
func (qm *QueueManager) GetTaskStatus(identifier string) (string, bool) {
	task_pos, exists := qm.getTaskPosUnsafe(identifier)
	if exists {
		return qm.tasks[task_pos].Status, true
	}
	return "", false
}

// -----------------------------------------------------------------
//              Reporting
// -----------------------------------------------------------------

// This function is used to report work at the end of a task
func (qm *QueueManager) reportWork(w *Worker, status string, res map[string]any, fail bool) {
	qm.mu.Lock()
	w.TaskActioning.Response = res
	w.TaskActioning.CompleteTime = time.Now()
	qm.mu.Unlock()

	switch status {
	case "error":
		w.TaskActioning.Status = "error"
	case "success":
		if fail {
			w.TaskActioning.Status = "fail"
		} else {
			inner_err, ieok := res["errors"]
			inner_suc, isok := res["success_count"]
			if ieok && isok {
				if inner_suc.(float64) == 0 {
					w.TaskActioning.Status = "fail"
				} else {
					if len(inner_err.([]any)) > 0 {
						w.TaskActioning.Status = "warn"
					} else {
						w.TaskActioning.Status = "success"
					}
				}
			} else {
				w.TaskActioning.Status = "success"
			}
		}
	}

	// Publish notification
	qm.publish(w)
}

func (qm *QueueManager) getReportableTaskInfo(w *Worker) map[string]any {
	switch w.TaskActioning.Status {
	case "error", "fail":
		return map[string]any{
			"task_id":       w.TaskActioning.TaskID,
			"task_type":     w.TaskActioning.TaskType,
			"task_start":    w.TaskActioning.StartTime,
			"task_end":      w.TaskActioning.CompleteTime,
			"note":          w.TaskActioning.Note,
			"task_status":   w.TaskActioning.Status,
			"task_error":    w.TaskActioning.Response,
		}
	case "success", "warn":
		return map[string]any{
			"task_id":       w.TaskActioning.TaskID,
			"task_type":     w.TaskActioning.TaskType,
			"task_start":    w.TaskActioning.StartTime,
			"task_end":      w.TaskActioning.CompleteTime,
			"note":          w.TaskActioning.Note,
			"task_status":   w.TaskActioning.Status,
			"task_response": w.TaskActioning.Response,
		}
	case "start":
		return map[string]any{
			"task_id":       w.TaskActioning.TaskID,
			"task_type":     w.TaskActioning.TaskType,
			"note":          w.TaskActioning.Note,
			"task_start":    w.TaskActioning.StartTime,
			"task_status":   w.TaskActioning.Status,
		}
	}
	return map[string]any{
		"task_id":         w.TaskActioning.TaskID,
		"task_type":       w.TaskActioning.TaskType,
		"note":            w.TaskActioning.Note,
		"task_start":      w.TaskActioning.StartTime,
		"task_status":     w.TaskActioning.Status,
	}
}

// This is an internal helper function to publish an event
func (qm *QueueManager) publish(w *Worker) {
	switch w.TaskActioning.Status {
	case "queued": 
		qm.publishTaskQueued(w.TaskActioning)
	case "start":
		qm.publishTaskStart(w.TaskActioning)
	case "success":
		qm.publishTaskSuccess(w.TaskActioning)
	case "warn":
		qm.publishTaskWarn(w.TaskActioning)
	case "fail":
		qm.publishTaskFail(w.TaskActioning)
	case "error":
		qm.publishTaskError(w.TaskActioning)
	}
	qm.eventHub.PublishNoTimestamp(w.TaskActioning.Ctx, w.TaskActioning.TaskType, w.TaskActioning.Status, w.TaskActioning.Topic, qm.getReportableTaskInfo(w))
}
// This is an internal helper function for publishing an event when there is no worker assigned, 
// instead utilizing the task directly
func (qm *QueueManager) publishTask(t *Task) {
	qm.eventHub.PublishNoTimestamp(t.Ctx, t.TaskType, t.Status, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"note":          t.Note,
		"task_start":    t.StartTime,
		"task_status":   t.Status,
	})
}
func (qm *QueueManager) publishTaskQueued(t *Task) {
	qm.eventHub.PublishNoTimestamp(t.Ctx, t.TaskType, t.Status, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"task_status":   t.Status,
	})
}
func (qm *QueueManager) publishTaskStart(t *Task) {
	qm.eventHub.PublishNoTimestamp(t.Ctx, t.TaskType, t.Status, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"task_status":   t.Status,
		"start_time":    t.StartTime.String(),
	})
}
func (qm *QueueManager) publishTaskSuccess(t *Task) {
	qm.eventHub.PublishNoTimestamp(t.Ctx, t.TaskType, t.Status, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"start_time":    t.StartTime.String(),
		"task_status":   t.Status,
	})
}
func (qm *QueueManager) publishTaskWarn(t *Task) {
	qm.eventHub.PublishNoTimestamp(t.Ctx, t.TaskType, t.Status, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"task_status":   t.Status,
		"start_time":    t.StartTime.String(),
	})
}
func (qm *QueueManager) publishTaskFail(t *Task) {
	qm.eventHub.PublishNoTimestamp(t.Ctx, t.TaskType, t.Status, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"task_status":   t.Status,
		"start_time":    t.StartTime.String(),
	})
}
func (qm *QueueManager) publishTaskError(t *Task) {
	qm.eventHub.PublishNoTimestamp(t.Ctx, t.TaskType, t.Status, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"task_status":   t.Status,
		"start_time":    t.StartTime.String(),
	})
}

// -----------------------------------------------------------------
//              Work Execution
// -----------------------------------------------------------------

// This function organizes and controls how the worker completes its task
// At the end, a new task well be searched for to assign it to the worker,
// else the worker will become available for the next task received.
func (qm *QueueManager) work(w *Worker) {
	qm.mu.Lock()

	if !w.islocked() || w.TaskActioning.Status != "start" {
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
		qm.Logger.Debug("exec called")
		cmdtag, err := qm.Db.Exec(ctx, sql, args...)
		if err != nil {
			qm.Logger.Debug("exec error", "err", err)
			qm.reportWork(w, "error", map[string]any{"error": err.Error()}, true)
		} else {
			qm.Logger.Debug("exec success", "response", cmdtag)
			bef, _, _ := strings.Cut(cmdtag.String(), " ")
			qm.reportWork(w, "success", map[string]any{
				"action":        bef,
				"rows_affected": cmdtag.RowsAffected(),
			}, cmdtag.RowsAffected() == 0)
		}

	case "function":
		qm.Logger.Debug("function called")
		func_response, err := task_func(ctx, args...)
		if err != nil {
			qm.Logger.Debug("function error", "err", err)
			qm.reportWork(w, "error", map[string]any{"error": err.Error()}, true)
		} else {
			qm.Logger.Debug("function success", "response", func_response)
			qm.reportWork(w, "success", func_response, false)
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

// Queue a task for work, if there is a worker available, it will be asssigned
// to the worker instead.
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
		qm.publishTaskQueued(t)
	}
	return t.TaskID, nil
}

// -----------------------------------------------------------------
//              Worker Management
// -----------------------------------------------------------------

// Get a free worker if one is available, else nil
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

// Assigns a task to a worker and splits of a goroutines to complete it.
func (qm *QueueManager) assignWorker(w *Worker, t *Task) {
	qm.mu.Lock()
	w.TaskActioning = nil
	w.TaskActioning = t
	t.Status = "start"
	qm.publish(w)
	qm.mu.Unlock()
	go qm.work(w)
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



// Lock the worker only once a task has been assigned
func (w *Worker) lock()     { w.Lock = true }
func (w *Worker) islocked() bool { return w.Lock }
// Free a worker to be able to make it discoverable for new tasks
func (w *Worker) free() {
	w.TaskActioning = nil
	w.Lock = false
}
