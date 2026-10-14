package main

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// revealSupported reports whether this build can reveal a file in the OS file
// manager. Only macOS Finder is wired; the UI hides the menu item everywhere
// else via the "reveal" flag on /api/meta.
const revealSupported = runtime.GOOS == "darwin"

// revealExec runs `open -R` against an absolute path. It is a package var so a
// test can stub it instead of launching Finder.
var revealExec = func(abs string) error {
	return exec.Command("open", "-R", abs).Run()
}

// revealTarget validates a client-supplied path relative to the root and
// returns the real absolute path to hand to the file manager. It cleans the
// path, keeps it inside the root, resolves symlinks, and rejects anything that
// escapes the root or does not exist. A directory resolves to the directory
// itself, so Finder reveals the folder.
func (s *Server) revealTarget(rel string) (string, bool) {
	abs, _, ok := s.safePath(rel)
	if !ok {
		return "", false
	}
	// EvalSymlinks also fails for a missing path, so this is the existence
	// check too: a path that is not there cannot be revealed.
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false
	}
	// A symlink inside the root could point outside it; re-check after
	// resolving so the real target is still within the root.
	root, err := filepath.EvalSymlinks(s.ix.Root())
	if err != nil {
		root = s.ix.Root()
	}
	if real != root && !strings.HasPrefix(real, root+string(filepath.Separator)) {
		return "", false
	}
	return real, true
}

// handleReveal opens the OS file manager at the given path. macOS only: other
// platforms return 501 and the UI never shows the entry.
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
