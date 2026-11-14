package modelapi

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"runtime"
	"strings"
	"testing"
)

const rwBoundary = "xXxBoundaryxXx"
const rwType = "multipart/form-data; boundary=" + rwBoundary

func rwField(name, value string) string {
	return "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"" + name + "\"\r\n\r\n" + value + "\r\n"
}

func rwFile(name, filename, contentType, content string) string {
	return "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"" + name + "\"; filename=\"" + filename + "\"\r\nContent-Type: " + contentType + "\r\n\r\n" + content + "\r\n"
}

const rwClose = "--" + rwBoundary + "--\r\n"

func TestRewriteMultipartModelChangesOnlyTheModelValue(t *testing.T) {
	audio := "RIFF\x00\x01\r\n--" + rwBoundary + "-not-a-delimiter\r\n\xff\xfe binary"
	cases := []struct{ name, in, want string }{
		{"model first", rwField("model", "friendly") + rwFile("file", "a.wav", "audio/wav", audio) + rwField("language", "en") + rwClose,
			rwField("model", "native-asr") + rwFile("file", "a.wav", "audio/wav", audio) + rwField("language", "en") + rwClose},
		{"model after the file", rwFile("file", "a.wav", "audio/wav", audio) + rwField("model", "friendly") + rwClose,
			rwFile("file", "a.wav", "audio/wav", audio) + rwField("model", "native-asr") + rwClose},
		{"model in the middle, text/plain", rwField("a", "1") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"model\"\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nfriendly\r\n" + rwFile("file", "a.wav", "audio/wav", "x") + rwClose,
			rwField("a", "1") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"model\"\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nnative-asr\r\n" + rwFile("file", "a.wav", "audio/wav", "x") + rwClose},
		{"one CRLF after the closing delimiter is kept", rwField("model", "friendly") + rwClose + "\r\n",
			rwField("model", "native-asr") + rwClose + "\r\n"},
		{"closing delimiter without a final line break", rwField("model", "friendly") + "--" + rwBoundary + "--",
			rwField("model", "native-asr") + "--" + rwBoundary + "--"},
		{"other field named like the model is untouched", rwField("model_hint", "friendly") + rwField("model", "friendly") + rwClose,
			rwField("model_hint", "friendly") + rwField("model", "native-asr") + rwClose},
		{"shorter native name", rwField("model", "a-much-longer-friendly-name") + rwClose, rwField("model", "native-asr") + rwClose},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			original, err := RewriteMultipartModel(&out, strings.NewReader(c.in), rwType, "native-asr")
			if err != nil || out.String() != c.want {
				t.Fatalf("err %v\n got %q\nwant %q", err, out.String(), c.want)
			}
			if got, err := MultipartModel(strings.NewReader(c.in), rwType); err != nil || got != original {
				t.Fatalf("MultipartModel = %q, %v; rewriter saw %q", got, err, original)
			}
		})
	}
}

