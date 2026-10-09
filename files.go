package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	badChars = regexp.MustCompile(`[<>:"|?*\x00-\x1f]`)
	textExt  = map[string]bool{".txt": true, ".md": true, ".log": true, ".csv": true, ".json": true, ".yml": true, ".yaml": true,
		".ini": true, ".toml": true, ".js": true, ".ts": true, ".py": true, ".css": true, ".java": true, ".c": true,
		".cpp": true, ".h": true, ".go": true, ".rs": true, ".sh": true, ".bat": true, ".sql": true}
	forceDownload = map[string]bool{"text/html": true, "application/xhtml+xml": true, "image/svg+xml": true, "text/xml": true, "application/xml": true}
)

func cleanName(s string) string {
	s = badChars.ReplaceAllString(s, "_")
	return strings.TrimRight(strings.TrimSpace(s), ". ")
}

func visible(n string) bool { return !strings.HasPrefix(n, ".") && !strings.HasSuffix(n, ".part") }

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolve turns a user-supplied relative path into a real path inside the shared folder.
func (a *App) resolve(rel string, create bool) (string, error) {
	parts := []string{a.root}
	for _, x := range strings.Split(strings.ReplaceAll(rel, "\\", "/"), "/") {
		if x == "" || x == "." {
			continue
		}
		if x == ".." {
			return "", errors.New("Invalid path")
		}
		if create {
			x = cleanName(x)
		}
		if x == "" || strings.HasPrefix(x, ".") {
			return "", errors.New("Invalid name")
		}
		parts = append(parts, x)
	}
	full := filepath.Join(parts...)
	probe := full
	for { // follow symlinks on the part that exists, so nothing can point outside the folder
		if real, err := filepath.EvalSymlinks(probe); err == nil {
			if !within(a.root, real) {
				return "", errors.New("Invalid path")
			}
			break
		}
		next := filepath.Dir(probe)
		if next == probe {
			break
		}
		probe = next
	}
	return full, nil
}

func (a *App) rel(full string) string {
	r, err := filepath.Rel(a.root, full)
	if err != nil || r == "." {
		return ""
	}
	return filepath.ToSlash(r)
}

