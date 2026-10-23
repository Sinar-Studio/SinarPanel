package main

// fs-helper: proses anak yang dijalankan panel dengan UID/GID/grup user yang
// login. Semua operasi file terjadi di sini, sehingga hak akses ditegakkan oleh
// kernel — user biasa tidak bisa menyentuh file yang tidak bisa ia akses lewat SSH.
//
// Protokol: stdin = 1 baris JSON request + payload opsional.
// stdout = 1 baris JSON header {ok,error,data} + payload opsional (download).

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

type fsReq struct {
	Op        string   `json:"op"`
	Path      string   `json:"path,omitempty"`
	Dest      string   `json:"dest,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Mode      string   `json:"mode,omitempty"`
	Overwrite bool     `json:"overwrite,omitempty"`
	Max       int64    `json:"max,omitempty"`
}

type fsHeader struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type fsEntry struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
	Perm   string `json:"perm"`
	Owner  string `json:"owner"`
	Group  string `json:"group"`
	MTime  int64  `json:"mtime"`
	Target string `json:"target,omitempty"`
	ToDir  bool   `json:"to_dir,omitempty"`
}

const (
	maxExtractBytes   = 20 << 30
	maxExtractEntries = 200000
	maxListEntries    = 20000
)

func runFSHelper() int {
	syscall.Umask(0o022)
	in := bufio.NewReaderSize(os.Stdin, 64<<10)
	out := bufio.NewWriterSize(os.Stdout, 64<<10)
	defer out.Flush()

	line, err := in.ReadBytes('\n')
	if err != nil {
		writeHeader(out, nil, errors.New("request kosong"))
		return 1
	}
	var req fsReq
	if err := json.Unmarshal(line, &req); err != nil {
		writeHeader(out, nil, errors.New("request rusak"))
		return 1
	}
	if err := helperDispatch(&req, in, out); err != nil {
		return 1
	}
	return 0
}

func writeHeader(out *bufio.Writer, data any, err error) {
	h := fsHeader{OK: err == nil}
	if err != nil {
		h.Error = friendlyErr(err)
	} else if data != nil {
		h.Data, _ = json.Marshal(data)
	}
	b, _ := json.Marshal(h)
	out.Write(b)
	out.WriteByte('\n')
}

func friendlyErr(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "Akses ditolak (permission denied): " + pathOf(err)
	case errors.Is(err, fs.ErrNotExist):
		return "Tidak ditemukan: " + pathOf(err)
	case errors.Is(err, fs.ErrExist):
		return "Sudah ada: " + pathOf(err)
	}
	return err.Error()
}

func pathOf(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Path
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.New
	}
	return ""
}

func cleanAbs(p string) (string, error) {
	if p == "" || !strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("path harus absolut: %q", p)
	}
	return filepath.Clean(p), nil
}

var protectedPaths = map[string]bool{
	"/usr/bin": true, "/usr/sbin": true, "/usr/lib": true, "/usr/local": true, "/usr/share": true,
	"/var/lib": true, "/var/log": true, "/var/www": true, "/etc/ssh": true, "/etc/pam.d": true,
	"/etc/apache2": true, "/etc/letsencrypt": true, "/etc/sinarpanel": true, "/root/.ssh": true,
}

// Cegah salah klik fatal (mis. hapus /etc) — berlaku juga untuk root.
func checkNotProtected(p string) error {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if p == "/" || len(parts) < 2 || protectedPaths[p] {
		return fmt.Errorf("%s dilindungi, tidak bisa dihapus/dipindah dari panel", p)
	}
	return nil
}

func helperDispatch(req *fsReq, in *bufio.Reader, out *bufio.Writer) error {
	p, err := cleanAbs(req.Path)
	if err != nil && req.Op != "delete" && req.Op != "zip" {
		writeHeader(out, nil, err)
		return err
	}
	var data any
	switch req.Op {
	case "list":
		data, err = fsList(p)
	case "read":
		data, err = fsRead(p, req.Max)
	case "write":
		data, err = fsWrite(p, in, req.Max)
	case "upload":
		data, err = fsUpload(p, in, req.Max, req.Overwrite)
	case "mkdir":
		err = os.Mkdir(p, 0o755)
	case "touch":
		var f *os.File
		if f, err = os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
			err = f.Close()
		}
	case "rename":
		err = fsRename(p, req.Dest)
	case "copy":
		err = fsCopy(p, req.Dest)
	case "delete":
		data, err = fsDelete(req.Paths)
	case "chmod":
		err = fsChmod(p, req.Mode)
	case "extract":
		data, err = fsExtract(p, req.Dest, req.Overwrite)
	case "download":
		return fsDownload(p, out)
	case "zip":
		return fsZip(req.Paths, out)
	default:
		err = errors.New("operasi tidak dikenal")
	}
	writeHeader(out, data, err)
	return err
}

var (
	uidCache = map[uint32]string{}
	gidCache = map[uint32]string{}
)

func uidName(id uint32) string {
	if n, ok := uidCache[id]; ok {
		return n
	}
	n := strconv.FormatUint(uint64(id), 10)
	if u, err := user.LookupId(n); err == nil {
		n = u.Username
	}
	uidCache[id] = n
	return n
}

func gidName(id uint32) string {
	if n, ok := gidCache[id]; ok {
		return n
	}
	n := strconv.FormatUint(uint64(id), 10)
	if g, err := user.LookupGroupId(n); err == nil {
		n = g.Name
	}
	gidCache[id] = n
	return n
}

func permString(m fs.FileMode) string {
	p := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		p |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		p |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		p |= 0o1000
	}
	return fmt.Sprintf("%04o", p)
}

func makeEntry(dir string, fi fs.FileInfo) fsEntry {
	e := fsEntry{Name: fi.Name(), Size: fi.Size(), Mode: fi.Mode().String(), Perm: permString(fi.Mode()), MTime: fi.ModTime().Unix()}
	switch {
	case fi.IsDir():
		e.Type = "dir"
	case fi.Mode()&fs.ModeSymlink != 0:
		e.Type = "link"
		full := filepath.Join(dir, fi.Name())
		e.Target, _ = os.Readlink(full)
		if st, err := os.Stat(full); err == nil {
			e.ToDir = st.IsDir()
		}
	case fi.Mode().IsRegular():
		e.Type = "file"
	default:
		e.Type = "other"
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		e.Owner, e.Group = uidName(st.Uid), gidName(st.Gid)
	}
	return e
}

func fsList(p string) (any, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s bukan direktori", p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	infos, err := f.Readdir(maxListEntries + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	truncated := len(infos) > maxListEntries
	if truncated {
		infos = infos[:maxListEntries]
	}
	entries := make([]fsEntry, 0, len(infos))
	for _, fi := range infos {
		entries = append(entries, makeEntry(p, fi))
	}
	sort.Slice(entries, func(i, j int) bool {
		di := entries[i].Type == "dir" || entries[i].ToDir
		dj := entries[j].Type == "dir" || entries[j].ToDir
		if di != dj {
			return di
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	writable := syscall.Access(p, 2) == nil
	return map[string]any{"path": p, "entries": entries, "truncated": truncated, "writable": writable}, nil
}

func fsRead(p string, max int64) (any, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("bukan file biasa")
	}
	if fi.Size() > max {
		return nil, fmt.Errorf("file terlalu besar untuk editor (maks %d MB) — download saja", max>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return nil, errors.New("file biner tidak bisa dibuka di editor — download saja")
	}
	return map[string]any{"content": string(data), "size": fi.Size(), "mtime": fi.ModTime().Unix(), "perm": permString(fi.Mode())}, nil
}

// fsWrite menulis di tempat (bukan rename) supaya owner & permission tetap.
func fsWrite(p string, in io.Reader, max int64) (any, error) {
	data, err := io.ReadAll(io.LimitReader(in, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("isi terlalu besar")
	}
	flags := os.O_WRONLY | os.O_TRUNC
	if fi, err := os.Lstat(p); err == nil {
		if !fi.Mode().IsRegular() {
			return nil, errors.New("bukan file biasa")
		}
	} else {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(p, flags, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return map[string]any{"size": len(data)}, nil
}

func fsUpload(p string, in io.Reader, max int64, overwrite bool) (any, error) {
	dir := filepath.Dir(p)
	tmp, err := os.CreateTemp(dir, ".sinarpanel-upload-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	n, err := io.Copy(tmp, io.LimitReader(in, max+1))
	if err == nil && n > max {
		err = fmt.Errorf("file melebihi batas upload %d MB", max>>20)
	}
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	if overwrite {
		if fi, err := os.Lstat(p); err == nil && fi.IsDir() {
			return nil, fmt.Errorf("%s adalah folder", p)
		}
		err = os.Rename(tmpName, p)
	} else if err = os.Link(tmpName, p); err != nil && !errors.Is(err, fs.ErrExist) {
		// Filesystem tanpa hardlink: fallback cek-lalu-rename.
		if _, serr := os.Lstat(p); serr == nil {
			return nil, &fs.PathError{Op: "upload", Path: p, Err: fs.ErrExist}
		}
		err = os.Rename(tmpName, p)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"size": n}, nil
}

func fsRename(src, dst string) error {
	dst, err := cleanAbs(dst)
	if err != nil {
		return err
	}
	if err := checkNotProtected(src); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return &fs.PathError{Op: "rename", Path: dst, Err: fs.ErrExist}
	}
	return os.Rename(src, dst)
}

func fsCopy(src, dst string) error {
	dst, err := cleanAbs(dst)
	if err != nil {
		return err
	}
	if dst == src || strings.HasPrefix(dst+"/", src+"/") {
		return errors.New("tidak bisa menyalin folder ke dalam dirinya sendiri")
	}
	if _, err := os.Lstat(dst); err == nil {
		return &fs.PathError{Op: "copy", Path: dst, Err: fs.ErrExist}
	}
	return copyTree(src, dst)
}

func copyTree(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		t, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(t, dst)
	case fi.IsDir():
		if err := os.Mkdir(dst, fi.Mode().Perm()|0o700); err != nil {
			return err
		}
		ents, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(dst, fi.Mode().Perm())
	case fi.Mode().IsRegular():
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return nil // device/socket dilewati
}

func fsDelete(paths []string) (any, error) {
	if len(paths) == 0 {
		return nil, errors.New("tidak ada yang dipilih")
	}
	var clean []string
	for _, p := range paths {
		c, err := cleanAbs(p)
		if err != nil {
			return nil, err
		}
		if err := checkNotProtected(c); err != nil {
			return nil, err
		}
		clean = append(clean, c)
	}
	deleted := 0
	for _, c := range clean {
		if err := os.RemoveAll(c); err != nil {
			return map[string]any{"deleted": deleted}, err
		}
		deleted++
	}
	return map[string]any{"deleted": deleted}, nil
}

func fsChmod(p, mode string) error {
	m, err := strconv.ParseUint(mode, 8, 32)
	if err != nil || m > 0o7777 {
		return errors.New("mode harus oktal, contoh 0644 atau 755")
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return errors.New("tidak bisa chmod symlink")
	}
	return syscall.Chmod(p, uint32(m))
}

func fsDownload(p string, out *bufio.Writer) error {
	f, err := os.Open(p)
	if err != nil {
		writeHeader(out, nil, err)
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err == nil && !fi.Mode().IsRegular() {
		err = errors.New("bukan file biasa")
	}
	if err != nil {
		writeHeader(out, nil, err)
		return err
	}
	writeHeader(out, map[string]any{"name": filepath.Base(p), "size": fi.Size()}, nil)
	_, err = io.Copy(out, f)
	return err
}

// fsZip men-stream zip dari file/folder terpilih. Symlink tidak diikuti.
func fsZip(paths []string, out *bufio.Writer) error {
	var clean []string
	for _, p := range paths {
		c, err := cleanAbs(p)
		if err == nil {
			_, err = os.Lstat(c)
		}
		if err != nil {
			writeHeader(out, nil, err)
			return err
		}
		clean = append(clean, c)
	}
	if len(clean) == 0 {
		err := errors.New("tidak ada yang dipilih")
		writeHeader(out, nil, err)
		return err
	}
	name := "download.zip"
	if len(clean) == 1 {
		name = filepath.Base(clean[0]) + ".zip"
	}
	writeHeader(out, map[string]any{"name": name}, nil)
	zw := zip.NewWriter(out)
	for _, root := range clean {
		base := filepath.Dir(root)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // lewati yang tidak bisa dibaca
			}
			rel, _ := filepath.Rel(base, p)
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if d.IsDir() {
				hdr, _ := zip.FileInfoHeader(info)
				hdr.Name = rel + "/"
				_, err := zw.CreateHeader(hdr)
				return err
			}
			if !d.Type().IsRegular() {
				return nil
			}
			f, err := os.Open(p)
			if err != nil {
				return nil
			}
			defer f.Close()
			hdr, _ := zip.FileInfoHeader(info)
			hdr.Name = rel
			hdr.Method = zip.Deflate
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			_, err = io.Copy(w, f)
			return err
		})
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

// ---- Extract (anti zip-slip, symlink & device dilewati) ----

type extractStats struct {
	Files   int `json:"files"`
	Dirs    int `json:"dirs"`
	Skipped int `json:"skipped"`
	Unsafe  int `json:"unsafe"`
	total   int64
}

func safeJoin(dest, name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", false
	}
	t := filepath.Join(dest, name)
	if t != dest && !strings.HasPrefix(t, dest+"/") {
		return "", false
	}
	return t, true
}

func fsExtract(archive, dest string, overwrite bool) (any, error) {
	if dest == "" {
		dest = filepath.Dir(archive)
	}
	dest, err := cleanAbs(dest)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	st := &extractStats{}
	lower := strings.ToLower(archive)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		err = extractZip(archive, dest, overwrite, st)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		err = extractTar(archive, dest, true, overwrite, st)
	case strings.HasSuffix(lower, ".tar"):
		err = extractTar(archive, dest, false, overwrite, st)
	default:
		err = errors.New("format tidak didukung (zip, tar, tar.gz, tgz)")
	}
	return st, err
}

func extractFile(target string, r io.Reader, mode fs.FileMode, overwrite bool, st *extractStats) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(target); err == nil {
		if !overwrite || !fi.Mode().IsRegular() {
			st.Skipped++
			return nil
		}
	}
	mode &= 0o777
	if mode == 0 {
		mode = 0o644
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxExtractBytes-st.total+1))
	st.total += n
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && st.total > maxExtractBytes {
		err = errors.New("isi arsip terlalu besar (batas 20 GB)")
	}
	if err == nil {
		st.Files++
	}
	return err
}

func extractZip(archive, dest string, overwrite bool, st *extractStats) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	if len(zr.File) > maxExtractEntries {
		return errors.New("arsip berisi terlalu banyak file")
	}
	for _, zf := range zr.File {
		target, ok := safeJoin(dest, zf.Name)
		if !ok {
			st.Unsafe++
			continue
		}
		m := zf.Mode()
		switch {
		case m.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			st.Dirs++
		case m.IsRegular():
			rc, err := zf.Open()
			if err != nil {
				return err
			}
			err = extractFile(target, rc, m, overwrite, st)
			rc.Close()
			if err != nil {
				return err
			}
		default:
			st.Unsafe++
		}
	}
	return nil
}

func extractTar(archive, dest string, gz, overwrite bool, st *extractStats) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if gz {
		g, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer g.Close()
		r = g
	}
	tr := tar.NewReader(r)
	for count := 0; ; count++ {
		if count > maxExtractEntries {
			return errors.New("arsip berisi terlalu banyak file")
		}
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, ok := safeJoin(dest, h.Name)
		if !ok {
			st.Unsafe++
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			st.Dirs++
		case tar.TypeReg:
			if err := extractFile(target, tr, fs.FileMode(h.Mode), overwrite, st); err != nil {
				return err
			}
		default:
			st.Unsafe++
		}
	}
}
