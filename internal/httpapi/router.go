package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

func NewMux(db *sql.DB) *http.ServeMux{
	mux:=http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter,r *http.Request){
		handleHealthz(w,r,db)
	})
	return mux
}

func handleHealthz(w http.ResponseWriter, r *http.Request,db *sql.DB){
	w.Header().Set("Content-Type","application/json")
	if err:=db.Ping();err!=nil{
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "db"})
		return
	}
	w.WriteHeader(http.StatusOK)
	_=json.NewEncoder(w).Encode(map[string]bool{"ok":true})
}