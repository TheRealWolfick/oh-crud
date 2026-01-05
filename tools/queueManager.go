package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Task struct {
	TaskID        string     `json:"task_id"`
	TaskType      string     `json:"task_type"`
	Sql           string     `json:"sql"`
	StartTime     time.Time  `json:"start_time"`
	CompleteTime  time.Time  `json:"complete_time"`
	Status        string     `json:"status"`
	Success       bool       `json:"success"`
	Response      []byte     `json:"response"`
	Ctx           context.Context  `json:"-"`
	Function      func(context.Context, ...any) ([]byte, error) `json:"-"`
	Args          []any `json:"-"`
	Lock          bool				`json:"lock"`
}

type QueueManager struct {
	tasks         []*Task
	workers       []*Worker
	workers_count int
	active_workers int
	db            *pgxpool.Pool
	logger        *slog.Logger
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


// Print the completed task info string. This should only be used once the task is complete
func (t *Task) taskCompleteInfoString() string {
	return fmt.Sprintf(`{"task_id": %s, "status": %s, "start_time": %v, "complete_time": %v, "num_args": %v, "response": %v}`, t.TaskID, t.Status, t.StartTime, t.CompleteTime, len(t.Args), t.Response)
}


// Return a string of the tasks current status snapshot
func (t *Task) taskStagedInfoString() string {
	return fmt.Sprintf(`{"task_id": "%s", "task_type": "%s", "status": "%s", "start_time": "%v"}`, t.TaskID, t.TaskType, t.Status, t.StartTime)
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
		db: db,
		workers: workers,
		active_workers: 0,
		workers_count: num_workers,
		logger: logger,
	}
}


// Creates a new task
func (qm *QueueManager) createTask(ctx context.Context, sql string, args ...any) (*Task, error) {
	qm.mu.Lock()

	task_id, err := Generate32CharString()

	if err != nil {
		return nil, err
	}

	t := &Task{
		TaskID: task_id,
		TaskType: "exec",
		StartTime: time.Now(),
		Status: "queued",
		Ctx: ctx,
		Args: args,
		Sql: sql,
		Lock: true,
	}
	
	qm.tasks = append(qm.tasks, t)
	
	// Write task creation to database
	log, _ := json.Marshal(t.taskStagedInfoString())
	
	qm.mu.Unlock()
	qm.logDatabaseEvent(ctx, "TASK_CREATE", log)
	
	return t, nil
}


// Creates a new function task
func (qm *QueueManager) createFunctionTask(ctx context.Context, function func(context.Context, ...any) ([]byte, error), args ...any) (*Task, error) {
	qm.mu.Lock()
	task_id, err := Generate32CharString()

	if err != nil {
		return nil, err
	}

	t := &Task{
		TaskID: task_id,
		TaskType: "function",
		StartTime: time.Now(),
		Status: "queued",
		Ctx: ctx,
		Args: args,
		Function: function,
		Lock: true,
	}
	
	// Write task creation to database
	log, _ := json.Marshal(t.taskStagedInfoString())
	
	qm.mu.Unlock()
	qm.logDatabaseEvent(ctx, "TASK_CREATE", log)

	return t, nil
}

// Report on the work done
func (qm *QueueManager) reportWork(w *Worker, status string, res []byte) {
	qm.mu.Lock()
	w.TaskActioning.Response = res
	w.TaskActioning.CompleteTime = time.Now()

	switch status {
	case "error":
		// Task failed
		// Update task status
		w.TaskActioning.Status = "error"

		// Report task info
		log, err := json.Marshal(fmt.Sprintf(`{"worker": %v, "task": "%s"}`, w.ID, w.TaskActioning.taskCompleteInfoString()))
		if err != nil {
			qm.logger.Error("Failed to report task failure", "worker", w.ID, "task_id", w.TaskActioning.TaskID, "error", w.TaskActioning.Response, "log_error", err)
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
		log, err := json.Marshal(fmt.Sprintf(`{"worker": %v, "task": "%s"}`, w.ID, w.TaskActioning.taskCompleteInfoString()))
		if err != nil {
			qm.logger.Error("Failed to report task success", "worker", w.ID, "task_id", w.TaskActioning.TaskID, "log_error", err)
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
		return nil
	}
	for _, task := range qm.tasks {
		if !task.islocked() {
			task.lock()
			return task
		}
	}
	// No outstanding tasks
	return nil
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
		cmdtag, err := qm.db.Exec(ctx, sql, args...)

		// Report work
		if err != nil {
			qm.reportWork(w, "error", []byte(err.Error()))
		} else {
			qm.reportWork(w, "success", []byte(cmdtag.String()))
		}

	case "function":
		// Function execution
		func_response, err := task_func(ctx, args...)

		// Report work
		if err != nil {
			qm.reportWork(w, "error", []byte(err.Error()))
		} else {
			qm.reportWork(w, "success", func_response)
		}
	}

	// Remove completed item from queue
	qm.Dequeue(w)

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
	// Assign task to worker
	w.TaskActioning = t

	// Update task and QueueManager
	t.Status = "processing"

	// Report assignment
	log, _ := json.Marshal(fmt.Sprintf(`{"task": %s, "worker": %v}`, t.taskStagedInfoString(), w.ID))
	ctx := t.Ctx
	qm.mu.Unlock() 
	qm.logDatabaseEvent(ctx, "TASK_UPDATE", log)

	// Commence work
	go qm.work(w)
}	


// Log a database event into the database
func (qm *QueueManager) logDatabaseEvent(ctx context.Context, event string, log []byte) {
	go func() {
		query := `INSERT INTO events (type, event_log) VALUES ($1, $2)`
		_, err := qm.db.Exec(ctx, query, event, log)
		if err != nil && qm.logger != nil {
			qm.logger.Error("Failed to log event", "error", err, "event", event)
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
			// Free task for next available worker
			t.free()
			qm.mu.Unlock()
		}
	} else {
		// Free task for next available worker
		t.free()
		qm.mu.Unlock()
	}

	// Return the TaskID to be used to get task status
	return t.TaskID, nil
}


// Queue a function to be run asynchronously. A 32 character uuid will be returned that can be used to query the status of the task and also get the database response.
func (qm *QueueManager) QueueFunction(ctx context.Context, function func(context.Context, ...any) ([]byte, error), args ...any) (string, error) {
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


// Remove a task from the queue
func (qm *QueueManager) Dequeue(worker *Worker) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	task_pos, found := qm.getTaskPosUnsafe(worker.TaskActioning.TaskID)

	if found {
		worker.TaskActioning = nil
		if task_pos == len(qm.tasks) - 1 {
			qm.tasks = qm.tasks[:task_pos]
		} else {
			qm.tasks = append(qm.tasks[:task_pos], qm.tasks[task_pos + 1:]...)
		}
	}
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
		return qm.tasks[task_pos].taskCompleteInfoString(), true
	}
	return "", false
}

// Lock a task to prevent concurrent workers being assigned
func (t *Task) lock() {
	t.Lock = true
}

// Free a task for the next available worker
func (t *Task) free() {
	t.Lock = false
}

// Check if a task is locked
func (t *Task) islocked() bool {
	return t.Lock == true
}

// Free the worker to be used again
func (w *Worker) free() {
	w.Lock = false
}

// Lock the worker so it cannot be overwritten with a new task before completing the current one
func (w *Worker) lock() {
	w.Lock = true
}

// Return whether the worker is currently locked
func (w *Worker) islocked() bool {
	return w.Lock
}

