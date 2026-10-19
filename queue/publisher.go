package queue

import (
	"context"
	"time"
)

// Publisher delivers task events to whoever is listening. The queue only depends on
// this interface; *events.EventManager satisfies it and is wired in by main.
type Publisher interface {
	Publish(ctx context.Context, action string, status string, timestamp time.Time, topic string, payload map[string]any, success_count int) error
}

// noopPublisher discards events. Used when NewQueue is given no publisher (e.g. tests).
type noopPublisher struct{}

func (noopPublisher) Publish(context.Context, string, string, time.Time, string, map[string]any, int) error {
	return nil
}
