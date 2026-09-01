// Command search 在 cwd 上子串 grep；只读。
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type req struct {
	ID   string         `json:"id"`
	Op   string         `json:"op"`
	Args map[string]any `json:"args"`
}

type resp struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

type hit struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func main() {
	var in req
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		write(resp{OK: false, Error: err.Error()})
		os.Exit(1)
	}
	if in.Op != "grep" {
		write(resp{ID: in.ID, OK: false, Error: "unknown_op: " + in.Op})
		os.Exit(1)
	}
	query, _ := in.Args["query"].(string)
	if query == "" {
		write(resp{ID: in.ID, OK: false, Error: "query_required"})
		os.Exit(1)
	}
	glob, _ := in.Args["glob"].(string)

	var hits []hit
	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			if d.Name() == "node_modules" || d.Name() == "vendor" || p == "go" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 256*1024 {
			return nil
		}
		rel := filepath.ToSlash(p)
		if glob != "" && !globOK(glob, rel) {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		n := 0
		for sc.Scan() {
			n++
			if !strings.Contains(sc.Text(), query) {
				continue
			}
			hits = append(hits, hit{Path: rel, Line: n, Text: sc.Text()})
			if len(hits) >= 50 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		write(resp{ID: in.ID, OK: false, Error: err.Error()})
		os.Exit(1)
	}
	write(resp{ID: in.ID, OK: true, Data: map[string]any{"hits": hits}})
}

func globOK(glob, rel string) bool {
	if ok, err := filepath.Match(glob, rel); err == nil && ok {
		return true
	}
	ok, err := filepath.Match(glob, filepath.Base(rel))
	return err == nil && ok
}

func write(r resp) {
	_ = json.NewEncoder(os.Stdout).Encode(r)
}
