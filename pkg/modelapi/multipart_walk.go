package modelapi

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"mime"
	"strings"
	"unicode/utf8"
)

// The strict multipart/form-data subset.
//
// Providers parse the uploads this gateway forwards with their own libraries
// (Go, Python's python-multipart, Node and others), and those libraries
// disagree about the corners of RFC 2046, RFC 2231 and RFC 7578. The gateway
// decides admission, grants and metering from the model it reads, so it only
// accepts bodies that every common parser reads the same way:
//
//   - the request Content-Type is multipart/form-data with exactly one plain
//     boundary parameter and no other parameter (no boundary*, no
//     continuations);
//   - the body begins with the opening delimiter (no preamble) and ends with
//     the closing delimiter, optionally followed by one CRLF (no epilogue);
//   - every line ends in CRLF, and delimiters have no padding;
//   - every part has exactly one Content-Disposition of form-data with a plain
//     name, and for a file a plain filename. name*, filename* without a plain
//     filename beside it, continuations, duplicate or unknown parameters,
//     backslash escapes and a Content-Transfer-Encoding are refused;
//   - there is at most one model field, as plain text.
//
// Everything is checked on every part, not only on the model part, because
// the attack is a part that another parser reads as the model.

func plainFieldValue(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

type multipartState int

const (
	statePreamble multipartState = iota // before the opening delimiter
	stateHeaders
	stateBody
	stateModel
	stateEpilogue
)

func walkMultipart(dst io.Writer, src io.Reader, contentType, native string, rewrite, requireModel bool) (string, bool, error) {
	boundary, err := strictBoundary(contentType)
	if err != nil {
		return "", false, err
	}
	open := []byte("--" + boundary + "\r\n")
	closing := []byte("--" + boundary + "--\r\n")
	closingBare := []byte("--" + boundary + "--") // at the very end, no line break

	br := bufio.NewReaderSize(src, multipartReadBuffer)
	state := statePreamble
	atLineStart := true
	var header, value []byte
	var original string
	found := false
	fail := func(e error) (string, bool, error) { return "", false, e }

	for {
		line, readErr := br.ReadSlice('\n')
		complete := readErr == nil
		if readErr != nil && !errors.Is(readErr, bufio.ErrBufferFull) {
			if errors.Is(readErr, io.EOF) && atLineStart && bytes.Equal(line, closingBare) && state != statePreamble && state != stateHeaders {
				// A closing delimiter with no final line break.
				complete = true
			} else if errors.Is(readErr, io.EOF) {
				return fail(multipartRefusal("the multipart body is truncated"))
			} else {
				return fail(readErr)
			}
		}
		delimiter := atLineStart && complete && (bytes.Equal(line, open) || bytes.Equal(line, closing) || bytes.Equal(line, closingBare))
		isClosing := delimiter && !bytes.Equal(line, open)
		atLineStart = complete && readErr == nil

		switch state {
		case statePreamble:
			if !delimiter || isClosing {
				return fail(multipartRefusal("the multipart body must begin with the opening delimiter"))
			}
			if _, err := dst.Write(line); err != nil {
				return fail(err)
			}
			state, header = stateHeaders, header[:0]
		case stateHeaders:
			if !complete || !bytes.HasSuffix(line, []byte("\r\n")) {
				return fail(multipartRefusal("a part header is not CRLF-terminated or too long"))
			}
			if _, err := dst.Write(line); err != nil {
				return fail(err)
			}
			if len(line) == 2 {
				isModel, err := classifyPart(header)
				if err != nil {
					return fail(err)
				}
				if isModel {
					if found {
						return fail(multipartRefusal("the model form field must appear once"))
					}
					found, value, state = true, value[:0], stateModel
				} else {
					state = stateBody
				}
				break
			}
			if len(header)+len(line) > maxMultipartPartHeaderBytes {
				return fail(multipartRefusal("a part header block is too large"))
			}
			header = append(header, line...)
		case stateBody:
			if _, err := dst.Write(line); err != nil {
				return fail(err)
			}
			if delimiter {
				if isClosing {
					state = stateEpilogue
				} else {
					state, header = stateHeaders, header[:0]
				}
			}
		case stateModel:
			if !delimiter {
				if len(value)+len(line) > maxMultipartModelBytes+2 {
					return fail(multipartRefusal("the model form field is too long"))
				}
				value = append(value, line...)
				break
			}
			if !bytes.HasSuffix(value, []byte("\r\n")) {
				return fail(multipartRefusal("the model form field is empty"))
			}
			original = string(value[:len(value)-2])
			if original == "" || !plainFieldValue(original) {
				return fail(multipartRefusal("the model form field must be one line of plain text"))
			}
			out := original
			if rewrite {
				out = native
			}
			if _, err := io.WriteString(dst, out+"\r\n"); err != nil {
				return fail(err)
			}
			if _, err := dst.Write(line); err != nil {
				return fail(err)
			}
			if isClosing {
				state = stateEpilogue
			} else {
				state, header = stateHeaders, header[:0]
			}
		}
		if state == stateEpilogue {
			// Nothing may follow the closing delimiter but one CRLF.
			rest, err := io.ReadAll(io.LimitReader(br, 3))
			if err != nil {
				return fail(err)
			}
			if len(rest) != 0 && string(rest) != "\r\n" {
				return fail(multipartRefusal("nothing may follow the closing delimiter"))
			}
			if _, err := dst.Write(rest); err != nil {
				return fail(err)
			}
			if !found && requireModel {
				return fail(multipartRefusal("the request must name a model"))
			}
			return original, found, nil
		}
	}
}

func isTokenChar(c byte) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTokenChar(s[i]) {
			return false
		}
	}
	return true
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 && s[i] != '\t' || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// strictBoundary accepts "multipart/form-data; boundary=B" and nothing else:
// B is a token or a quoted string of boundary characters, 1 to 70 long.
func strictBoundary(contentType string) (string, error) {
	refuse := multipartRefusal("the Content-Type must be multipart/form-data with exactly one plain boundary parameter")
	const media = "multipart/form-data"
	if len(contentType) < len(media) || !strings.EqualFold(contentType[:len(media)], media) {
		return "", refuse
	}
	rest := strings.TrimLeft(contentType[len(media):], " \t")
	if !strings.HasPrefix(rest, ";") {
		return "", refuse
	}
	rest = strings.TrimLeft(rest[1:], " \t")
	const key = "boundary="
	if len(rest) < len(key) || !strings.EqualFold(rest[:len(key)], key) {
		return "", refuse
	}
	rest = strings.TrimRight(rest[len(key):], " \t")
	boundary, quoted := rest, false
	if strings.HasPrefix(rest, `"`) {
		if len(rest) < 2 || !strings.HasSuffix(rest, `"`) {
			return "", refuse
		}
		boundary, quoted = rest[1:len(rest)-1], true
	}
	if boundary == "" || len(boundary) > maxMultipartBoundary || strings.HasSuffix(boundary, " ") {
		return "", refuse
	}
	for i := 0; i < len(boundary); i++ {
		c := boundary[i]
		bchar := isTokenChar(c) && c != '!' && c != '#' && c != '$' && c != '%' && c != '&' && c != '*' && c != '^' && c != '`' && c != '|' && c != '~' || strings.IndexByte("()+,/:=? ", c) >= 0
		if !bchar || (!quoted && !isTokenChar(c)) {
			return "", refuse
		}
	}
	// The standard library must read the same single boundary.
	if _, params, err := mime.ParseMediaType(contentType); err != nil || len(params) != 1 || params["boundary"] != boundary {
		return "", refuse
	}
	return boundary, nil
}

