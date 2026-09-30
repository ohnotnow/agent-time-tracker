package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const session = `{"type":"user","timestamp":"2026-09-30T10:00:00Z","origin":{"kind":"human"},"message":{"content":"hello"}}
{"type":"assistant","timestamp":"2026-09-30T10:00:05Z","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"system","subtype":"turn_duration","durationMs":5000,"timestamp":"2026-09-30T10:00:05Z"}
`

func TestTimelineAnswers304UntilTheLogChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abcdef123456.jsonl")
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Project: "demo", Resolve: func() (string, error) { return path, nil }}

	get := func(etag string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/timeline", nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}

	first := get("")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", first.Code)
	}
	var body apiTimeline
	if err := json.NewDecoder(first.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Session != "abcdef12" || body.Totals.Agent.Dur != "5s" || len(body.Rows) != 2 {
		t.Errorf("body = %+v", body)
	}

	etag := first.Header().Get("ETag")
	if got := get(etag).Code; got != http.StatusNotModified {
		t.Errorf("unchanged log: status = %d, want 304", got)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"user","timestamp":"2026-09-30T10:01:00Z","origin":{"kind":"human"},"message":{"content":"more"}}` + "\n")
	f.Close()
	if got := get(etag).Code; got != http.StatusOK {
		t.Errorf("grown log: status = %d, want 200", got)
	}
}
