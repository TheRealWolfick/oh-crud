package events

import (
	"testing"

	"lotusforge.au/api-server/models"
)

func TestEnableDiffTopic(t *testing.T) {
	h := NewEventManager(0, 0, nil, nil)
	cfg := &models.DataModel{}
	h.EnableTopic("table:asset", nil, true, cfg)
	h.EnableDiffTopic("diff:asset", cfg)

	diff, ok := h.targets["diff:asset"]
	if !ok || !h.validTopics["diff:asset"] {
		t.Fatal("Expected the diff topic to be registered")
	}
	if diff.permission != "diff" || !diff.persist_to_db || diff.config != cfg {
		t.Errorf("Unexpected diff topic instructions: permission %q, persist %v", diff.permission, diff.persist_to_db)
	}
	if h.targets["table:asset"].permission != "get" {
		t.Errorf("Expected table topics to need get, got %q", h.targets["table:asset"].permission)
	}
}
