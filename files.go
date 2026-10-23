package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type helperRun struct {
	cmd    *exec.Cmd
	out    *bufio.Reader
	hdr    fsHeader
	stderr *limitedBuffer
}

// startHelper menjalankan "sinarpanel fs-helper" sebagai user yang login.
func (a *App) startHelper(ctx context.Context, u User, req fsReq, payload io.Reader) (*helperRun, error) {
	euid := uint32(os.Geteuid())
	if euid != 0 && euid != u.UID {
		return nil, errors.New("panel tidak berjalan sebagai root, tidak bisa berganti user")
	}
	line, _ := json.Marshal(req)
	cmd := exec.CommandContext(ctx, a.selfExe, "fs-helper")
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + u.Home, "USER=" + u.Name, "LANG=C.UTF-8"}
	if euid == 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: u.UID, Gid: u.GID, Groups: u.Groups}}
	}
	var in io.Reader = bytes.NewReader(append(line, '\n'))
	if payload != nil {
		in = io.MultiReader(in, payload)
	}
	cmd.Stdin = in
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &limitedBuffer{n: 8 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	h := &helperRun{cmd: cmd, out: bufio.NewReaderSize(stdout, 64<<10), stderr: stderr}
	hl, err := h.out.ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(hl, &h.hdr)
	}
	if err != nil {
		_ = cmd.Wait()
		return nil, fmt.Errorf("helper gagal: %s", strings.TrimSpace(stderr.String()))
	}
	return h, nil
}

// fsCall untuk operasi non-streaming: kembalikan data JSON atau error.
func (a *App) fsCall(r *http.Request, req fsReq, payload io.Reader) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	h, err := a.startHelper(ctx, sessionFrom(r).User, req, payload)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, h.out)
	_ = h.cmd.Wait()
	if !h.hdr.OK {
		return nil, errors.New(h.hdr.Error)
	}
	return h.hdr.Data, nil
}

func (a *App) fsRespond(w http.ResponseWriter, r *http.Request, action string, req fsReq, payload io.Reader) {
	data, err := a.fsCall(r, req, payload)
	target := req.Path
	if len(req.Paths) > 0 {
		target = strings.Join(req.Paths, ",")
	}
	if req.Op != "list" && req.Op != "read" {
		a.audit(r, "fs-"+action, target, err)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if data == nil {
		data = json.RawMessage(`{}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

func (a *App) handleFSList(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = sessionFrom(r).User.Home
	}
	a.fsRespond(w, r, "list", fsReq{Op: "list", Path: p}, nil)
}

func (a *App) handleFSRead(w http.ResponseWriter, r *http.Request) {
	a.fsRespond(w, r, "read", fsReq{Op: "read", Path: r.URL.Query().Get("path"), Max: a.cfg.MaxEditMB << 20}, nil)
}

func (a *App) handleFSWrite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, (a.cfg.MaxEditMB<<20)*2+4096)
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeErr(w, http.StatusBadRequest, "Content-Type harus application/json")
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "body tidak valid / terlalu besar")
		return
	}
	a.fsRespond(w, r, "write", fsReq{Op: "write", Path: req.Path, Max: a.cfg.MaxEditMB << 20}, strings.NewReader(req.Content))
}

func (a *App) handleFSSimple(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path      string   `json:"path"`
			Dest      string   `json:"dest"`
			Paths     []string `json:"paths"`
			Mode      string   `json:"mode"`
			Overwrite bool     `json:"overwrite"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(body.Paths) > 5000 {
			writeErr(w, http.StatusBadRequest, "terlalu banyak item")
			return
		}
		a.fsRespond(w, r, op, fsReq{Op: op, Path: body.Path, Dest: body.Dest, Paths: body.Paths, Mode: body.Mode, Overwrite: body.Overwrite}, nil)
	}
}

func safeFileName(name string) (string, error) {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." || name == ".." || name == "/" || len(name) > 255 || strings.ContainsRune(name, 0) {
		return "", errors.New("nama file tidak valid")
	}
	return name, nil
}

func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(name))
}

func (a *App) handleFSDownload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	paths := q["path"]
	if len(paths) == 0 {
		writeErr(w, http.StatusBadRequest, "path kosong")
		return
	}
	req := fsReq{Op: "zip", Paths: paths}
	if len(paths) == 1 && q.Get("zip") != "1" {
		req = fsReq{Op: "download", Path: paths[0]}
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	h, err := a.startHelper(ctx, sessionFrom(r).User, req, nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer h.cmd.Wait()
	if !h.hdr.OK {
		writeErr(w, http.StatusBadRequest, h.hdr.Error)
		return
	}
	var meta struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	_ = json.Unmarshal(h.hdr.Data, &meta)
	a.audit(r, "fs-download", strings.Join(paths, ","), nil)

	// Selalu attachment + octet-stream: file user tidak pernah dirender di origin panel.
	hd := w.Header()
	hd.Set("Content-Type", "application/octet-stream")
	hd.Set("Content-Disposition", contentDisposition(meta.Name))
	hd.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if req.Op == "download" {
		hd.Set("Content-Length", fmt.Sprint(meta.Size))
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	_, _ = io.Copy(w, h.out)
}

func (a *App) handleFSUpload(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if !strings.HasPrefix(dir, "/") {
		writeErr(w, http.StatusBadRequest, "folder tujuan tidak valid")
		return
	}
	dir = filepath.Clean(dir)
	overwrite := r.URL.Query().Get("overwrite") == "1"
	max := a.cfg.MaxUploadMB << 20
	r.Body = http.MaxBytesReader(w, r.Body, max+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "upload harus multipart/form-data")
		return
	}
	type result struct {
		Name  string `json:"name"`
		Error string `json:"error,omitempty"`
	}
	var results []result
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, "upload terputus / melebihi batas")
			return
		}
		if part.FormName() != "files" || part.FileName() == "" {
			part.Close()
			continue
		}
		name, err := safeFileName(part.FileName())
		if err != nil {
			results = append(results, result{Name: part.FileName(), Error: err.Error()})
			part.Close()
			continue
		}
		target := filepath.Join(dir, name)
		_, err = a.fsCall(r, fsReq{Op: "upload", Path: target, Max: max, Overwrite: overwrite}, part)
		a.audit(r, "fs-upload", target, err)
		res := result{Name: name}
		if err != nil {
			res.Error = err.Error()
		}
		results = append(results, res)
		part.Close()
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