func (a *App) unique(full string) string {
	taken := func(p string) bool {
		_, err := os.Lstat(p)
		return err == nil || a.inflight[p]
	}
	if !taken(full) {
		return full
	}
	d, n := filepath.Split(full)
	ext := filepath.Ext(n)
	base := strings.TrimSuffix(n, ext)
	for i := 1; ; i++ {
		c := filepath.Join(d, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if !taken(c) {
			return c
		}
	}
}

func fail(w http.ResponseWriter, err error) {
	code := 400
	msg := err.Error()
	if errors.Is(err, os.ErrNotExist) {
		code, msg = 404, "Not found"
	} else if errors.Is(err, os.ErrExist) {
		code, msg = 409, "That name is already taken"
	}
	apiErr(w, code, msg)
}

// ---------------------------------------------------------------- listing

type Entry struct {
	Name string  `json:"name"`
	Dir  bool    `json:"dir"`
	T    float64 `json:"t"`
	N    int     `json:"n"`
	Size int64   `json:"size"`
}

func (a *App) apiLs(w http.ResponseWriter, r *http.Request) {
	full, err := a.resolve(r.URL.Query().Get("p"), false)
	if err != nil {
		fail(w, err)
		return
	}
	des, err := os.ReadDir(full)
	if err != nil {
		apiErr(w, 404, "Folder not found")
		return
	}
	out := []Entry{}
	for _, de := range des {
		if !visible(de.Name()) {
			continue
		}
		info, err := os.Stat(filepath.Join(full, de.Name()))
		if err != nil {
			continue
		}
		e := Entry{Name: de.Name(), Dir: info.IsDir(), T: float64(info.ModTime().UnixNano()) / 1e9}
		if e.Dir {
			if kids, err := os.ReadDir(filepath.Join(full, de.Name())); err == nil {
				for _, k := range kids {
					if visible(k.Name()) {
						e.N++
					}
				}
			}
		} else {
			e.Size = info.Size()
		}
		out = append(out, e)
	}
	writeJSON(w, 200, map[string]any{"path": a.rel(full), "entries": out})
}

type Node struct {
	Name string `json:"name"`
	C    []Node `json:"c"`
}

func folderTree(dir string, budget *int) []Node {
	des, err := os.ReadDir(dir)
	out := []Node{}
	if err != nil {
		return out
	}
	sort.Slice(des, func(i, j int) bool { return strings.ToLower(des[i].Name()) < strings.ToLower(des[j].Name()) })
	for _, de := range des {
		if !visible(de.Name()) || !de.IsDir() {
			continue
		}
		if *budget <= 0 {
			break
		}
		*budget--
		out = append(out, Node{de.Name(), folderTree(filepath.Join(dir, de.Name()), budget)})
	}
	return out
}

func (a *App) apiTree(w http.ResponseWriter, r *http.Request) {
	b := 3000
	writeJSON(w, 200, map[string]any{"tree": folderTree(a.root, &b)})
}

// ---------------------------------------------------------------- download

func contentType(name string, download bool) (ctype, kind string) {
	ext := strings.ToLower(filepath.Ext(name))
	ctype = mime.TypeByExtension(ext)
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	if textExt[ext] {
		ctype = "text/plain; charset=utf-8"
	}
	base, _, _ := mime.ParseMediaType(ctype)
	if download || forceDownload[base] {
		return "application/octet-stream", "attachment"
	}
	return ctype, "inline"
}

func dispo(kind, name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r > 126 || r < 32 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf("%s; filename=\"%s\"; filename*=UTF-8''%s", kind, ascii, strings.ReplaceAll(url.PathEscape(name), "+", "%2B"))
}

func (a *App) getFile(w http.ResponseWriter, r *http.Request) {
	full, err := a.resolve(r.PathValue("path"), false)
	if err != nil {
		fail(w, err)
		return
	}
	a.serveFile(w, r, full, r.URL.Query().Get("dl") == "1", "", false)
}

func (a *App) serveFile(w http.ResponseWriter, r *http.Request, full string, download bool, forced string, cache bool) {
	f, err := os.Open(full)
	if err != nil {
		apiErr(w, 404, "File not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		apiErr(w, 404, "File not found")
		return
	}
	name := filepath.Base(full)
	ctype, kind := contentType(name, download)
	if forced != "" {
		ctype, kind = forced, "inline"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", dispo(kind, name))
	h.Set("X-Content-Type-Options", "nosniff")
	if strings.ToLower(filepath.Ext(name)) != ".pdf" {
		h.Set("Content-Security-Policy", "sandbox")
	}
	if cache {
		h.Set("Cache-Control", "private, max-age=86400")
	} else {
		h.Set("Cache-Control", "no-store")
	}
	http.ServeContent(w, r, name, st.ModTime(), f) // handles Range, so videos can seek
}

// ---------------------------------------------------------------- upload

// saveStream writes body to a unique name inside the shared folder. The file only appears under
// its real name once it is complete, so an interrupted transfer never leaves a half file.
func (a *App) saveStream(full string, body io.Reader) (string, error) {
	parent := filepath.Dir(full)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", errors.New("Can't create that folder")
	}
	if st, err := os.Stat(parent); err != nil || !st.IsDir() {
		return "", errors.New("A file with that name is in the way")
	}
	a.mu.Lock()
	dest := a.unique(full)
	a.inflight[dest] = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.inflight, dest); a.mu.Unlock() }()
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", errors.New("Can't save the file here")
	}
	_, err = io.Copy(f, body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", errors.New("Transfer interrupted")
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dest, nil
}

func (a *App) putFile(w http.ResponseWriter, r *http.Request) {
	full, err := a.resolve(r.PathValue("path"), true)
	if err != nil || full == a.root {
		apiErr(w, 400, "Missing or invalid file name")
		return
	}
	if r.ContentLength < 0 {
		apiErr(w, 411, "Length required")
		return
	}
	dest, err := a.saveStream(full, r.Body)
	if err != nil {
		apiErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]string{"path": a.rel(dest)})
}

// ---------------------------------------------------------------- folder actions

func (a *App) apiMkdir(w http.ResponseWriter, r *http.Request) {
	var q struct{ Path string }
	_ = readJSON(r, &q)
	full, err := a.resolve(q.Path, true)
	if err != nil || full == a.root {
		apiErr(w, 400, "Invalid name")
		return
	}
	if _, err := os.Lstat(full); err == nil {
		apiErr(w, 409, "That name is already taken")
		return
	}
	if err := os.MkdirAll(full, 0o755); err != nil {
		apiErr(w, 500, "Can't create that folder")
		return
	}
	writeJSON(w, 200, map[string]string{"path": a.rel(full)})
}

