package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/middleware"
	"lotusforge.au/api-server/models"
)

type Task struct {
	TaskID        string     `json:"task_id"`
	TaskType      string     `json:"task_type"`
	Sql           string     `json:"sql"`
	StartTime     time.Time  `json:"start_time"`
	CompleteTime  time.Time  `json:"complete_time"`
	Status        string     `json:"status"`
	Success       bool       `json:"success"`
	Response      map[string]any     `json:"response"`
	Ctx           context.Context  `json:"-"`
	Function      func(context.Context, ...any) (map[string]any, error) `json:"-"`
	Args          []any `json:"-"`
}

type QueueManager struct {
	tasks         []*Task
	workers       []*Worker
	workers_count int
	active_workers int
	Db            *pgxpool.Pool
	Logger        *slog.Logger
	mu            sync.Mutex
}

type Worker struct {
	ID             int
	Lock           bool
	TaskActioning  *Task
}

func newWorker(id int) *Worker {
	return &Worker{
		ID: id,
	}
}


// Create a new queue for running the db.Exec command asyncronously. It creates both database logs and local logs
func NewQueue(db *pgxpool.Pool, num_workers int, logger *slog.Logger) *QueueManager {
	// Create the workers
	workers := make([]*Worker, num_workers)
	for i := range num_workers {
		workers[i] = newWorker(i)
	}

	// Return the queue
	return &QueueManager{
		tasks: []*Task{},
		Db: db,
		workers: workers,
		active_workers: 0,
		workers_count: num_workers,
		Logger: logger,
	}
}


// Creates a new task
func (qm *QueueManager) createTask(ctx context.Context, sql string, args ...any) (*Task, error) {
	task_id, ok := middleware.GetTask(ctx)

	if !ok {
		task, _ := Generate32CharString()
		task_id = task
	}

	t := &Task{
		TaskID: task_id,
		TaskType: "exec",
		StartTime: time.Now(),
		Status: "queued",
		Ctx: ctx,
		Args: args,
		Sql: sql,
	}
	
	// Write task creation to database
	qm.logDatabaseEvent(ctx, "TASK_CREATE", t.logMiniUnsafe())
	
	return t, nil
}


// Creates a new function task
func (qm *QueueManager) createFunctionTask(ctx context.Context, function func(context.Context, ...any) (map[string]any, error), args ...any) (*Task, error) {
	task_id, ok := middleware.GetTask(ctx)

	if !ok {
		task, _ := Generate32CharString()
		task_id = task
	}

	t := &Task{
		TaskID: task_id,
		TaskType: "function",
		StartTime: time.Now(),
		Status: "queued",
		Ctx: ctx,
		Args: args,
		Function: function,
	}
	
	// Write task creation to database
	qm.logDatabaseEvent(ctx, "TASK_CREATE", t.logMiniUnsafe())

	return t, nil
}

// Report on the work done
func (qm *QueueManager) reportWork(w *Worker, status string, res map[string]any) {
	qm.mu.Lock()
	w.TaskActioning.Response = res
	w.TaskActioning.CompleteTime = time.Now()

	switch status {
	case "error":
		// Task failed
		// Update task status
		w.TaskActioning.Status = "error"

		// Report task info
		log, err := w.logTaskUnsafe()
		if err != nil {
			qm.Logger.Error("Failed to report task failure", "worker", w.ID, "task_id", w.TaskActioning.TaskID, "task_type", w.TaskActioning.TaskType, "error", w.TaskActioning.Response, "log_error", err, "user", w.TaskActioning.Ctx.Value(middleware.Contextkey("user")).(*models.User).Username)
			qm.mu.Unlock()
		} else {
			qm.mu.Unlock()
			qm.logDatabaseEvent(w.TaskActioning.Ctx, "TASK_ERROR", log)
		}

	case "success":
		// Task suceeded
		// Update task status
		w.TaskActioning.Status = "complete"

		// Report task info
		log, err := w.logTaskUnsafe()
		if err != nil {
			qm.Logger.Error("Failed to report task success", "worker", w.ID, "task_id", w.TaskActioning.TaskID, "task_type", w.TaskActioning.TaskType, "error", w.TaskActioning.Response, "log_error", err, "user", w.TaskActioning.Ctx.Value(middleware.Contextkey("user")).(*models.User).Username)
			qm.mu.Unlock()
		} else {
			qm.mu.Unlock()
			qm.logDatabaseEvent(w.TaskActioning.Ctx, "TASK_COMPLETE", log)
		}
	}
}

// Look for an outstanding tasks. If a task is found, that task is returned in a locked state
func (qm *QueueManager) lookForTask() *Task {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	// Look for more Work
	if len(qm.tasks) < 1 {
		// No tasks remaining
		return nil
	}

	// Extract next task from queue
	t := qm.tasks[0]

	// Remove the extracted task
	qm.tasks = qm.tasks[1:]

	if len(qm.tasks) > 0 && cap(qm.tasks) > 64 && len(qm.tasks) < cap(qm.tasks)/4 {
		newTasks := make([]*Task, len(qm.tasks))
		copy(newTasks, qm.tasks)
		qm.tasks = newTasks
	}
	
	return t
}


