package ctxmgr

import (
	"os"
	"path/filepath"
)

// LoadPrompt 读 compact 专用 prompt；不进入 chat Snapshot hash。
func LoadPrompt(dir, kind string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "compact", kind+".md"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
