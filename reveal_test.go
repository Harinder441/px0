package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func revealTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	root := t.TempDir()
	// macOS resolves /var to /private/var, so compare against the resolved root.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n")
	write("sub/deep.go", "package sub\n")

	ix := NewIndex(root)
	s := NewServer(ix, nil)
	return s, root, realRoot
}

func TestRevealTargetValidation(t *testing.T) {
	s, root, realRoot := revealTestServer(t)

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	haveSymlink := false
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape")); err == nil {
			haveSymlink = true
		}
	}

	cases := []struct {
		name string
		rel  string
		want bool
	}{
		{"file inside root", "main.go", true},
		{"nested file", "sub/deep.go", true},
		{"directory reveals itself", "sub", true},
		{"root itself", ".", true},
		{"dot-dot escape", "../secret.txt", false},
		{"nested dot-dot escape", "sub/../../secret.txt", false},
		{"absolute path", filepath.Join(outside, "secret.txt"), false},
		{"missing file", "nope.go", false},
	}
	if haveSymlink {
		cases = append(cases, struct {
			name string
			rel  string
			want bool
		}{"symlink escaping root", "escape", false})
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			abs, ok := s.revealTarget(c.rel)
			if ok != c.want {
				t.Fatalf("revealTarget(%q) ok = %v, want %v (abs=%q)", c.rel, ok, c.want, abs)
			}
			if ok {
				if abs != realRoot && !hasRootPrefix(abs, realRoot) {
					t.Fatalf("revealTarget(%q) returned %q outside root %q", c.rel, abs, realRoot)
				}
			}
		})
	}
}

func hasRootPrefix(abs, root string) bool {
	return len(abs) > len(root) && abs[:len(root)] == root && abs[len(root)] == filepath.Separator
}

func TestRevealHandler(t *testing.T) {
	if !revealSupported {
		// On non-macOS the handler must refuse with 501 regardless of path.
		s, _, _ := revealTestServer(t)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reveal?path=main.go", nil))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("non-macOS reveal: status = %d, want 501", rec.Code)
		}
		return
	}

	s, _, realRoot := revealTestServer(t)

	var gotArg string
	orig := revealExec
	revealExec = func(abs string) error { gotArg = abs; return nil }
	defer func() { revealExec = orig }()

	// Valid path: handler succeeds and passes the resolved absolute path to exec.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reveal?path=main.go", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid reveal: status = %d body = %s", rec.Code, rec.Body.String())
	}
	if want := filepath.Join(realRoot, "main.go"); gotArg != want {
		t.Fatalf("reveal exec arg = %q, want %q", gotArg, want)
	}

	// Escaping path: 400 and exec is never called.
	gotArg = ""
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/reveal?path=../secret.txt", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("escaping reveal: status = %d, want 400", rec.Code)
	}
	if gotArg != "" {
		t.Fatalf("escaping reveal reached exec with %q", gotArg)
	}

	// Wrong method: 405.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reveal?path=main.go", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET reveal: status = %d, want 405", rec.Code)
	}
}
