package modelapi

import (
	"fmt"
	"io"
)

// Rewriting the model of a multipart upload.
//
// An access point can show a client its own name for a model. On a multipart
// request (audio transcription and translation, image edits) the model is the
// "model" form field, and only that field may change: the file parts, their
// names and content types, the other fields and their order are copied
// through byte for byte, so the upload is not re-encoded and its boundary is
// unchanged.
//
// The rewriter is a single forward pass over the stream. It never seeks and
// never holds a part: memory is the read buffer (64 KiB), the header block of
// the current part (at most 16 KiB) and the model value (at most 1 KiB), however
// large the upload is and wherever the model field sits in it. A model field
// that follows a file part is handled the same way as one that precedes it;
// the bytes ahead of it are already on their way to the destination.
//
// The parser is deliberately stricter than mime/multipart, because the bytes
// it writes are parsed again by the upstream: anything it would have to guess
// about is refused, so the rewriter and the upstream cannot disagree about
// which field is the model.
const (
	// maxMultipartModelBytes bounds the model value: a longer one names no
	// model the access point can have.
	maxMultipartModelBytes = 1024
	// maxMultipartPartHeaderBytes bounds the header block of one part.
	maxMultipartPartHeaderBytes = 16 << 10
	multipartReadBuffer         = 64 << 10
	maxMultipartBoundary        = 70
)

// MultipartError is a refusal of a multipart body the rewriter will not
// guess about. It is a client error: the request is malformed or ambiguous.
type MultipartError struct{ Message string }

func (e *MultipartError) Error() string { return e.Message }

func multipartRefusal(format string, args ...any) error {
	return &MultipartError{Message: fmt.Sprintf(format, args...)}
}

// MultipartModel returns the value of the single "model" form field of a
// multipart/form-data body, reading the body once, with the same bounds and
// the same strictness as RewriteMultipartModel. It writes nothing.
func MultipartModel(src io.Reader, contentType string) (string, error) {
	model, _, err := walkMultipart(io.Discard, src, contentType, "", false, true)
	return model, err
}

// ValidateMultipart applies the same structural rules as MultipartModel to a
// body that need not name a model (a batch input file upload), and returns
// the model field if there is exactly one. It is the check the gateway runs
// on every multipart upload it spools, so the access point, the gateway and
// the rewriter share one parser.
func ValidateMultipart(src io.Reader, contentType string) (model string, found bool, err error) {
	return walkMultipart(io.Discard, src, contentType, "", false, false)
}

// RewriteMultipartModel copies a multipart/form-data body from src to dst,
// replacing the value of its single "model" form field with native, and
// returns the value it replaced. Everything else is copied unchanged. The
// body must have exactly one "model" part: plain text, no filename, no
// transfer encoding, and a closing delimiter; otherwise it returns a
// *MultipartError. A failure can arrive after earlier bytes were written, so
// a caller that cannot discard dst must run MultipartModel first.
func RewriteMultipartModel(dst io.Writer, src io.Reader, contentType, native string) (string, error) {
	if native == "" || !plainFieldValue(native) {
		return "", multipartRefusal("the native model name is not a plain form value")
	}
	model, _, err := walkMultipart(dst, src, contentType, native, true, true)
	return model, err
}
