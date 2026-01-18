package middleware

import (
	"context"

	"lotusforge.au/api-server/models"
)

type Contextkey string

const userContextKey Contextkey = "user"
const taskContextKey Contextkey = "task"

func SetUser(ctx context.Context, user *models.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func GetUser(ctx context.Context) (*models.User, bool) {
	user, ok := ctx.Value(userContextKey).(*models.User)
	return user, ok
}

func StartTask(ctx context.Context) context.Context {
	task, _ := generateRandomString(32)
	return context.WithValue(ctx, taskContextKey, task)
}

func GetTask(ctx context.Context) (string, bool) {
	val := ctx.Value(taskContextKey)
	if val == nil {
		return "", false
	}
	task, ok := val.(string)
	if !ok {
		return "", false
	}
	if len(task) == 32 {
		return task, true
	}
	return task, false
}
