package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
)

// queryFailure maps busy and deadline storage errors to a retryable 503
// storage_busy and keeps other failures as storage_unavailable.
func TestQueryFailureMapsBusyToStorageBusy(t *testing.T) {
	for _, c := range []struct {
		err   error
		code  string
		retry string
	}{
		{context.DeadlineExceeded, "storage_busy", "2"},
		{fmt.Errorf("wrap: %w", context.Canceled), "storage_busy", "2"},
		{errors.New("database is locked (5) (SQLITE_BUSY)"), "storage_busy", "2"},
		{errors.New("disk I/O error"), "storage_unavailable", ""},
	} {
		w := httptest.NewRecorder()
		queryFailure(w, c.err)
		var v map[string]any
		json.Unmarshal(w.Body.Bytes(), &v)
		if w.Code != 503 || v["code"] != c.code || w.Header().Get("Retry-After") != c.retry {
			t.Errorf("%v: %d %v Retry-After=%q", c.err, w.Code, v["code"], w.Header().Get("Retry-After"))
		}
	}
}