func (a *App) apiRename(w http.ResponseWriter, r *http.Request) {
	var q struct{ Path, Name string }
	_ = readJSON(r, &q)
	src, err := a.resolve(q.Path, false)
	name := cleanName(q.Name)
	if err != nil || src == a.root {
		apiErr(w, 404, "Not found")
		return
	}
	if name == "" || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		apiErr(w, 400, "Invalid name")
		return
	}
	dst := filepath.Join(filepath.Dir(src), name)
	if dst != src {
		if _, err := os.Lstat(dst); err == nil {
			apiErr(w, 409, "That name is already taken")
			return
		}
		if err := os.Rename(src, dst); err != nil {
			fail(w, err)
			return
		}
	}
	writeJSON(w, 200, map[string]string{"path": a.rel(dst)})
}

func (a *App) apiMove(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Src []string
		Dst string
	}
	_ = readJSON(r, &q)
	dst, err := a.resolve(q.Dst, false)
	if st, e2 := os.Stat(dst); err != nil || e2 != nil || !st.IsDir() {
		apiErr(w, 404, "Destination folder not found")
		return
	}
	moved := 0
	for _, s := range q.Src {
		src, err := a.resolve(s, false)
		if err != nil || src == a.root {
			continue
		}
		if _, err := os.Lstat(src); err != nil || filepath.Dir(src) == dst {
			continue
		}
		if st, _ := os.Stat(src); st != nil && st.IsDir() && (src == dst || within(src, dst)) {
			apiErr(w, 400, "A folder can't go inside itself")
			return
		}
		a.mu.Lock()
		target := a.unique(filepath.Join(dst, filepath.Base(src)))
		err = os.Rename(src, target)
		a.mu.Unlock()
		if err != nil {
			fail(w, err)
			return
		}
		moved++
	}
	writeJSON(w, 200, map[string]int{"moved": moved})
}

func (a *App) apiDelete(w http.ResponseWriter, r *http.Request) {
	var q struct{ Paths []string }
	_ = readJSON(r, &q)
	n := 0
	var gone []string
	for _, s := range q.Paths {
		full, err := a.resolve(s, false)
		if err != nil || full == a.root {
			continue
		}
		if _, err := os.Lstat(full); err != nil {
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			fail(w, err)
			return
		}
		n++
		gone = append(gone, a.rel(full))
	}
	if len(gone) > 0 {
		go a.removeOnDevices(gone)
	}
	writeJSON(w, 200, map[string]int{"deleted": n})
}

func (a *App) apiTextGet(w http.ResponseWriter, r *http.Request) {
	b, _ := os.ReadFile(filepath.Join(a.root, ".note.txt"))
	writeJSON(w, 200, map[string]string{"text": string(b)})
}

func (a *App) apiTextSet(w http.ResponseWriter, r *http.Request) {
	var q struct{ Text string }
	_ = readJSON(r, &q)
	if len(q.Text) > 500000 {
		q.Text = q.Text[:500000]
	}
	_ = os.WriteFile(filepath.Join(a.root, ".note.txt"), []byte(q.Text), 0o644)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------- zip

func (a *App) getZip(w http.ResponseWriter, r *http.Request) {
	var targets []string
	for _, p := range r.URL.Query()["p"] {
		full, err := a.resolve(p, false)
		if err != nil {
			fail(w, err)
			return
		}
		if _, err := os.Stat(full); err != nil {
			apiErr(w, 404, "Not found")
			return
		}
		targets = append(targets, full)
	}
	if len(targets) == 0 {
		targets = []string{a.root}
	}
	name := "Drop.zip"
	if len(targets) == 1 && targets[0] != a.root {
		if st, _ := os.Stat(targets[0]); st != nil && st.IsDir() {
			name = filepath.Base(targets[0]) + ".zip"
		}
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", dispo("attachment", name))
	zw := zip.NewWriter(w)
	defer zw.Close()
	for _, t := range targets {
		base := filepath.Dir(t)
		if t == a.root {
			base = a.root
		}
		_ = filepath.Walk(t, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if p != a.root && !visible(info.Name()) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(base, p)
			if rel == "." {
				return nil
			}
			hdr, err := zip.FileInfoHeader(info)
			if err != nil {
				return nil
			}
			hdr.Name = filepath.ToSlash(rel)
			if info.IsDir() {
				hdr.Name += "/"
				_, _ = zw.CreateHeader(hdr)
				return nil
			}
			hdr.Method = zip.Store // nothing is recompressed
			fw, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			f, err := os.Open(p)
			if err != nil {
				return nil
			}
			defer f.Close()
			_, err = io.Copy(fw, f)
			return err
		})
	}
}

// ---------------------------------------------------------------- thumbnails

func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 < len(b) {
		if b[i] != 0xFF {
			return 1
		}
		m, l := b[i+1], int(b[i+2])<<8|int(b[i+3])
		if m == 0xE1 && i+10 < len(b) && string(b[i+4:i+8]) == "Exif" {
			t := b[i+10:]
			if len(t) < 8 {
				return 1
			}
			var u16 func([]byte) int
			var u32 func([]byte) int
			if t[0] == 'I' {
				u16 = func(x []byte) int { return int(x[0]) | int(x[1])<<8 }
				u32 = func(x []byte) int { return int(x[0]) | int(x[1])<<8 | int(x[2])<<16 | int(x[3])<<24 }
			} else {
				u16 = func(x []byte) int { return int(x[0])<<8 | int(x[1]) }
				u32 = func(x []byte) int { return int(x[0])<<24 | int(x[1])<<16 | int(x[2])<<8 | int(x[3]) }
			}
			o := u32(t[4:8])
			if o+2 > len(t) {
				return 1
			}
			n := u16(t[o:])
			for k := 0; k < n; k++ {
				e := o + 2 + k*12
				if e+12 > len(t) {
					return 1
				}
				if u16(t[e:]) == 0x0112 {
					return u16(t[e+8:])
				}
			}
			return 1
		}
		i += 2 + l
	}
	return 1
}

