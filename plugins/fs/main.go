package main

import (
	"encoding/json"
	"os"
)

type req struct{
	ID string `json:"id"`
	Op string `json:"op"`
	Args map[string]any `json:"args"`
}

type resp struct{
	ID string `json:"id"`
	OK bool `json:"ok"`
	Data any `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func main(){
	var in req
	if err := json.NewDecoder(os.Stdin).Decode(&in);err != nil{
		write(resp{OK: false,Error: err.Error()})
		os.Exit(1)
	}
	if in.Op != "read"{
		write(resp{ID: in.ID,OK: false,Error: "unknown_op: " + in.Op})
		os.Exit(1)
	}
	path,_ :=in.Args["path"].(string)
	if path == ""{
		write(resp{ID: in.ID,OK: false,Error: "path_required"})
		os.Exit(1)
	}
	b,err := os.ReadFile(path)
	if err != nil{
		write(resp{ID: in.ID,OK: false,Error: err.Error()})
		os.Exit(1)
	}
	write(resp{ID: in.ID,OK: true,Data: map[string]string{"content":string(b)}})
}

func write(r resp){
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(r)
}