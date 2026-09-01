package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func loadFS(t *testing.T) *Registry {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("go", "build", "-o", filepath.Join(root, "plugins/fs/fs"), "./plugins/fs")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fs: %s %v", out, err)
	}
	r, err := Load(filepath.Join(root, "plugins"), root)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPathEscapes(t *testing.T) {
	r := loadFS(t)
	ctx := context.Background()
	_, err := r.Exec(ctx, "fs", "read", map[string]any{"path": ".."})
	if err == nil || !strings.Contains(err.Error(), "path_escaped") {
		t.Fatalf(".. : %v", err)
	}
	_, err = r.Exec(ctx, "fs", "read", map[string]any{"path": "../.."})
	if err == nil || !strings.Contains(err.Error(), "path_escaped") {
		t.Fatalf("../.. : %v", err)
	}
	_, err = r.Exec(ctx, "fs", "read", map[string]any{"path": "/etc/passwd"})
	if err == nil || !strings.Contains(err.Error(), "path_escaped") {
		t.Fatalf("abs: %v", err)
	}
}

func TestNormalRelativePath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("in"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := ResolveInWorkspace(root, "ok.txt")
	if err != nil || rel != "ok.txt" {
		t.Fatalf("rel=%q err=%v", rel, err)
	}
}

func TestPathEscapesThroughSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveInWorkspace(root, "link/file.txt"); err == nil ||
		!strings.Contains(err.Error(), "path_escaped") {
		t.Fatalf("symlink escape: %v", err)
	}
}

func TestPathEscapesThroughNestedSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(nested, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveInWorkspace(root, "a/b/link/file.txt"); err == nil ||
		!strings.Contains(err.Error(), "path_escaped") {
		t.Fatalf("nested symlink: %v", err)
	}
}

func TestWriteAndGrepStayInWorkspace(t *testing.T) {
	r := loadFS(t)
	ctx := context.Background()
	_, err := r.Exec(ctx, "fs", "write", map[string]any{"path": "../x.txt", "content": "no"})
	if err == nil || !strings.Contains(err.Error(), "path_escaped") {
		t.Fatalf("write escape: %v", err)
	}
	root := repoRoot(t)
	search := exec.Command("go", "build", "-o", filepath.Join(root, "plugins/search/search"), "./plugins/search")
	search.Dir = root
	if out, err := search.CombinedOutput(); err != nil {
		t.Fatalf("build search: %s %v", out, err)
	}
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "in.txt"), []byte("desk inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(filepath.Join(root, "plugins"), work)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := reg.Exec(ctx, "search", "grep", map[string]any{"query": "desk", "glob": "../*"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "..") && strings.Contains(string(raw), repoRoot(t)) {
		t.Fatalf("grep escaped: %s", raw)
	}
}

func TestPluginTimeout(t *testing.T) {
	r := loadFS(t)
	start := time.Now()
	_, err := r.Exec(context.Background(), "fs", "sleep", map[string]any{})
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err=%v", err)
	}
	if elapsed > 7*time.Second {
		t.Fatalf("elapsed=%s, want ~5s", elapsed)
	}
}

func TestMissingRootSkipsPlugin(t *testing.T) {
	dir := t.TempDir()
	plug := filepath.Join(dir, "extra")
	if err := os.Mkdir(plug, 0o755); err != nil {
		t.Fatal(err)
	}
	man := []byte(`{"name":"extra","command":"extra","risk":"write","root":"host","ops":[{"name":"run","description":"x","parameters":{}}]}`)
	if err := os.WriteFile(filepath.Join(plug, "plugin.json"), man, 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range reg.Tools() {
		if strings.HasPrefix(tool.Name, "extra.") {
			t.Fatalf("plugin registered without root: %s", tool.Name)
		}
	}
}

func TestWorkspaceTooBroad(t *testing.T) {
	_, err := Load(t.TempDir(), "/")
	if err == nil || !strings.Contains(err.Error(), "workspace_too_broad") {
		t.Fatalf("broad workspace: %v", err)
	}
}
