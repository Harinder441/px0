package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// hosLink is the optional bridge to an hos daemon (Harinder's agent OS) for
// "Mark Snag". It stays nil, and the feature inert, unless HOS_URL and
// HOS_PX0_TOKEN are set: hos sets both when it launches px0, and
// ~/.px0/hos.env (KEY=VALUE lines) can set them by hand.
type hosLink struct {
	url    string
	token  string
	client *http.Client
}

const (
	hosSnagTextMax  = 20000
	hosSnagFilesMax = 10
	// hos takes at most 25 MB of files; base64 grows that by 4/3, plus room for the text.
	hosSnagBodyMax = 35 << 20
)

func hosEnvPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "px0", "hos.env")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".px0", "hos.env")
}

func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return out
}

// loadHosLink prefers the launch environment, then the env file; both values
// must come from the same place so a stale file never pairs with a fresh URL.
func loadHosLink(getenv func(string) string, envFile string) *hosLink {
	url, token := strings.TrimSpace(getenv("HOS_URL")), strings.TrimSpace(getenv("HOS_PX0_TOKEN"))
	if url == "" || token == "" {
		file := readEnvFile(envFile)
		url, token = file["HOS_URL"], file["HOS_PX0_TOKEN"]
	}
	if url == "" || token == "" {
		return nil
	}
	return &hosLink{url: strings.TrimRight(url, "/"), token: token, client: &http.Client{Timeout: 30 * time.Second}}
}

// handleHosSnag forwards a snag to hos server-side, so the browser never needs
// hos's origin, session or token.
func (s *Server) handleHosSnag(w http.ResponseWriter, r *http.Request) {
	if s.hos == nil {
		fail(w, http.StatusNotFound, "hos is not connected: set HOS_URL and HOS_PX0_TOKEN, or "+hosEnvPath())
		return
	}
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST a snag")
		return
	}
	// A JSON content type forces a CORS preflight, which px0 never answers, so another site cannot post here.
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, http.StatusUnsupportedMediaType, "send JSON")
		return
	}
	var in struct {
		Kind  string            `json:"kind"`
		Text  string            `json:"text"`
		Files []json.RawMessage `json:"files,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, hosSnagBodyMax)).Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(w, http.StatusRequestEntityTooLarge, "snag files are at most 25 MB in total")
			return
		}
		fail(w, http.StatusBadRequest, "bad JSON")
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Kind != "task" && in.Kind != "question" {
		fail(w, http.StatusBadRequest, "kind must be task or question")
		return
	}
	if (in.Text == "" && len(in.Files) == 0) || len(in.Text) > hosSnagTextMax {
		fail(w, http.StatusBadRequest, "a snag needs text (at most 20000 characters) or a file")
		return
	}
	if len(in.Files) > hosSnagFilesMax {
		fail(w, http.StatusBadRequest, "a snag holds at most 10 files")
		return
	}
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.hos.url+"/px0/snags", bytes.NewReader(body))
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.hos.token)
	res, err := s.hos.client.Do(req)
	if err != nil {
		fail(w, http.StatusBadGateway, "hos did not answer at "+s.hos.url)
		return
	}
	defer res.Body.Close()
	var out struct {
		Ref   string `json:"ref"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&out)
	if res.StatusCode != http.StatusCreated || out.Ref == "" {
		msg := out.Error
		if msg == "" {
			msg = res.Status
		}
		fail(w, http.StatusBadGateway, "hos refused the snag: "+msg)
		return
	}
	writeJSON(w, map[string]string{"ref": out.Ref})
}

func (s *Server) postHosReview(ctx context.Context, payload map[string]any, out any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.hos.url+"/px0/review", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.hos.token)
	res, err := s.hos.client.Do(req)
	if err != nil {
		return errors.New("hos did not answer at " + s.hos.url)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if res.StatusCode != http.StatusCreated {
		var refusal struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &refusal) != nil || refusal.Error == "" {
			refusal.Error = res.Status
		}
		return errors.New(refusal.Error)
	}
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
	return nil
}

// handleHosReview turns a review session on or off; while on, new drafts also go to hos and never to GitHub.
func (s *Server) handleHosReview(w http.ResponseWriter, r *http.Request) {
	if s.hos == nil {
		fail(w, http.StatusNotFound, "hos is not connected: set HOS_URL and HOS_PX0_TOKEN, or "+hosEnvPath())
		return
	}
	if !s.prOrFail(w) {
		return
	}
	if !localPost(w, r) {
		return
	}
	var in struct {
		Action string `json:"action"`
		Chat   bool   `json:"chat"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil || (in.Action != "start" && in.Action != "end") {
		fail(w, http.StatusBadRequest, "action must be start or end")
		return
	}
	p := s.pr
	payload := map[string]any{"action": in.Action, "pr": p.target.URL}
	if in.Action == "start" {
		payload["head"], payload["chat"] = p.meta.HeadRef, in.Chat
	}
	var out map[string]any
	if err := s.postHosReview(r.Context(), payload, &out); err != nil {
		fail(w, http.StatusBadGateway, "hos refused: "+err.Error())
		return
	}
	s.hosReview.Store(in.Action == "start")
	writeJSON(w, out)
}
