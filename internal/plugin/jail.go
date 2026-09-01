package plugin

import (
	"fmt"
	"os"
	"path/filepath"
)

// JailTooBroad 拒绝把 jail 设成文件系统根。
func JailTooBroad(root string) bool {
	abs, err := filepath.Abs(root)
	if err != nil {
		return true
	}
	abs = filepath.Clean(abs)
	sep := string(os.PathSeparator)
	return abs == sep || abs == filepath.VolumeName(abs)+sep
}

// ResolveInWorkspace 把 path 收进 jail，挡住 ..、绝对路径逃逸和 symlink 逃逸。
func ResolveInWorkspace(work, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path_required")
	}
	root, err := filepath.Abs(work)
	if err != nil {
		return "", err
	}
	cand := p
	if !filepath.IsAbs(cand) {
		cand = filepath.Join(root, p)
	}
	cand, err = filepath.Abs(cand)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, cand)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path_escaped: %s", p)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realCandidate, err := resolveExistingPrefix(cand)
	if err != nil {
		return "", err
	}
	realRel, err := filepath.Rel(realRoot, realCandidate)
	if err != nil || !filepath.IsLocal(realRel) {
		return "", fmt.Errorf("path_escaped: %s", p)
	}
	return rel, nil
}

func resolveExistingPrefix(path string) (string, error) {
	current := path
	var missing []string
	for {
		if _, err := os.Lstat(current); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("path_not_found: %s", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, missing[i])
	}
	return resolved, nil
}
