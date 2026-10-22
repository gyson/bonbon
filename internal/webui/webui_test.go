package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPagePaths(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)
	for path, want := range map[string]int{
		"/":                    http.StatusOK,
		"/sessions/31a54e53":   http.StatusOK,
		"/settings":            http.StatusOK,
		"/sessions":            http.StatusNotFound,
		"/sessions/":           http.StatusNotFound,
		"/sessions/31a54e53/x": http.StatusNotFound,
		"/settings/":           http.StatusNotFound,
		"/unknown":             http.StatusNotFound,
	} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != want {
			t.Errorf("GET %s: status %d, want %d", path, recorder.Code, want)
			continue
		}
		if want == http.StatusOK && !strings.Contains(recorder.Body.String(), "<title>BonBon</title>") {
			t.Errorf("GET %s did not serve the UI page", path)
		}
	}
}
