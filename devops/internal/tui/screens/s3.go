package screens

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/auth"
	"devops/internal/aws/console"
	"devops/internal/aws/meter"
	"devops/internal/aws/s3x"
	"devops/internal/executor"
	"devops/internal/preview"
	"devops/internal/safety"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

type S3 struct{ *Resource }

func NewS3() *S3 {
	s3x.CleanTemp()
	return &S3{NewResource("s3", S3Buckets)}
}

func S3Buckets() *Level {
	return &Level{
		Title: "S3 Buckets", Noun: "buckets", CacheKind: "s3",
		Columns: []col{{Title: "Bucket", Width: 30, Flex: true}, {Title: "Region", Width: 15}, {Title: "Created", Width: 11}},
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			bs, next, err := s3x.BucketsPage(ctx, s, token)
			rows := make([]components.Row, 0, len(bs))
			for _, b := range bs {
				rows = append(rows, components.Row{Key: b.Name, Data: b, Cells: []string{b.Name, dash(b.Region), b.Created.Format("2006-01-02")}})
			}
			return rows, next, err
		},
		Child: func(r components.Row) *Level {
			b := r.Data.(s3x.Bucket)
			return s3Objects(b.Name, &regionRef{v: b.Region}, "")
		},
		Link: func(region string, r components.Row) string { return console.S3(region, r.Key, "") },
		Actions: []Action{
			{Key: "i", Label: "Security check", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("s3.bucket.security", map[string]string{"bucket": r.Key})
			}},
		},
		Empty: "No buckets in this account.",
	}
}

var (
	imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}
	videoExt = map[string]bool{".mp4": true, ".mov": true, ".webm": true, ".mkv": true, ".avi": true, ".m4v": true}
	textExt  = map[string]bool{".txt": true, ".log": true, ".json": true, ".yaml": true, ".yml": true, ".csv": true, ".tsv": true,
		".md": true, ".xml": true, ".html": true, ".htm": true, ".js": true, ".ts": true, ".go": true, ".py": true, ".sh": true,
		".conf": true, ".cfg": true, ".ini": true, ".toml": true, ".sql": true, ".tf": true, ".properties": true, ".env": true}
)

func fileIcon(e s3x.Entry) string {
	ext := strings.ToLower(path.Ext(e.Name))
	switch {
	case e.Folder:
		return theme.Title.Render("▸")
	case imageExt[ext]:
		return theme.OK.Render("▣")
	case videoExt[ext]:
		return theme.Warn.Render("▶")
	case textExt[ext]:
		return theme.Body.Render("≡")
	}
	return theme.Dim.Render("·")
}

// regionRef holds a bucket's region, resolved lazily by a background load.
type regionRef struct {
	mu sync.Mutex
	v  string
}

func (r *regionRef) get() string  { r.mu.Lock(); defer r.mu.Unlock(); return r.v }
func (r *regionRef) set(v string) { r.mu.Lock(); r.v = v; r.mu.Unlock() }

func s3Objects(bucket string, region *regionRef, prefix string) *Level {
	title := bucket
	if prefix != "" {
		title = strings.TrimSuffix(path.Base(strings.TrimSuffix(prefix, "/")), "/") + "/"
	}
	return &Level{
		Title: title, Noun: "objects", CacheKind: "s3",
		Columns: []col{{Title: "", Width: 1}, {Title: "Name", Width: 30, Flex: true}, {Title: "Size", Width: 10},
			{Title: "Modified", Width: 16}, {Title: "Class", Width: 12}},
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			reg := region.get()
			if reg == "" {
				var err error
				if reg, err = s3x.Region(ctx, s, bucket); err != nil {
					return nil, "", err
				}
				region.set(reg)
			}
			es, next, err := s3x.ListPage(ctx, s, reg, bucket, prefix, token)
			rows := make([]components.Row, 0, len(es))
			for _, en := range es {
				size, mod := "", ""
				if !en.Folder {
					size, mod = components.Bytes(en.Size), en.Modified.Local().Format("2006-01-02 15:04")
				}
				tone := theme.ToneNormal
				if strings.Contains(en.StorageClass, "GLACIER") || en.StorageClass == "DEEP_ARCHIVE" {
					tone = theme.ToneDim
				}
				rows = append(rows, components.Row{Key: en.Key, Data: en, Tone: tone,
					Cells: []string{fileIcon(en), en.Name, size, mod, en.StorageClass}})
			}
			return rows, next, err
		},
		Open: func(e *core.Env, r components.Row) (Viewer, tea.Cmd) {
			en := r.Data.(s3x.Entry)
			if en.Folder {
				return nil, nil // fall through to Child
			}
			return newS3Preview(e, bucket, region.get(), en)
		},
		Child: func(r components.Row) *Level { return s3Objects(bucket, region, r.Key) },
		Link: func(reg string, r components.Row) string {
			if en, ok := r.Data.(s3x.Entry); ok && !en.Folder {
				return console.S3Object(reg, bucket, en.Key)
			}
			return console.S3(reg, bucket, r.Key)
		},
		Actions: []Action{
			{Key: "P", Label: "Open in browser", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return presignOpen(e, bucket, region.get(), r.Key)
			}},
		},
		Empty: "This folder is empty.",
	}
}

