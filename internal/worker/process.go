package worker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
)

type Process struct {
	Python string
	Script string
	Env    []string

	mu    sync.Mutex
	procs map[string]*child
}

type child struct {
	cmd *exec.Cmd
	in  *json.Encoder
	out *bufio.Scanner
}

func NewProcess(python, script string, env []string) *Process {
	return &Process{Python: python, Script: script, Env: env, procs: map[string]*child{}}
}

func (p *Process) Handle(in In, emit func(Out) error) (*Out, error) {
	if in.RunID == "" {
		return nil, fmt.Errorf("worker_exit")
	}
	ch, err := p.get(in.RunID)
	if err != nil {
		p.Done(in.RunID)
		return nil, err
	}
	if err := ch.in.Encode(in); err != nil {
		p.Done(in.RunID)
		return nil, fmt.Errorf("worker_exit")
	}
	for {
		if !ch.out.Scan() {
			p.Done(in.RunID)
			return nil, fmt.Errorf("worker_exit")
		}
		var out Out
		if err := json.Unmarshal(ch.out.Bytes(), &out); err != nil {
			p.Done(in.RunID)
			return nil, fmt.Errorf("worker_exit")
	    }
		if out.T == "message.delta" {
			if emit != nil{
				if err := emit(out); err != nil{
					return nil,err
				}
			}
			continue
		}
		return &out, nil
	}
}

func (p *Process) Done(runID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.procs[runID]
	if !ok {
		return
	}
	delete(p.procs, runID)
	_ = ch.cmd.Process.Kill()
	_ = ch.cmd.Wait()
}

func (p *Process) get(runID string) (*child, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ch, ok := p.procs[runID]; ok {
		return ch, nil
	}
	cmd := exec.Command(p.Python, p.Script)
	cmd.Env = p.Env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("worker_exit")
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	ch := &child{cmd: cmd, in: json.NewEncoder(stdin), out: sc}
	p.procs[runID] = ch
	return ch, nil
}