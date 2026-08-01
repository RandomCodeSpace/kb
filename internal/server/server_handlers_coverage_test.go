package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteBoardJSONParseErrorReturnsStorageErrorWhenConditionCheckFails(t *testing.T) {
	st := newTestStore(t)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	w := httptest.NewRecorder()
	(&server{store: st}).writeBoardJSONParseError(w, "default", `"r1"`)

	if w.Code != http.StatusInternalServerError || w.Body.String() != storageErrorMessage+"\n" {
		t.Fatalf("response = %d %q, want 500 %q", w.Code, w.Body.String(), storageErrorMessage+"\n")
	}
	if got := w.Header().Get("ETag"); got != "" {
		t.Fatalf("ETag = %q, want empty on storage failure", got)
	}
}

func TestPutBoardReturnsStorageErrorWhenReplaceFails(t *testing.T) {
	st := newTestStore(t)
	srv := newServer(Config{}, testStatic, st)
	closedAfterSnapshot := false
	srv.afterConditionalBoardSnapshot = func() {
		closedAfterSnapshot = true
		if err := st.Close(); err != nil {
			t.Fatalf("close store after snapshot: %v", err)
		}
	}

	w := doReq(t, srv.handler(), http.MethodPut, "/api/board", "# Board\n", map[string]string{
		"If-Match": "*",
	})

	if !closedAfterSnapshot {
		t.Fatal("conditional snapshot hook was not called")
	}
	if w.Code != http.StatusInternalServerError || w.Body.String() != storageErrorMessage+"\n" {
		t.Fatalf("response = %d %q, want 500 %q", w.Code, w.Body.String(), storageErrorMessage+"\n")
	}
	if got := w.Header().Get("ETag"); got != "" {
		t.Fatalf("ETag = %q, want empty when replacement is not committed", got)
	}
}

func TestParseBoardJSONPutReturnsObjectKeyDecodeError(t *testing.T) {
	got, ids, err := parseBoardJSONPut([]byte(`{"board":"# Board\n",!`))
	if err == nil || err.Error() != "invalid character '!' looking for beginning of object key string" {
		t.Fatalf("error = %v, want malformed object key error", err)
	}
	if got.Title != "" || len(got.Tasks) != 0 || ids != nil {
		t.Fatalf("partial result = %+v, %v; want zero board and nil task IDs", got, ids)
	}
}
