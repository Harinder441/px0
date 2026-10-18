package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadHosLinkPrefersLaunchEnvThenFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hos.env")
	if err := os.WriteFile(file, []byte("# hand-set\nexport HOS_URL=\"http://127.0.0.1:9/\"\nHOS_PX0_TOKEN=filetoken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOS_URL": "http://127.0.0.1:4000", "HOS_PX0_TOKEN": "envtoken"}
	got := loadHosLink(func(k string) string { return env[k] }, file)
	if got == nil || got.url != "http://127.0.0.1:4000" || got.token != "envtoken" {
		t.Fatalf("launch env should win, got %+v", got)
	}
	env = map[string]string{"HOS_URL": "http://127.0.0.1:4000"}
	got = loadHosLink(func(k string) string { return env[k] }, file)
	if got == nil || got.url != "http://127.0.0.1:9" || got.token != "filetoken" {
		t.Fatalf("half a launch env should fall back to the file, got %+v", got)
	}
	if got := loadHosLink(func(string) string { return "" }, filepath.Join(dir, "missing")); got != nil {
		t.Fatalf("nothing configured should leave the link off, got %+v", got)
	}
}

func hosSnagServer(t *testing.T, link *hosLink) *Server {
	t.Helper()
	isolateSettings(t)
	ix := NewIndex(t.TempDir())
	ix.Build()
	s := NewServer(ix, nil)
	s.hos = link
	t.Cleanup(func() {
		if s.gitWatcher != nil {
			s.gitWatcher.Stop()
		}
	})
	return s
}

func postSnag(s *Server, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/hos/snag", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestHosSnagForwardsToHosWithTheToken(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]string
	hos := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ref":"S42"}`))
	}))
	defer hos.Close()
	s := hosSnagServer(t, &hosLink{url: hos.URL, token: "tok", client: hos.Client()})

	rec := postSnag(s, "application/json", `{"kind":"question","text":"  why is this slow?\n\nFile: /r/a.go  "}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ref":"S42"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if gotAuth != "Bearer tok" || gotPath != "/px0/snags" {
		t.Fatalf("hos got auth %q path %q", gotAuth, gotPath)
	}
	if gotBody["kind"] != "question" || gotBody["text"] != "why is this slow?\n\nFile: /r/a.go" {
		t.Fatalf("hos got body %v", gotBody)
	}
}

func TestHosSnagForwardsFilesAsIs(t *testing.T) {
	var gotBody struct {
		Kind  string           `json:"kind"`
		Text  string           `json:"text"`
		Files []map[string]any `json:"files"`
	}
	hos := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ref":"S78"}`))
	}))
	defer hos.Close()
	s := hosSnagServer(t, &hosLink{url: hos.URL, token: "tok", client: hos.Client()})

	big := strings.Repeat("QUFB", 1<<20/4*3)
	rec := postSnag(s, "application/json", `{"kind":"task","text":"","files":[{"name":"shot.png","data":"aGk="},{"name":"big.bin","data":"`+big+`"}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ref":"S78"`) {
		t.Fatalf("a snag with files and no note: got %d %s", rec.Code, rec.Body.String())
	}
	if len(gotBody.Files) != 2 || gotBody.Files[0]["name"] != "shot.png" || gotBody.Files[0]["data"] != "aGk=" || gotBody.Files[1]["data"] != big {
		t.Fatalf("hos got files %v", len(gotBody.Files))
	}
}

func TestHosSnagRefusals(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unknown px0 token"}`))
	}))
	defer refusing.Close()
	s := hosSnagServer(t, &hosLink{url: refusing.URL, token: "old", client: refusing.Client()})
	cases := []struct {
		name, contentType, body string
		code                    int
		want                    string
	}{
		{"form posts from other sites", "text/plain", `{"kind":"task","text":"x"}`, http.StatusUnsupportedMediaType, "send JSON"},
		{"unknown kind", "application/json", `{"kind":"bug","text":"x"}`, http.StatusBadRequest, "kind must be"},
		{"blank text", "application/json", `{"kind":"task","text":"   "}`, http.StatusBadRequest, "needs text"},
		{"hos refuses", "application/json", `{"kind":"task","text":"x"}`, http.StatusBadGateway, "unknown px0 token"},
		{"too many files", "application/json", `{"kind":"task","text":"x","files":[` + strings.TrimSuffix(strings.Repeat(`{"name":"a","data":""},`, 11), ",") + `]}`, http.StatusBadRequest, "at most 10 files"},
		{"body over the cap", "application/json", `{"kind":"task","text":"x","files":[{"name":"a","data":"` + strings.Repeat("A", hosSnagBodyMax) + `"}]}`, http.StatusRequestEntityTooLarge, "25 MB"},
	}
	for _, c := range cases {
		rec := postSnag(s, c.contentType, c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: got %d %s", c.name, rec.Code, rec.Body.String())
		}
	}

	off := hosSnagServer(t, nil)
	if rec := postSnag(off, "application/json", `{"kind":"task","text":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("without hos the route should be off, got %d", rec.Code)
	}
}