func shrink(src image.Image, max int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
		return dst
	}
	nw, nh := max, max
	if w > h {
		nh = h * max / w
	} else {
		nw = w * max / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := y*h/nh, (y+1)*h/nh
		if y1 == y0 {
			y1 = y0 + 1
		}
		for x := 0; x < nw; x++ {
			x0, x1 := x*w/nw, (x+1)*w/nw
			if x1 == x0 {
				x1 = x0 + 1
			}
			var r, g, bl, n uint32
			sy, sx := (y1-y0+3)/4, (x1-x0+3)/4 // sample, don't visit every pixel
			for yy := y0; yy < y1; yy += sy {
				for xx := x0; xx < x1; xx += sx {
					cr, cg, cb, _ := src.At(b.Min.X+xx, b.Min.Y+yy).RGBA()
					r, g, bl, n = r+cr, g+cg, bl+cb, n+1
				}
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = uint8(r/n>>8), uint8(g/n>>8), uint8(bl/n>>8), 255
		}
	}
	return dst
}

func orient(im *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return im
	}
	w, h := im.Bounds().Dx(), im.Bounds().Dy()
	nw, nh := w, h
	if o >= 5 {
		nw, nh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			copy(out.Pix[out.PixOffset(nx, ny):out.PixOffset(nx, ny)+4], im.Pix[im.PixOffset(x, y):im.PixOffset(x, y)+4])
		}
	}
	return out
}

func (a *App) getThumb(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	full, err := a.resolve(rel, false)
	if err != nil {
		fail(w, err)
		return
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		apiErr(w, 404, "File not found")
		return
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", full, st.ModTime().UnixNano(), st.Size())))
	cached := filepath.Join(a.root, ".thumbs", hex.EncodeToString(sum[:])+".jpg")
	if _, err := os.Stat(cached); err != nil {
		a.thumbSem <- struct{}{}
		ok := a.makeThumb(full, cached)
		<-a.thumbSem
		if !ok { // can't decode it here: let the browser try the original
			http.Redirect(w, r, "/f/"+(&url.URL{Path: rel}).EscapedPath(), http.StatusFound)
			return
		}
	}
	a.serveFile(w, r, cached, false, "image/jpeg", true)
}

func (a *App) makeThumb(full, out string) bool {
	if st, err := os.Stat(full); err != nil || st.Size() > 120<<20 {
		return false
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return false
	}
	src, format, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return false
	}
	im := shrink(src, 520)
	if format == "jpeg" {
		im = orient(im, exifOrientation(b))
	}
	_ = os.MkdirAll(filepath.Dir(out), 0o755)
	tmp := out + fmt.Sprintf(".%d.tmp", time.Now().UnixNano())
	f, err := os.Create(tmp)
	if err != nil {
		return false
	}
	err = jpeg.Encode(f, im, &jpeg.Options{Quality: 82})
	f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return false
	}
	return os.Rename(tmp, out) == nil
}

var _ = png.Decode
var _ = gif.Decode
