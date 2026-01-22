package middleware

import (
	"context"

	"lotusforge.au/api-server/models"
)

type Contextkey string

type TaskContext struct {
	Id string
	Type string
}

const userContextKey Contextkey = "user"
const taskContextKey Contextkey = "task"

func SetUser(ctx context.Context, user *models.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func GetUser(ctx context.Context) (*models.User, bool) {
	user, ok := ctx.Value(userContextKey).(*models.User)
	return user, ok
}

func StartTask(ctx context.Context, task_type string) context.Context {
	id, _ := generateRandomString(32)
	task := TaskContext{Id: id, Type: task_type}
	return context.WithValue(ctx, taskContextKey, task)
}

func GetTask(ctx context.Context) (TaskContext, bool) {
	val := ctx.Value(taskContextKey)
	if val == nil {
		return TaskContext{}, false
	}
	task, ok := val.(TaskContext)
	if !ok {
		return TaskContext{}, false
	}
	if len(task.Id) == 32 {
		return task, true
	}
	return task, false
}
