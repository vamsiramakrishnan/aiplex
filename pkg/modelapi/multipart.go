package modelapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"sort"
	"strings"
)

// Multipart uploads (audio files, image edits, batch input files) are relayed
// byte-for-byte. The request body is copied once to a private temporary file
// while a multipart reader walks it: form fields (bounded) are kept for
// validation, authorization and quota; file parts are read only to advance
// the parser and are never parsed, retained in memory or logged. Memory use is
// bounded by the field limits; disk use by the upload limit.
const (
	maxMultipartFields     = 64
	maxMultipartFieldBytes = 64 << 10
	maxMultipartFiles      = 16
	// DefaultMaxUploadBytes bounds one multipart upload when Options does not.
	DefaultMaxUploadBytes = 32 << 20
)

type spooledUpload struct {
	file        *os.File
	size        int64
	contentType string
	fields      map[string][]string
	files       int
}

func (u *spooledUpload) Close() {
	if u == nil || u.file == nil {
		return
	}
	name := u.file.Name()
	_ = u.file.Close()
	_ = os.Remove(name)
	u.file = nil
}

// reader returns a fresh reader over the spooled bytes.
func (u *spooledUpload) reader() (io.Reader, error) {
	if _, err := u.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.LimitReader(u.file, u.size), nil
}

// validateStrict runs ValidateMultipart over the spooled body and refuses a
// body whose model field differs between it and the standard-library pass
// that filled u.fields.
func (u *spooledUpload) validateStrict() error {
	reader, err := u.reader()
	if err != nil {
		return fmt.Errorf("upload spool unavailable")
	}
	model, found, err := ValidateMultipart(reader, u.contentType)
	if err != nil {
		var refusal *MultipartError
		if errors.As(err, &refusal) {
			return badRequest("invalid multipart body: %s", refusal.Message)
		}
		return badRequest("invalid multipart body")
	}
	values := u.fields["model"]
	if found && (len(values) != 1 || values[0] != model) || !found && len(values) != 0 {
		return badRequest("invalid multipart body: the model form field is ambiguous")
	}
	return nil
}

// field returns a single-valued form field; repeated values are refused.
func (u *spooledUpload) field(name string) (string, bool, error) {
	values := u.fields[name]
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, badRequest("form field %s must appear once", name)
	}
	return values[0], true, nil
}

// canonical returns the form fields as a canonical JSON object (sorted keys,
// single values as strings). It is the quota/idempotency view of the request:
// it identifies the form but never contains file bytes. A digest of each
// file part is not included; the attempt is keyed by the caller's
// Idempotency-Key as for every other exchange.
func (u *spooledUpload) canonical() []byte {
	object := map[string]any{}
	keys := make([]string, 0, len(u.fields))
	for key := range u.fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values := u.fields[key]
		if len(values) == 1 {
			object[key] = values[0]
		} else {
			object[key] = values
		}
	}
	object["_files"] = u.files
	raw, _ := json.Marshal(object)
	return raw
}

type countingWriter struct {
	w        io.Writer
	n        int64
	limit    int64
	exceeded bool
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) > c.limit {
		c.exceeded = true
		return 0, errUploadTooLarge
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

var errUploadTooLarge = errors.New("upload exceeds the configured bound")

// spoolMultipart validates and spools a multipart/form-data body.
func spoolMultipart(body io.Reader, contentType string, limit int64) (*spooledUpload, error) {
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil || media != "multipart/form-data" || params["boundary"] == "" {
		return nil, badRequest("a multipart/form-data body is required")
	}
	file, err := os.CreateTemp("", "aiplex-upload-*")
	if err != nil {
		return nil, fmt.Errorf("upload spool unavailable")
	}
	upload := &spooledUpload{file: file, contentType: contentType, fields: map[string][]string{}}
	counter := &countingWriter{w: file, limit: limit}
	reader := multipart.NewReader(io.TeeReader(body, counter), params["boundary"])
	fields := 0
	for {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			upload.Close()
			if counter.exceeded {
				return nil, &providerRequestError{status: 413, message: "upload exceeds the configured bound"}
			}
			return nil, badRequest("invalid multipart body")
		}
		name := part.FormName()
		if name == "" {
			upload.Close()
			return nil, badRequest("multipart part without a form name")
		}
		if part.FileName() != "" {
			upload.files++
			if upload.files > maxMultipartFiles {
				upload.Close()
				return nil, badRequest("too many file parts")
			}
			// Advance through the file content; the tee writes it to the spool.
			if _, err := io.Copy(io.Discard, part); err != nil {
				upload.Close()
				if counter.exceeded {
					return nil, &providerRequestError{status: 413, message: "upload exceeds the configured bound"}
				}
				return nil, badRequest("invalid multipart file part")
			}
			continue
		}
		fields++
		if fields > maxMultipartFields {
			upload.Close()
			return nil, badRequest("too many form fields")
		}
		value, err := io.ReadAll(io.LimitReader(part, maxMultipartFieldBytes+1))
		if err != nil || len(value) > maxMultipartFieldBytes {
			upload.Close()
			if counter.exceeded {
				return nil, &providerRequestError{status: 413, message: "upload exceeds the configured bound"}
			}
			return nil, badRequest("oversized or invalid form field")
		}
		upload.fields[name] = append(upload.fields[name], string(value))
	}
	// Drain any epilogue so the spool holds the complete original body.
	if _, err := io.Copy(io.Discard, io.TeeReader(body, counter)); err != nil {
		upload.Close()
		if counter.exceeded {
			return nil, &providerRequestError{status: 413, message: "upload exceeds the configured bound"}
		}
		return nil, badRequest("invalid multipart body")
	}
	upload.size = counter.n
	// The strict structural check, shared with the access point and the
	// rewriter: the spooled bytes are what the provider will parse, so the
	// model the fields above name must be the model the strict parser reads.
	if err := upload.validateStrict(); err != nil {
		upload.Close()
		return nil, err
	}
	for key := range upload.fields {
		for _, reserved := range []string{"model", "stream", "purpose", "n"} {
			if strings.EqualFold(key, reserved) && key != reserved {
				upload.Close()
				return nil, &providerRequestError{status: 403, message: "ambiguous security field spelling"}
			}
		}
	}
	return upload, nil
}
