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

type fakeHosReview struct {
	got    []map[string]any
	status int
	reply  string
}

func (f *fakeHosReview) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/px0/review" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.got = append(f.got, body)
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func reviewServer(t *testing.T, f *fakeHosReview) *Server {
	t.Helper()
	srv := f.server(t)
	s := hosSnagServer(t, &hosLink{url: srv.URL, token: "tok", client: srv.Client()})
	s.pr = &prSession{target: PRTarget{Provider: "github", Owner: "acme", Repo: "shop", Number: 42, URL: "https://github.com/acme/shop/pull/42"}, meta: PRMeta{Number: 42, HeadRef: "fix-cart"}}
	return s
}

func localJSON(s *Server, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestHosReviewStartSendsThePRAndItsHeadBranch(t *testing.T) {
	f := &fakeHosReview{status: http.StatusCreated, reply: `{"topicId":"t1","chatId":"c1","file":"/brain/work/x/reviews/PR-42.md"}`}
	s := reviewServer(t, f)

	rec := localJSON(s, "/api/hos/review", `{"action":"start","chat":true}`)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"chatId":"c1"`) || !strings.Contains(rec.Body.String(), "PR-42.md") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if len(f.got) != 1 || f.got[0]["action"] != "start" || f.got[0]["pr"] != "https://github.com/acme/shop/pull/42" || f.got[0]["head"] != "fix-cart" || f.got[0]["chat"] != true {
		t.Fatalf("hos got %v", f.got)
	}
	meta := httptest.NewRecorder()
	s.ServeHTTP(meta, httptest.NewRequest(http.MethodGet, "/api/pr/meta", nil))
	if !strings.Contains(meta.Body.String(), `"hosReview":true`) {
		t.Fatalf("meta should say a review session is on: %s", meta.Body.String())
	}
}

func TestHosReviewForwardsDraftCommentsOnlyDuringASession(t *testing.T) {
	f := &fakeHosReview{status: http.StatusCreated, reply: `{"ref":"C1"}`}
	s := reviewServer(t, f)
	comment := `{"path":"src/cart.ts","line":11,"side":"left","body":"  why?  "}`

	if rec := localJSON(s, "/api/pr/comments", comment); rec.Code != http.StatusOK || len(f.got) != 0 {
		t.Fatalf("before a session nothing goes to hos: %d %v", rec.Code, f.got)
	}
	localJSON(s, "/api/hos/review", `{"action":"start","chat":false}`)
	rec := localJSON(s, "/api/pr/comments", comment)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hosRef":"C1"`) || !strings.Contains(rec.Body.String(), `"id":2`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	c := f.got[1]
	if c["action"] != "comment" || c["pr"] != "https://github.com/acme/shop/pull/42" || c["path"] != "src/cart.ts" || c["line"] != float64(11) || c["side"] != "LEFT" || c["body"] != "why?" {
		t.Fatalf("hos got %v", c)
	}

	if rec := localJSON(s, "/api/hos/review", `{"action":"end"}`); rec.Code != http.StatusOK || f.got[2]["action"] != "end" {
		t.Fatalf("end: %d %v", rec.Code, f.got)
	}
	localJSON(s, "/api/pr/comments", comment)
	if len(f.got) != 3 {
		t.Fatalf("after End review comments stay in px0, hos got %v", f.got)
	}
	if len(s.pr.comments) != 3 {
		t.Fatalf("every comment is still a px0 draft, got %d", len(s.pr.comments))
	}
}

func TestHosReviewKeepsTheDraftWhenHosRefusesAComment(t *testing.T) {
	f := &fakeHosReview{status: http.StatusCreated, reply: `{"chatId":null}`}
	s := reviewServer(t, f)
	localJSON(s, "/api/hos/review", `{"action":"start","chat":false}`)
	f.status, f.reply = http.StatusNotFound, `{"error":"that PR is not linked to a topic yet"}`

	rec := localJSON(s, "/api/pr/comments", `{"path":"a.go","line":1,"body":"x"}`)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hosError":"that PR is not linked to a topic yet"`) || len(s.pr.comments) != 1 {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestHosReviewRefusals(t *testing.T) {
	f := &fakeHosReview{status: http.StatusConflict, reply: `{"error":"no work folder"}`}
	s := reviewServer(t, f)
	if rec := localJSON(s, "/api/hos/review", `{"action":"start","chat":true}`); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "no work folder") {
		t.Fatalf("hos refusing start: %d %s", rec.Code, rec.Body.String())
	}
	if rec := localJSON(s, "/api/hos/review", `{"action":"pause"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown action: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/hos/review", strings.NewReader(`{"action":"start"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a POST from another origin: %d", rec.Code)
	}

	notPR := hosSnagServer(t, &hosLink{url: "http://127.0.0.1:9", token: "tok", client: http.DefaultClient})
	if rec := localJSON(notPR, "/api/hos/review", `{"action":"start"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("outside a PR session: %d", rec.Code)
	}
	off := hosSnagServer(t, nil)
	off.pr = s.pr
	if rec := localJSON(off, "/api/hos/review", `{"action":"start"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("without hos: %d", rec.Code)
	}
}
