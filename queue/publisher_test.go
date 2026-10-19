package queue

import (
	"context"
	"testing"
	"time"
)

type publishedEvent struct {
	action string
	status string
	topic  string
	payload map[string]any
}

type fakePublisher struct {
	events []publishedEvent
}

func (f *fakePublisher) Publish(_ context.Context, action string, status string, _ time.Time, topic string, payload map[string]any, _ int) error {
	f.events = append(f.events, publishedEvent{action: action, status: status, topic: topic, payload: payload})
	return nil
}

func TestNewQueue_NilPublisherIsNoop(t *testing.T) {
	qm := NewQueue(nil, 1, nil, nil)
	if _, ok := qm.publisher.(noopPublisher); !ok {
		t.Fatalf("expected noopPublisher when nil is passed, got %T", qm.publisher)
	}
	// Must not panic
	qm.publishTaskQueued(&Task{Ctx: context.Background(), TaskID: "t1", TaskType: "create", Status: "queued", Topic: "table:test"})
}

func TestQueue_PublishesThroughPublisher(t *testing.T) {
	fp := &fakePublisher{}
	qm := NewQueue(nil, 1, nil, fp)

	qm.publishTaskQueued(&Task{Ctx: context.Background(), TaskID: "t1", TaskType: "create", Status: "queued", Topic: "table:test"})

	if len(fp.events) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(fp.events))
	}
	e := fp.events[0]
	if e.action != "create" || e.status != "queued" || e.topic != "table:test" || e.payload["task_id"] != "t1" {
		t.Errorf("unexpected event: %+v", e)
	}
}
