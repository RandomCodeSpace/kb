package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestLabels(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	w := doReq(t, h, "GET", "/api/labels", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET labels: got %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("empty labels body = %q, want []", got)
	}

	const in = "# B\n\n## To Do\n\n- [ ] t1 #backend #type::bug\n"
	if w := doReq(t, h, "PUT", "/api/board", in, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT board: got %d", w.Code)
	}
	w = doReq(t, h, "GET", "/api/labels", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET labels: got %d, want 200", w.Code)
	}
	var labels []string
	if err := json.Unmarshal(w.Body.Bytes(), &labels); err != nil {
		t.Fatalf("labels JSON: %v (body=%s)", err, w.Body)
	}
	// Most recently used first.
	if want := []string{"type::bug", "backend"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	getSettings := func() settingsResponse {
		t.Helper()
		w := doReq(t, h, "GET", "/api/settings", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET settings: got %d", w.Code)
		}
		if strings.Contains(w.Body.String(), "sk-secret-123") {
			t.Fatalf("settings response leaks the key: %s", w.Body)
		}
		var got settingsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("settings JSON: %v", err)
		}
		return got
	}

	if got := getSettings(); got != (settingsResponse{}) {
		t.Errorf("default settings = %+v, want zero", got)
	}
	put := `{"ai_base_url":"https://api.example.com/v1","ai_model":"gpt-test","ai_key":"sk-secret-123"}`
	if w := doReq(t, h, "PUT", "/api/settings", put, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT settings: got %d (body=%s)", w.Code, w.Body)
	}
	want := settingsResponse{BaseURL: "https://api.example.com/v1", Model: "gpt-test", HasKey: true}
	if got := getSettings(); got != want {
		t.Errorf("settings = %+v, want %+v", got, want)
	}
	// Subset PUT patches only the given field.
	if w := doReq(t, h, "PUT", "/api/settings", `{"ai_model":"other"}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT subset: got %d", w.Code)
	}
	want.Model = "other"
	if got := getSettings(); got != want {
		t.Errorf("after subset PUT = %+v, want %+v", got, want)
	}
	// Empty key clears the stored key.
	if w := doReq(t, h, "PUT", "/api/settings", `{"ai_key":""}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT clear key: got %d", w.Code)
	}
	want.HasKey = false
	if got := getSettings(); got != want {
		t.Errorf("after key clear = %+v, want %+v", got, want)
	}
	// Bad inputs.
	if w := doReq(t, h, "PUT", "/api/settings", `{"ai_base_url":"ftp://example.com"}`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("non-http scheme: got %d, want 400", w.Code)
	}
	if w := doReq(t, h, "PUT", "/api/settings", `not json`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("bad JSON: got %d, want 400", w.Code)
	}
}

func TestPutSettingsBaseURLChangeClearsKey(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	put := `{"ai_base_url":"https://api.example.com/v1","ai_model":"m","ai_key":"sk-secret-123"}`
	if w := doReq(t, h, "PUT", "/api/settings", put, nil); w.Code != http.StatusNoContent {
		t.Fatalf("seed PUT settings: got %d (body=%s)", w.Code, w.Body)
	}
	// Re-pointing the base URL to another host without re-sending the key
	// must drop the stored key and say so.
	w := doReq(t, h, "PUT", "/api/settings", `{"ai_base_url":"https://evil.example.net"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("cross-host PUT: got %d, want 200 (body=%s)", w.Code, w.Body)
	}
	var res struct {
		KeyCleared bool `json:"key_cleared"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || !res.KeyCleared {
		t.Fatalf("cross-host PUT body = %s (err=%v), want key_cleared true", w.Body, err)
	}
	w = doReq(t, h, "GET", "/api/settings", "", nil)
	var got settingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("settings JSON: %v", err)
	}
	if got.HasKey {
		t.Error("key survived a cross-host base URL change")
	}
}
