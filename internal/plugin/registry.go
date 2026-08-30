package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Registry struct {
	Workspace string
	byName map[string]Plugin
}

func Load(pluginDir, workspace string)(*Registry,error){
	entries,err := os.ReadDir(pluginDir)
	if err != nil{
		return nil,err
	}
	r := &Registry{
		Workspace: workspace,
		byName: map[string]Plugin{},
	}
	for _,e := range entries{
		if !e.IsDir(){
			continue
		}
		dir := filepath.Join(pluginDir,e.Name())
		b,err := os.ReadFile(filepath.Join(dir,"plugin.json"))
		if err != nil{
			continue
		}
		var man Manifest
		if err := json.Unmarshal(b,&man);err!=nil{
			return nil,err
		}
		r.byName[man.Name] = &Process{dir:dir,man:man,work:workspace}
	}
	return r,nil
}

func (r *Registry) Put(p Plugin) {
	r.byName[p.Manifest().Name] = p
}

func (r *Registry)Tools() []Tool{
	var out []Tool
	for _,p :=range r.byName{
		m := p.Manifest()
		for _,op := range m.Ops{
			risk := m.Risk
			out = append(out, Tool{
				Name:        m.Name + "." + op.Name,
				Description: op.Description,
				Risk:        risk,
				Parameters:  op.Parameters,
			})
			if op.Name == "write" {
				out[len(out)-1].Risk = "write"
			}
		}
	}
	return out
}

func (r *Registry) Exec(ctx context.Context, name, op string, args map[string]any) (json.RawMessage, error) {
	p, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("unknown_plugin: %s", name)
	}
	return p.Exec(ctx, op, args)
}