func presignOpen(e *core.Env, bucket, region, key string) tea.Cmd {
	s := e.Session
	return e.Run("s3", "view:presign", executor.Spec{Name: "Presign S3 object", Quiet: true, Resources: []string{bucket + "/" + key}},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			return s3x.Presign(ctx, s, region, bucket, key)
		})
}

// Update handles presigned-link results for the list (P key).
func (s *S3) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	if m, ok := msg.(core.ResultMsg); ok && m.Key == "view:presign" && s.view == nil {
		if m.Err != nil {
			return e.Toast("Could not create a link: "+safety.Describe(m.Err), theme.ToneBad)
		}
		return e.OpenURL(m.Value.(string), true)
	}
	return s.Resource.Update(e, msg)
}

type previewKind int

const (
	kindUnknown previewKind = iota
	kindImage
	kindVideo
	kindText
)

type s3Preview struct {
	bucket, region string
	entry          s3x.Entry
	kind           previewKind
	head           *s3x.Head
	err            error
	loading        bool
	needConfirm    bool
	img            image.Image
	file           string
	text           []string
	note           string
	offset         int
	wrap           bool
	rendered       string
	renderKey      string
}

func newS3Preview(e *core.Env, bucket, region string, en s3x.Entry) (*s3Preview, tea.Cmd) {
	p := &s3Preview{bucket: bucket, region: region, entry: en, wrap: true}
	ext := strings.ToLower(path.Ext(en.Key))
	switch {
	case imageExt[ext]:
		p.kind = kindImage
	case videoExt[ext]:
		p.kind = kindVideo
	case textExt[ext]:
		p.kind = kindText
	}
	return p, p.start(e)
}

func (p *s3Preview) limit(e *core.Env) int64 { return int64(e.Deps.Config.Limits.MaxPreviewMB) << 20 }

func (p *s3Preview) start(e *core.Env) tea.Cmd {
	p.loading = true
	s, reg, b, k := e.Session, p.region, p.bucket, p.entry.Key
	return e.Run("s3", "view:head", executor.Spec{Name: "Inspect S3 object", Quiet: true, Resources: []string{b + "/" + k}},
		func(ctx context.Context, _ *executor.Job) (any, error) { return s3x.HeadObject(ctx, s, reg, b, k) })
}

