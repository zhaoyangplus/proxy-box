package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func (a *appRuntime) handleBypassCN(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUserErr(rw, errors.New("请使用 POST"))
		return
	}
	var req struct {
		Enable bool `json:"enable"`
		Retry  bool `json:"retry"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeUserErr(rw, err)
		return
	}
	if err := a.setBypassCN(r.Context(), req.Enable, req.Retry); err != nil {
		writeUserErr(rw, err)
		return
	}
	body, err := a.coreStatusResponse()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, body)
}