// Do work
func (qm *QueueManager) work(w *Worker) {
	qm.mu.Lock()

	// Ensure work can be completed
	if !w.islocked() || w.TaskActioning.Status != "processing" {
		qm.mu.Unlock()
		return
	}
	// Copy task state that will be used asyncronously
	ctx := w.TaskActioning.Ctx
	sql := w.TaskActioning.Sql
	args := w.TaskActioning.Args
	task_type := w.TaskActioning.TaskType
	task_func := w.TaskActioning.Function

	qm.mu.Unlock()

	// Do work based on what type of work it is
	switch task_type {
	case "exec":
		// SQL exec
		cmdtag, err := qm.Db.Exec(ctx, sql, args...)

		// Report work
		if err != nil {
			qm.reportWork(w, "error", map[string]any{"error": err.Error()})
		} else {
			bef, _, _ := strings.Cut(cmdtag.String(), " ")
			logData := map[string]any {
				"action": bef,
				"rows_affected": cmdtag.RowsAffected(),
			}
			qm.reportWork(w, "success", logData)
		}

	case "function":
		// Function execution
		func_response, err := task_func(ctx, args...)

		// Report work
		if err != nil {
			qm.reportWork(w, "error", map[string]any{"error": err.Error()})
		} else {
			qm.reportWork(w, "success", func_response)
		}
	}

	// Look for more work
	t := qm.lookForTask()

	// If there was no new tasks
	if t == nil {
		qm.mu.Lock()
		qm.active_workers--
		w.free()
		qm.mu.Unlock()
		return
	}

	// There was a new task
	qm.assignWorker(w, t)
}


// Get the first free worker and if the worker is free. If there are no free workers, the return will be -1, false
func (qm *QueueManager) getFreeWorker() (w *Worker) {
	// Check if there are free workers
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


// Assign a worker to a task. The worker stores a reference to the task, and the context for the request
func (qm *QueueManager) assignWorker(w *Worker, t *Task) {
	qm.mu.Lock()
	// Clear the worker's task field to ensure any existing tasks are cleared for the garbage collector
	w.TaskActioning = nil
	// Assign task to worker
	w.TaskActioning = t

	// Update task and QueueManager
	t.Status = "processing"

	// Report assignment
	qm.mu.Unlock() 

	// Commence work
	go qm.work(w)
}	


// Log a database event into the database
func (qm *QueueManager) logDatabaseEvent(ctx context.Context, event string, log []byte) {
	go func() {
		query := `INSERT INTO events (type, event_log, event_user) VALUES ($1, $2, $3)`
		_, err := qm.Db.Exec(ctx, query, event, log, ctx.Value(middleware.Contextkey("user")).(*models.User).Username)
		if err != nil && qm.Logger != nil {
			qm.Logger.Error("Failed to log event", "error", err, "event", event, "user", ctx.Value(middleware.Contextkey("user")).(*models.User).Username, "log", log)
		}
	}()
}


// Queue a task to run asyncronously. A w2 character uuid will be returned that can be used to query the status of the task and also get the database response.
func (qm *QueueManager) queue(t *Task) ( string, error ) {
	// Assign worker to task if there is a free worker
	worker := qm.getFreeWorker()

	qm.mu.Lock()
	// If we have both a worker and a task
	if worker != nil {
		if t != nil {
			// Assign a worker to work on this task
			qm.mu.Unlock()
			qm.assignWorker(worker, t)
		} else {
			// No task was supplied
			qm.Logger.Error("Queue was called without a valid task")
			qm.mu.Unlock()
			return "", errors.New("No valid task was supplied to queue")
		}
	} else {
		// add task to queue for next available worker
		qm.tasks = append(qm.tasks, t)
		qm.mu.Unlock()
	}

	// Return the TaskID to be used to get task status
	return t.TaskID, nil
}


// Queue a function to be run asynchronously. A 32 character uuid will be returned that can be used to query the status of the task and also get the database response.
func (qm *QueueManager) QueueFunction(ctx context.Context, function func(context.Context, ...any) (map[string]any, error), args ...any) (string, error) {
	t, err := qm.createFunctionTask(ctx, function, args...)
	if err != nil {
		return "", err
	}
	return qm.queue(t)
}


// Queue a db.exec command to be run asyncronously. A 32 character uuid will be returned that can be used to query the status of the task and also get the database response.
func (qm *QueueManager) QueueExec(ctx context.Context, sql string, args ...any) (string, error) {
	t, err := qm.createTask(ctx, sql, args...)
	if err != nil {
		return "", err
	}
	return qm.queue(t)
}


// Get the position in the queue of a task
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



// Lock the worker so it cannot be overwritten with a new task before completing the current one
func (w *Worker) lock() {
	w.Lock = true
}

// Return whether the worker is currently locked
func (w *Worker) islocked() bool {
	return w.Lock
}

// Free the worker to be used again
func (w *Worker) free() {
	// clear the outstanding task for the garbage collector
	w.TaskActioning = nil
	w.Lock = false
}

// Return the basic log information of the task. Intended for when the task is incomplete.
// Returns the task_id, task_type, status, and start_time
func (t *Task) logMiniUnsafe() []byte {
	logData := map[string]any {
		"task": map[string]any {
			"task_id":       t.TaskID,
			"task_type":     t.TaskType,
			"status":        t.Status,
			"start_time":    t.StartTime,
		},
	}
	log, _ := json.Marshal(logData)
	return log
}


// Return the full completed log information of the task
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
				"response":      w.TaskActioning.Response,
			},
		}
		// Report task info
		return json.Marshal(logsData)
}
