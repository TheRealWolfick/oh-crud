package audit

import (
	"net/http/httptest"
	"strings"
	"testing"

	"lotusforge.au/api-server/globals"
	"lotusforge.au/api-server/models/modelstest"
)

func TestRecord(t *testing.T) {
	db := &modelstest.FakeDB{}
	err := Record(t.Context(), db, Event{
		Action: globals.ActionUserLogin, Target: "usr_1", Service: globals.AuditSystem, ActorUser: "usr_1",
		AMR: []string{"pwd"}, After: map[string]string{"session": "fam1"}, IP: "192.0.2.1",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	c := db.LastCall()
	if !strings.Contains(c.SQL, "INSERT INTO "+globals.AuditTable) || len(c.Args) != 11 {
		t.Fatalf("unexpected call %s %v", c.SQL, c.Args)
	}
	// Empty optional fields are NULL, not empty strings, so "is null" queries work
	if c.Args[4] != nil || c.Args[6] != nil || c.Args[7] != nil || c.Args[10] != nil {
		t.Errorf("expected via_service, org_id, before and request_id to be NULL, got %v", c.Args)
	}
	if c.Args[8] != `{"session":"fam1"}` {
		t.Errorf("expected after as JSON text, got %#v", c.Args[8])
	}
}

func TestRecordNeverStoresNilAMR(t *testing.T) {
	db := &modelstest.FakeDB{}
	if err := Record(t.Context(), db, Event{Action: "a.b", Target: "t", Service: globals.AuditSystem}); err != nil {
		t.Fatal(err)
	}
	if amr, ok := db.LastCall().Args[5].([]string); !ok || amr == nil {
		t.Errorf("expected an empty amr array, got %#v", db.LastCall().Args[5])
	}
}

func TestRecordRequiresActionTargetService(t *testing.T) {
	db := &modelstest.FakeDB{}
	for _, e := range []Event{{Target: "t", Service: "s"}, {Action: "a", Service: "s"}, {Action: "a", Target: "t"}} {
		if err := Record(t.Context(), db, e); err == nil {
			t.Errorf("expected %+v to be refused", e)
		}
	}
	if len(db.Calls) != 0 {
		t.Errorf("a refused event must not be written")
	}
}

func TestIP(t *testing.T) {
	cases := map[string]struct{ remote, xff, want string }{
		"remote with port":     {"192.0.2.1:1234", "", "192.0.2.1"},
		"ipv6 with port":       {"[2001:db8::1]:443", "", "2001:db8::1"},
		"forwarded list":       {"10.0.0.1:1", "203.0.113.7, 10.0.0.2", "203.0.113.7"},
		"forwarded with port":  {"10.0.0.1:1", "203.0.113.7:5555", "203.0.113.7"},
		"garbage":              {"10.0.0.1:1", "not-an-ip", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" { r.Header.Set("X-Forwarded-For", tc.xff) }
			if got := IP(r); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequestID(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-Request-Id", "req-7c2e.abc_123")
	if got := RequestID(r); got != "req-7c2e.abc_123" {
		t.Errorf("expected the caller's id, got %q", got)
	}

	for _, bad := range []string{"", "short", "has space in it", "new\nline-injected", strings.Repeat("a", 65)} {
		r.Header.Set("X-Request-Id", bad)
		got := RequestID(r)
		if got == bad || len(got) != 24 {
			t.Errorf("expected %q to be replaced by a fresh id, got %q", bad, got)
		}
	}
}
