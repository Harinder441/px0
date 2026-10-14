package main

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const revealSupported = runtime.GOOS == "darwin"

var revealExec = func(abs string) error {
	return exec.Command("open", "-R", abs).Run()
}

func (s *Server) revealTarget(rel string) (string, bool) {
	abs, _, ok := s.safePath(rel)
	if !ok {
		return "", false
	}
	// EvalSymlinks fails on a missing path, so it doubles as the existence check.
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false
	}
	root, err := filepath.EvalSymlinks(s.ix.Root())
	if err != nil {
		root = s.ix.Root()
	}
	// A symlink inside the root can point outside it, so re-check the resolved target.
	if real != root && !strings.HasPrefix(real, root+string(filepath.Separator)) {
		return "", false
	}
	return real, true
}

func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST a path")
		return
	}
	if !revealSupported {
		fail(w, http.StatusNotImplemented, "revealing in the file manager is only supported on macOS")
		return
	}
	abs, ok := s.revealTarget(r.URL.Query().Get("path"))
	if !ok {
		fail(w, http.StatusBadRequest, "path is outside the workspace or does not exist")
		return
	}
	if err := revealExec(abs); err != nil {
		fail(w, http.StatusInternalServerError, "could not open the file manager: "+err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