// parseHeaderBlock splits a part's header block (CRLF-terminated lines) into
// lower-cased names and values. Folded lines and malformed lines are refused.
func parseHeaderBlock(block []byte) (map[string][]string, error) {
	headers := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(block), "\r\n"), "\r\n") {
		colon := strings.IndexByte(line, ':')
		if line == "" || line[0] == ' ' || line[0] == '\t' || colon < 1 || !isToken(line[:colon]) {
			return nil, multipartRefusal("invalid part header")
		}
		value := strings.Trim(line[colon+1:], " \t")
		if hasControl(value) || !utf8.ValidString(value) {
			return nil, multipartRefusal("invalid part header")
		}
		name := strings.ToLower(line[:colon])
		headers[name] = append(headers[name], value)
	}
	return headers, nil
}

// parseDisposition reads a Content-Disposition value: form-data followed by
// name, filename and (only beside a plain filename) filename*, each given
// once. Quoted strings take no backslash escapes.
func parseDisposition(v string) (name, filename string, hasFilename bool, err error) {
	refuse := func() (string, string, bool, error) {
		return "", "", false, multipartRefusal("every part needs a Content-Disposition of form-data with a plain name and, for a file, a plain filename")
	}
	if len(v) < 9 || !strings.EqualFold(v[:9], "form-data") {
		return refuse()
	}
	rest := v[9:]
	seen := map[string]string{}
	for strings.TrimLeft(rest, " \t") != "" {
		rest = strings.TrimLeft(rest, " \t")
		if rest[0] != ';' {
			return refuse()
		}
		rest = strings.TrimLeft(rest[1:], " \t")
		eq := strings.IndexByte(rest, '=')
		if eq < 1 || !isToken(rest[:eq]) {
			return refuse()
		}
		key := strings.ToLower(rest[:eq])
		rest = rest[eq+1:]
		var val string
		if strings.HasPrefix(rest, `"`) {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return refuse()
			}
			val, rest = rest[1:1+end], rest[end+2:]
			if strings.ContainsRune(val, '\\') || hasControl(val) {
				return refuse()
			}
		} else {
			end := 0
			for end < len(rest) && isTokenChar(rest[end]) {
				end++
			}
			val, rest = rest[:end], rest[end:]
			if val == "" {
				return refuse()
			}
		}
		if _, dup := seen[key]; dup || (key != "name" && key != "filename" && key != "filename*") {
			return refuse()
		}
		seen[key] = val
	}
	name = seen["name"]
	if name == "" {
		return refuse()
	}
	filename, hasFilename = seen["filename"]
	_, star := seen["filename*"]
	if (hasFilename || star) && filename == "" {
		// An empty plain filename is a file to one parser and a field to
		// another; filename* never stands alone.
		return refuse()
	}
	return name, filename, hasFilename || seen["filename*"] != "", nil
}

// classifyPart parses one part's header block and reports whether it is the
// model field. A part is refused when it cannot be classified with certainty
// or when it is a model field the rewriter cannot treat as plain text.
func classifyPart(block []byte) (bool, error) {
	headers, err := parseHeaderBlock(block)
	if err != nil {
		return false, err
	}
	if len(headers["content-disposition"]) != 1 {
		return false, multipartRefusal("every part needs exactly one Content-Disposition")
	}
	if _, ok := headers["content-transfer-encoding"]; ok {
		return false, multipartRefusal("a part must not use a transfer encoding")
	}
	name, _, isFile, err := parseDisposition(headers["content-disposition"][0])
	if err != nil {
		return false, err
	}
	if name != "model" {
		return false, nil
	}
	if isFile {
		return false, multipartRefusal("the model form field must not be a file")
	}
	if types := headers["content-type"]; len(types) > 0 {
		media, params, err := mime.ParseMediaType(types[0])
		charset := strings.ToLower(params["charset"])
		if len(types) != 1 || err != nil || media != "text/plain" || (charset != "" && charset != "utf-8") {
			return false, multipartRefusal("the model form field must be plain text")
		}
	}
	return true, nil
}
