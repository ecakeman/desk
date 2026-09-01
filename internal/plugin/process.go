package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Process 以外进程执行插件二进制；stdin 一行 JSON，stdout 一行 JSON。
type Process struct {
	dir  string
	man  Manifest
	jail string
}

func (p *Process) Manifest() Manifest {
	return p.man
}

// Exec 若 args 含 path 则先 jail；cwd=jail，超时杀进程组。
func (p *Process) Exec(ctx context.Context, op string, args map[string]any) (json.RawMessage, error) {
	ok := false
	for _, o := range p.man.Ops {
		if o.Name == op {
			ok = true
			break
		}
	}
	if !ok {
		return nil, fmt.Errorf("unknown_op: %s", op)
	}
	if args == nil {
		args = map[string]any{}
	}
	if path, has := args["path"].(string); has {
		rel, err := ResolveInWorkspace(p.jail, path)
		if err != nil {
			return nil, err
		}
		args["path"] = rel
	}
	raw, err := json.Marshal(request{ID: "1", Op: op, Args: args})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	bin := filepath.Join(p.dir, p.man.Command)
	abs, err := filepath.Abs(bin)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, abs)
	cmd.Dir = p.jail
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = bytes.NewReader(append(raw, '\n'))
	out, execErr := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	var r response
	if len(out) > 0 {
		if err := json.Unmarshal(out, &r); err == nil && !r.OK && r.Error != "" {
			return nil, fmt.Errorf("%s", r.Error)
		}
	}
	if execErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("timeout")
		}
		return nil, execErr
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	if !r.OK {
		return nil, fmt.Errorf("%s", r.Error)
	}
	return r.Data, nil
}