// The rewritten body parses, with the standard library, to the original parts
// except for the model value.
func TestRewriteMultipartModelAgreesWithStandardParser(t *testing.T) {
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	f, _ := w.CreateFormFile("file", "voice.mp3")
	_, _ = f.Write(bytes.Repeat([]byte("\r\n--abc\r\n"), 500))
	_ = w.WriteField("model", "friendly")
	_ = w.WriteField("temperature", "0.2")
	f2, _ := w.CreateFormFile("file2", "b.bin")
	_, _ = f2.Write([]byte{0, 1, 2})
	_ = w.Close()
	var out bytes.Buffer
	if _, err := RewriteMultipartModel(&out, bytes.NewReader(form.Bytes()), w.FormDataContentType(), "n"); err != nil {
		t.Fatal(err)
	}
	read := func(b []byte) []string {
		r := multipart.NewReader(bytes.NewReader(b), w.Boundary())
		var parts []string
		for {
			p, err := r.NextRawPart()
			if err == io.EOF {
				return parts
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(p)
			parts = append(parts, fmt.Sprintf("%s|%s|%v|%x", p.FormName(), p.FileName(), p.Header, data))
		}
	}
	before, after := read(form.Bytes()), read(out.Bytes())
	if len(before) != len(after) {
		t.Fatalf("part count changed: %d -> %d", len(before), len(after))
	}
	changed := 0
	for i := range before {
		if before[i] != after[i] {
			changed++
			if !strings.HasPrefix(before[i], "model|") || !strings.HasPrefix(after[i], "model|") {
				t.Fatalf("part %d changed: %s", i, before[i][:40])
			}
		}
	}
	if changed != 1 {
		t.Fatalf("%d parts changed, want 1", changed)
	}
	if want := form.Len() + len("n") - len("friendly"); out.Len() != want {
		t.Fatalf("length %d, want %d", out.Len(), want)
	}
}

func TestRewriteMultipartModelRefusesWhatItCannotBeSureOf(t *testing.T) {
	file := rwFile("file", "a.wav", "audio/wav", "x")
	cases := map[string]struct{ in, contentType string }{
		"missing":                 {file + rwField("language", "en") + rwClose, rwType},
		"duplicated":              {rwField("model", "friendly") + file + rwField("model", "friendly") + rwClose, rwType},
		"duplicated, differing":   {rwField("model", "friendly") + rwField("model", "native-asr") + rwClose, rwType},
		"model with a filename":   {rwFile("model", "m.txt", "text/plain", "friendly") + rwClose, rwType},
		"model with filename*":    {"--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"model\"; filename*=UTF-8''m.txt\r\n\r\nfriendly\r\n" + rwClose, rwType},
		"model not plain text":    {"--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"model\"\r\nContent-Type: application/octet-stream\r\n\r\nfriendly\r\n" + rwClose, rwType},
		"model charset":           {"--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"model\"\r\nContent-Type: text/plain; charset=utf-16\r\n\r\nfriendly\r\n" + rwClose, rwType},
		"model transfer encoding": {"--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"model\"\r\nContent-Transfer-Encoding: base64\r\n\r\nZnJpZW5kbHk=\r\n" + rwClose, rwType},
		"model empty":             {rwField("model", "") + rwClose, rwType},
		"model on two lines":      {rwField("model", "friendly\r\nsecond") + rwClose, rwType},
		"model too long":          {rwField("model", strings.Repeat("m", 1025)) + rwClose, rwType},
		"no closing delimiter":    {rwField("model", "friendly") + file, rwType},
		"truncated in a file":     {rwField("model", "friendly") + rwFile("file", "a", "audio/wav", "abc")[:90], rwType},
		"truncated in headers":    {rwField("model", "friendly") + "--" + rwBoundary + "\r\nContent-Disposition: form-da", rwType},
		"bare LF line ends":       {strings.ReplaceAll(rwField("model", "friendly")+rwClose, "\r\n", "\n"), rwType},
		"two dispositions":        {"--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"x\"\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nfriendly\r\n" + rwClose, rwType},
		"part without a name":     {"--" + rwBoundary + "\r\nContent-Type: text/plain\r\n\r\nx\r\n" + rwField("model", "friendly") + rwClose, rwType},
		"no parts":                {rwClose, rwType},
		"empty body":              {"", rwType},
		"missing boundary":        {rwField("model", "friendly") + rwClose, "multipart/form-data"},
		"not multipart":           {rwField("model", "friendly") + rwClose, "application/json"},
		"boundary too long":       {rwField("model", "friendly") + rwClose, "multipart/form-data; boundary=" + strings.Repeat("b", 71)},
		"preamble":                {"preamble text\r\n" + rwField("model", "friendly") + rwClose, rwType},
		"preamble with a fake model behind a padded delimiter": {"--" + rwBoundary + " \r\n" + "Content-Disposition: form-data; name=\"model\"\r\n\r\nexpensive\r\n" + rwField("model", "friendly") + rwClose, rwType},
		"epilogue":                     {rwField("model", "friendly") + rwClose + "epilogue\r\n", rwType},
		"epilogue with a second form":  {rwField("model", "friendly") + rwClose + rwField("model", "expensive") + rwClose, rwType},
		"two CRLFs after the end":      {rwField("model", "friendly") + rwClose + "\r\n\r\n", rwType},
		"boundary* parameter":          {rwField("model", "friendly") + rwClose, "multipart/form-data; boundary=\"" + rwBoundary + "\"; boundary*=UTF-8''Y"},
		"boundary* alone":              {rwField("model", "friendly") + rwClose, "multipart/form-data; boundary*=UTF-8''" + rwBoundary},
		"extra content type parameter": {rwField("model", "friendly") + rwClose, rwType + "; charset=utf-8"},
		"two boundary parameters":      {rwField("model", "friendly") + rwClose, rwType + "; boundary=other"},
		"name* on the model part":      {"--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"model\"; name*=UTF-8''x\r\n\r\nfriendly\r\n" + rwClose, rwType},
		"name* on a later part":        {rwField("model", "friendly") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"model\"; name*=UTF-8''x\r\n\r\nexpensive\r\n" + rwClose, rwType},
		"name* on a file part":         {rwField("model", "friendly") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"; name*=UTF-8''model\r\n\r\nx\r\n" + rwClose, rwType},
		"name continuation":            {rwField("model", "friendly") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name*0=\"mo\"; name*1=\"del\"\r\n\r\nexpensive\r\n" + rwClose, rwType},
		"filename* alone":              {rwField("model", "friendly") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename*=UTF-8''a.wav\r\n\r\nx\r\n" + rwClose, rwType},
		"empty filename":               {rwField("model", "friendly") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"n\"; filename=\"\"\r\n\r\n10\r\n" + rwClose, rwType},
		"duplicate parameter":          {rwField("model", "friendly") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"a\"; name=\"model\"\r\n\r\nx\r\n" + rwClose, rwType},
		"unknown parameter":            {rwField("model", "friendly") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"a\"; size=3\r\n\r\nx\r\n" + rwClose, rwType},
		"backslash escape":             {rwField("model", "friendly") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"a\\\"model\"\r\n\r\nx\r\n" + rwClose, rwType},
		"folded header":                {rwField("model", "friendly") + "--" + rwBoundary + "\r\nContent-Disposition: form-data;\r\n name=\"a\"\r\n\r\nx\r\n" + rwClose, rwType},
		"transfer encoding on a file":  {rwField("model", "friendly") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a\"\r\nContent-Transfer-Encoding: base64\r\n\r\nx\r\n" + rwClose, rwType},
		"oversized header":             {"--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"a\"\r\nX-Pad: " + strings.Repeat("p", 20<<10) + "\r\n\r\nx\r\n" + rwField("model", "friendly") + rwClose, rwType},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			_, err := RewriteMultipartModel(&out, strings.NewReader(c.in), c.contentType, "native-asr")
			var refusal *MultipartError
			if !errors.As(err, &refusal) {
				t.Fatalf("rewrite: err %v, wrote %q", err, out.String())
			}
			if _, err = MultipartModel(strings.NewReader(c.in), c.contentType); !errors.As(err, &refusal) {
				t.Fatalf("scan: err %v", err)
			}
		})
	}
	if _, err := RewriteMultipartModel(io.Discard, strings.NewReader(rwField("model", "f")+rwClose), rwType, "bad\r\nname"); err == nil {
		t.Fatal("a native name with a line break was accepted")
	}
	if _, err := RewriteMultipartModel(io.Discard, strings.NewReader(rwField("model", "f")+rwClose), rwType, ""); err == nil {
		t.Fatal("an empty native name was accepted")
	}
}

