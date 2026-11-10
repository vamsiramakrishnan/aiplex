package api

// clientRequestID accepts a caller-chosen per-request id (X-Client-Request-Id)
// as an opaque correlation key: short, printable and without separators that
// could be confused with other fields.
func clientRequestID(v string) string {
	if v == "" || len(v) > 128 {
		return ""
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':') {
			return ""
		}
	}
	return v
}