func (p *s3Preview) fetch(e *core.Env) tea.Cmd {
	p.loading, p.needConfirm = true, false
	s, reg, b, k, kind := e.Session, p.region, p.bucket, p.entry.Key, p.kind
	if kind == kindUnknown && p.head != nil && strings.HasPrefix(p.head.ContentType, "text/") {
		kind = kindText
		p.kind = kindText
	}
	limit := max(p.limit(e), p.head.Size+1)
	spec := executor.Spec{Name: "Preview " + path.Base(k), Resources: []string{b + "/" + k}}
	switch kind {
	case kindText:
		return e.Run("s3", "view:text", spec, func(ctx context.Context, _ *executor.Job) (any, error) {
			return s3x.ReadRange(ctx, s, reg, b, k, 64<<10)
		})
	case kindImage:
		return e.Run("s3", "view:image", spec, func(ctx context.Context, _ *executor.Job) (any, error) {
			f, err := s3x.Download(ctx, s, reg, b, k, limit)
			if err != nil {
				return nil, err
			}
			fh, err := os.Open(f)
			if err != nil {
				return nil, err
			}
			defer fh.Close()
			img, _, err := image.Decode(fh)
			if err != nil {
				return nil, fmt.Errorf("could not decode image: %w", err)
			}
			return imageResult{img, f}, nil
		})
	case kindVideo:
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			p.loading = false
			p.note = "No preview available — install ffmpeg to see a frame. Press P to play it in your browser/player."
			return nil
		}
		spec.Timeout = 60 * time.Second
		return e.Run("s3", "view:image", spec, func(ctx context.Context, _ *executor.Job) (any, error) {
			u, err := s3x.Presign(ctx, s, reg, b, k)
			if err != nil {
				return nil, err
			}
			// ffmpeg reads just enough of the object (via a 5-minute presigned
			// URL) to grab one frame about a second in.
			cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-ss", "1", "-i", u,
				"-frames:v", "1", "-f", "image2pipe", "-vcodec", "png", "-")
			out, err := cmd.Output()
			if err != nil || len(out) == 0 {
				return nil, fmt.Errorf("ffmpeg could not read a frame from this video")
			}
			img, _, err := image.Decode(bytes.NewReader(out))
			if err != nil {
				return nil, err
			}
			f, _ := os.CreateTemp(s3x.TempDir(), "frame-*.png")
			if f != nil {
				f.Write(out)
				f.Close()
				return imageResult{img, f.Name()}, nil
			}
			return imageResult{img: img}, nil
		})
	}
	p.loading = false
	p.note = "No preview available for this file type. Press P to open it in your browser, or o for the S3 console."
	return nil
}

type imageResult struct {
	img  image.Image
	file string
}

func (p *s3Preview) Capturing() bool { return false }

func (p *s3Preview) Back(e *core.Env) bool {
	if p.file != "" {
		os.Remove(p.file)
	}
	return false
}

func (p *s3Preview) Hints() []core.Hint {
	h := []core.Hint{{Key: "P", Label: "Open in browser"}, {Key: "o", Label: "Console"}, {Key: "y", Label: "Copy s3:// URI"}}
	if p.kind == kindImage || p.kind == kindVideo {
		h = append(h, core.Hint{Key: "F", Label: "Full resolution"})
	}
	if p.kind == kindText {
		h = append(h, core.Hint{Key: "w", Label: "Wrap"}, core.Hint{Key: "↑↓", Label: "Scroll"})
	}
	return append(h, core.Hint{Key: "esc", Label: "Back"})
}

func (p *s3Preview) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.ResultMsg:
		switch m.Key {
		case "view:head":
			p.loading, p.err = false, m.Err
			if h, ok := m.Value.(s3x.Head); ok {
				p.head = &h
				switch {
				case h.Archived:
					p.note = "This object is archived (Glacier / Deep Archive) and must be restored before it can be read."
					return nil
				case p.kind == kindImage && h.Size > p.limit(e):
					p.needConfirm = true
					return nil
				}
				return p.fetch(e)
			}
		case "view:text":
			p.loading, p.err = false, m.Err
			if b, ok := m.Value.([]byte); ok {
				p.text = textPreview(b, path.Ext(p.entry.Key))
				if p.head != nil && p.head.Size > int64(len(b)) {
					p.note = fmt.Sprintf("Showing the first %s of %s.", components.Bytes(int64(len(b))), components.Bytes(p.head.Size))
				}
			}
		case "view:image":
			p.loading, p.err = false, m.Err
			if r, ok := m.Value.(imageResult); ok {
				p.img, p.file = r.img, r.file
			}
		case "view:presign":
			if m.Err != nil {
				return e.Toast("Could not create a link: "+safety.Describe(m.Err), theme.ToneBad)
			}
			return e.OpenURL(m.Value.(string), true)
		}
	case core.MouseMsg:
		if m.WheelUp {
			p.offset = max(0, p.offset-3)
		}
		if m.WheelDown {
			p.offset += 3
		}
	case tea.KeyMsg:
		switch m.String() {
		case "y":
			return e.Copy("s3://"+p.bucket+"/"+p.entry.Key, "S3 URI")
		case "o":
			return e.OpenURL(console.S3Object(e.Ctx().Region, p.bucket, p.entry.Key), false)
		case "P":
			return presignOpen(e, p.bucket, p.region, p.entry.Key)
		case "Y", "enter":
			if p.needConfirm {
				return p.fetch(e)
			}
		case "w":
			p.wrap = !p.wrap
		case "up", "k":
			p.offset = max(0, p.offset-1)
		case "down", "j":
			p.offset++
		case "pgup":
			p.offset = max(0, p.offset-10)
		case "pgdown":
			p.offset += 10
		case "F":
			if p.file == "" {
				return nil
			}
			if preview.Detect() == preview.None {
				return e.Toast("Full-resolution preview isn't available in "+preview.Name()+" — showing the block preview", theme.ToneWarn)
			}
			self, err := os.Executable()
			if err != nil {
				return e.Toast(err.Error(), theme.ToneBad)
			}
			return tea.ExecProcess(exec.Command(self, "--show-image", p.file), func(error) tea.Msg { return nil })
		}
	}
	return nil
}

