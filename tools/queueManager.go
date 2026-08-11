package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

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
	Function     func(context.Context, ...any) (string, map[string]any, error) `json:"-"`
	Args         []any                                                 `json:"-"`
	Cfg          models.DataModel                                      `json:"-"`
	Topic        string                                                `json:"-"`
}

type QueueManager struct {
	tasks          []*Task
	workers        []*Worker
	workers_count  int
	active_workers int
	Db             models.DBExecQuery
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
func NewQueue(db models.DBExecQuery, num_workers int, logger *slog.Logger, evh *EventManager) *QueueManager {
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

func (qm *QueueManager) ReturnTargets() map[string]*Instructions {
	return qm.eventHub.targets
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
		Topic:     fmt.Sprintf("table:%s", *task.Cfg.End_point),
	}
	return t, nil
}

// This creates a task to execute an arbitrary function asyncronously.
// The scheduled function must return a payload of data in the form of a map as well as an error status
func (qm *QueueManager) createFunctionTask(ctx context.Context, function func(context.Context, ...any) (string, map[string]any, error), note string, args ...any) (*Task, error) {
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
		Topic:     fmt.Sprintf("table:%s", *task.Cfg.End_point),
	}
  qm.Logger.Debug("Function task created", "details", DereferencedString(t))
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
	case "complete":
		if fail {
			w.TaskActioning.Status = "failed"
		} else {
			failed_count, ieok := res["failed_count"]
			if ieok && failed_count.(int) > 0 {
				w.TaskActioning.Status = "warn"
			} else {
				w.TaskActioning.Status = "success"
			}
		}
	}

	// Publish notification
	qm.publish(w)
}

func (qm *QueueManager) getReportableTaskInfo(w *Worker) (map[string]any, int) {
	switch w.TaskActioning.Status {
	case "error", "failed":
		return map[string]any{
			"task_id":       w.TaskActioning.TaskID,
			"task_type":     w.TaskActioning.TaskType,
			"task_start":    w.TaskActioning.StartTime,
			"task_end":      w.TaskActioning.CompleteTime,
			"note":          w.TaskActioning.Note,
			"task_status":   w.TaskActioning.Status,
			"task_error":    w.TaskActioning.Response,
		}, 0
	case "success", "warn":
		return map[string]any{
			"task_id":       w.TaskActioning.TaskID,
			"task_type":     w.TaskActioning.TaskType,
			"task_start":    w.TaskActioning.StartTime,
			"task_end":      w.TaskActioning.CompleteTime,
			"note":          w.TaskActioning.Note,
			"task_status":   w.TaskActioning.Status,
			"task_response": w.TaskActioning.Response,
		}, w.TaskActioning.Response["success_count"].(int)
	case "start":
		return map[string]any{
			"task_id":       w.TaskActioning.TaskID,
			"task_type":     w.TaskActioning.TaskType,
			"note":          w.TaskActioning.Note,
			"task_start":    w.TaskActioning.StartTime,
			"task_status":   w.TaskActioning.Status,
		}, 0
	}
	return map[string]any{
		"task_id":         w.TaskActioning.TaskID,
		"task_type":       w.TaskActioning.TaskType,
		"note":            w.TaskActioning.Note,
		"task_start":      w.TaskActioning.StartTime,
		"task_status":     w.TaskActioning.Status,
	}, 0
}

// This is an internal helper function to publish an event
func (qm *QueueManager) publish(w *Worker) {

	payload, success_count := qm.getReportableTaskInfo(w)
	qm.eventHub.PublishNoTimestamp(w.TaskActioning.Ctx, w.TaskActioning.TaskType, w.TaskActioning.Status, w.TaskActioning.Topic, payload, success_count)
}
// This is an internal helper function for publishing an event when there is no worker assigned, 
// instead utilizing the task directly
func (qm *QueueManager) publishTask(t *Task) {
	qm.eventHub.PublishNoTimestamp(t.Ctx, t.TaskType, t.Status, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"task_start":    t.StartTime,
		"task_status":   t.Status,
	}, 0)
}
func (qm *QueueManager) publishTaskQueued(t *Task) {
	qm.eventHub.PublishNoTimestamp(t.Ctx, t.TaskType, t.Status, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"task_status":   t.Status,
	}, 0)
}
func (qm *QueueManager) publishTaskStart(t *Task) {
	qm.eventHub.PublishNoTimestamp(t.Ctx, t.TaskType, t.Status, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"task_status":   t.Status,
		"start_time":    t.StartTime.String(),
	}, 0)
}
func (qm *QueueManager) publishTaskSuccess(t *Task) {
	qm.eventHub.Publish(t.Ctx, t.TaskType, t.Status, t.CompleteTime, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"start_time":    t.StartTime.String(),
		"task_status":   t.Status,
		"complete_time": t.CompleteTime.String(),
		"success_items": t.Response["success_items"],
	}, t.Response["success_count"].(int))
}
func (qm *QueueManager) publishTaskWarn(t *Task) {
	qm.eventHub.Publish(t.Ctx, t.TaskType, t.Status, t.CompleteTime, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"task_status":   t.Status,
		"start_time":    t.StartTime.String(),
		"complete_time": t.CompleteTime.String(),
		"success_items": t.Response["success_items"],
		"failed_items":  t.Response["failed_items"],
	}, t.Response["success_count"].(int))
}
func (qm *QueueManager) publishTaskFail(t *Task) {
	qm.eventHub.Publish(t.Ctx, t.TaskType, t.Status, t.CompleteTime, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"task_status":   t.Status,
		"start_time":    t.StartTime.String(),
		"complete_time": t.CompleteTime.String(),
		"failed_items":  t.Response["failed_items"],
	}, 0)
}
func (qm *QueueManager) publishTaskError(t *Task) {
	qm.eventHub.PublishNoTimestamp(t.Ctx, t.TaskType, t.Status, t.Topic, map[string]any{
		"task_id":       t.TaskID,
		"task_type":     t.TaskType,
		"task_status":   t.Status,
		"start_time":    t.StartTime.String(),
		"complete_time": t.CompleteTime.String(),
	}, 0)
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
			qm.reportWork(w, "complete", map[string]any{
				"action":        bef,
				"rows_affected": cmdtag.RowsAffected(),
			}, cmdtag.RowsAffected() == 0)
		}

	case "function":
		qm.Logger.Debug("function called")
		func_type, func_response, err := task_func(ctx, args...)

		if err != nil || func_type != w.TaskActioning.TaskType {
			if func_type != w.TaskActioning.TaskType && err == nil {
				// Report on bad func call. Should never occur, but you never know
				err_func_called := fmt.Errorf("Task type and function called did not match! \nTask type: {%s}\nFunction called: {%s}", w.TaskActioning.TaskType, func_type)
				qm.Logger.Debug("function error", "err", err_func_called)
				qm.reportWork(w, "error", map[string]any{"error": err_func_called.Error()}, true)
			} else {
				// Report error in return data parsing
				qm.Logger.Debug("function error", "err", err)
				qm.reportWork(w, "error", map[string]any{"error": err.Error()}, true)
			}
		} else {
			qm.Logger.Debug("function success", "response", func_response)
			suc_count, ok := func_response["success_count"].(int)
			qm.reportWork(w, "complete", func_response, !ok || suc_count == 0)
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
func (qm *QueueManager) QueueFunction(ctx context.Context, function func(context.Context, ...any) (string, map[string]any, error), note string, args ...any) (string, error) {
	
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
