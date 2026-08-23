package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
)

type Process struct {
	dir string
	man Manifest
	work string
}

func (p *Process) Manifest() Manifest {
	return p.man
}

func (p *Process) Exec(ctx context.Context,op string,args map[string]any) (json.RawMessage,error){
	ok :=false
	for _,o := range p.man.Ops{
		if o == op{
			ok =true
			break
		}
	}
	if !ok{
		return nil,fmt.Errorf("unknown_op: %s",op)
	}
	raw, err := json.Marshal(request{ID: "1", Op: op,Args: args})
	if err != nil{
		return nil,err
	}
	bin := filepath.Join(p.dir,p.man.Command)
	cmd := exec.CommandContext(ctx,bin)
	cmd.Dir = p.work
	cmd.Stdin = bytes.NewReader(append(raw,'\n'))
	out,err := cmd.Output()
	if err != nil{
		return nil,err
	}
	var r response
	if err := json.Unmarshal(out,&r);err!= nil{
		return nil,err
	}
	if !r.OK{
		return nil,fmt.Errorf("%s",r.Error)
	}
	return r.Data,nil
}