func textPreview(b []byte, ext string) []string {
	if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b[:max(0, len(b)-4)]) {
		return []string{theme.Dim.Render("Binary content — no text preview available.")}
	}
	s := safety.Redact(string(b))
	switch strings.ToLower(ext) {
	case ".json":
		var x any
		if json.Unmarshal(b, &x) == nil {
			if pretty, err := json.MarshalIndent(x, "", "  "); err == nil {
				s = safety.Redact(string(pretty))
			}
		}
	case ".csv", ".tsv":
		r := csv.NewReader(strings.NewReader(s))
		if ext == ".tsv" {
			r.Comma = '\t'
		}
		r.FieldsPerRecord = -1
		recs, err := r.ReadAll()
		if err == nil && len(recs) > 1 {
			return csvTable(recs)
		}
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

func csvTable(recs [][]string) []string {
	if len(recs) > 200 {
		recs = recs[:200]
	}
	widths := make([]int, len(recs[0]))
	for _, r := range recs {
		for i := range widths {
			if i < len(r) {
				widths[i] = min(30, max(widths[i], len([]rune(r[i]))))
			}
		}
	}
	var out []string
	for n, r := range recs {
		cells := make([]string, len(widths))
		for i := range widths {
			v := ""
			if i < len(r) {
				v = r[i]
			}
			cells[i] = components.Pad(v, widths[i])
		}
		line := strings.Join(cells, "  ")
		if n == 0 {
			line = theme.TableHead.Render(line)
		}
		out = append(out, line)
	}
	return out
}

func (p *s3Preview) View(e *core.Env, w, h int) string {
	title := theme.Title.Render("S3 › ") + p.bucket + "/" + p.entry.Key
	right := ""
	if p.head != nil {
		right = components.Bytes(p.head.Size) + " · " + dash(p.head.ContentType)
	}
	out := headingRaw(title, right, w) + "\n"
	out += theme.Warn.Render("$ ") + theme.Dim.Render(meter.BilledNote("S3", "GetObject")+" — previews read only what they need") + "\n\n"
	body := h - 3
	switch {
	case p.err != nil:
		return out + errorView(p.err, w)
	case p.needConfirm:
		return out + theme.Warn.Render(fmt.Sprintf("This image is %s, above the preview limit of %d MB.", components.Bytes(p.head.Size), e.Deps.Config.Limits.MaxPreviewMB)) +
			"\n\n" + theme.Key.Render("enter") + theme.Dim.Render(" download and preview anyway   ") + theme.Key.Render("P") + theme.Dim.Render(" open in browser   ") +
			theme.Key.Render("esc") + theme.Dim.Render(" back")
	case p.loading:
		return out + loadingView(e, "preview")
	case p.img != nil:
		key := fmt.Sprint(w, body)
		if key != p.renderKey {
			p.rendered, p.renderKey = components.RenderImage(p.img, w, body-1), key
		}
		b := p.img.Bounds()
		info := theme.Dim.Render(fmt.Sprintf("%d×%d px · block preview", b.Dx(), b.Dy()))
		if preview.Detect() != preview.None {
			info += theme.Dim.Render(" · F for full resolution")
		} else {
			info += theme.Dim.Render(" · full resolution isn't available in " + preview.Name())
		}
		return out + p.rendered + "\n" + info
	case p.text != nil:
		var lines []string
		for _, l := range p.text {
			if p.wrap {
				lines = append(lines, components.WrapLines(l, w)...)
			} else {
				lines = append(lines, components.Truncate(l, w))
			}
		}
		if p.note != "" {
			lines = append(lines, "", theme.Dim.Render(p.note))
		}
		p.offset = max(0, min(p.offset, len(lines)-body))
		return out + components.Fit(strings.Join(lines[p.offset:], "\n"), w, body)
	case p.note != "":
		return out + theme.Dim.Render(components.Wrap(p.note, w))
	}
	return out
}
