package plugin

import (
	"fmt"
	"path/filepath"
)

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
	return rel, nil
}
