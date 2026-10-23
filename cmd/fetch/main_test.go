package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CelesTrak answers 403 to GitHub's servers; one thing in our hands is to say
// who is asking instead of sending Go's default User-Agent.
func TestDownloadSendsAUserAgent(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.Write([]byte("OBJECT_NAME,OBJECT_ID\nISS,1998-067A\n"))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "x.csv")
	n, err := download(&http.Client{Timeout: 5 * time.Second}, srv.URL, path)
	if err != nil || n != 1 {
		t.Fatalf("rows %d, err %v", n, err)
	}
	if !strings.Contains(ua, "orbitscreen") || strings.HasPrefix(ua, "Go-http-client") {
		t.Fatalf("User-Agent %q", ua)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadRefusalIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusForbidden) }))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "x.csv")
	if _, err := download(&http.Client{Timeout: 5 * time.Second}, srv.URL, path); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a refused download must leave no file")
	}
}