// A source error is the caller's error, not a malformed-body refusal.
func TestRewriteMultipartModelReportsSourceAndDestinationErrors(t *testing.T) {
	boom := errors.New("boom")
	_, err := RewriteMultipartModel(io.Discard, io.MultiReader(strings.NewReader(rwField("a", "1")), &failingReader{boom}), rwType, "n")
	var refusal *MultipartError
	if !errors.Is(err, boom) || errors.As(err, &refusal) {
		t.Fatalf("source error: %v", err)
	}
	_, err = RewriteMultipartModel(&failingWriter{boom}, strings.NewReader(rwField("model", "f")+rwClose), rwType, "n")
	if !errors.Is(err, boom) {
		t.Fatalf("destination error: %v", err)
	}
}

type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

type failingWriter struct{ err error }

func (f *failingWriter) Write([]byte) (int, error) { return 0, f.err }

// generatedUpload is a multipart body that exists only as a stream: a file
// part of fileBytes pseudo-random bytes, with the model field before or after
// it. It is never held in memory, and it counts how much has been read.
type generatedUpload struct {
	parts     []io.Reader
	reader    io.Reader
	read      int64
	fileBytes int64
}

type patternReader struct {
	left  int64
	state uint64
}

func (p *patternReader) Read(b []byte) (int, error) {
	if p.left == 0 {
		return 0, io.EOF
	}
	if int64(len(b)) > p.left {
		b = b[:p.left]
	}
	for i := range b {
		p.state = p.state*6364136223846793005 + 1442695040888963407
		b[i] = byte(p.state >> 56)
	}
	p.left -= int64(len(b))
	return len(b), nil
}

func newGeneratedUpload(fileBytes int64, modelFirst bool, model string) *generatedUpload {
	head := "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"big.wav\"\r\nContent-Type: audio/wav\r\n\r\n"
	file := []io.Reader{strings.NewReader(head), &patternReader{left: fileBytes, state: 42}, strings.NewReader("\r\n")}
	var parts []io.Reader
	if modelFirst {
		parts = append(append(parts, strings.NewReader(rwField("model", model))), file...)
	} else {
		parts = append(append(parts, file...), strings.NewReader(rwField("model", model)))
	}
	parts = append(parts, strings.NewReader(rwClose))
	u := &generatedUpload{parts: parts, fileBytes: fileBytes}
	u.reader = io.MultiReader(parts...)
	return u
}

func (u *generatedUpload) Read(b []byte) (int, error) {
	n, err := u.reader.Read(b)
	u.read += int64(n)
	return n, err
}

// auditWriter hashes what it receives and fails the test if the rewriter ever
// reads further ahead of what it has written than a fixed window: a rewriter
// that held the upload (or read it to find the model) would fall behind.
type auditWriter struct {
	t       *testing.T
	src     *generatedUpload
	window  int64
	written int64
	sum     [sha256.Size]byte
	hash    interface {
		io.Writer
		Sum([]byte) []byte
	}
	maxLag int64
}

func (a *auditWriter) Write(p []byte) (int, error) {
	a.written += int64(len(p))
	a.hash.Write(p)
	if lag := a.src.read - a.written; lag > a.maxLag {
		a.maxLag = lag
	}
	return len(p), nil
}

func TestRewriteMultipartModelStreamsALargeUploadInFixedMemory(t *testing.T) {
	const fileBytes = 48 << 20
	for _, modelFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("model first=%v", modelFirst), func(t *testing.T) {
			// Expected digest: the same stream with the native name written by hand.
			want := sha256.New()
			expected := newGeneratedUpload(fileBytes, modelFirst, "native-asr")
			if _, err := io.Copy(want, expected); err != nil {
				t.Fatal(err)
			}
			src := newGeneratedUpload(fileBytes, modelFirst, "friendly")
			audit := &auditWriter{t: t, src: src, hash: sha256.New()}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			original, err := RewriteMultipartModel(audit, src, rwType, "native-asr")
			runtime.ReadMemStats(&after)
			if err != nil || original != "friendly" {
				t.Fatalf("rewrite: %q, %v", original, err)
			}
			if got := audit.hash.Sum(nil); !bytes.Equal(got, want.Sum(nil)) {
				t.Fatal("the rewritten stream differs from the stream with the native name")
			}
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4<<20 {
				t.Fatalf("allocated %d bytes streaming %d bytes: memory grows with the upload", allocated, int64(fileBytes))
			}
			if audit.maxLag > 2*multipartReadBuffer {
				t.Fatalf("the rewriter read %d bytes ahead of what it had written", audit.maxLag)
			}
			if audit.written != src.read+int64(len("native-asr")-len("friendly")) {
				t.Fatalf("read %d, wrote %d", src.read, audit.written)
			}
		})
	}
}

// A model field placed after a file part is found without holding the file
// back, and an unbounded upload that never names a model is refused only
// after it has been passed through, never held: the caller bounds the body.
func TestRewriteMultipartModelNeverRetainsAnUploadWithoutAModel(t *testing.T) {
	src := newGeneratedUpload(64<<20, true, "x")
	// Replace the model field with a field of another name.
	src = &generatedUpload{reader: io.MultiReader(strings.NewReader(rwField("other", "x")), io.MultiReader(src.parts[1:]...))}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := RewriteMultipartModel(io.Discard, src, rwType, "n")
	runtime.ReadMemStats(&after)
	var refusal *MultipartError
	if !errors.As(err, &refusal) {
		t.Fatalf("err %v", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4<<20 {
		t.Fatalf("allocated %d bytes", allocated)
	}
}
