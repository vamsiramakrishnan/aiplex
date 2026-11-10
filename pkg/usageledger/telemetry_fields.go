package usageledger

import (
	"regexp"
	"sort"
	"strings"
)

// Bounds on the labels a record may carry. Labels are for comparison and
// display; none of them is identity.
const (
	// MaxReportedAttributes bounds reported_attributes on one record.
	MaxReportedAttributes = 16
	// MaxTags bounds tags on one record.
	MaxTags = 8
	// MaxLabelValueLength bounds a reported attribute value.
	MaxLabelValueLength = 256
	// MaxTagValueLength bounds a tag value.
	MaxTagValueLength = 64
	// TagOverflowValue replaces a tag value once a key has too many distinct
	// values in a tenant.
	TagOverflowValue = "(other)"
	// TagGroupPrefix selects a tag in group_by ("tag:experiment").
	TagGroupPrefix = "tag:"
)

var (
	reportedAttributeKey = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	tagKeyPattern        = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,31}$`)
	tagValuePattern      = regexp.MustCompile(`^[A-Za-z0-9_.:/@+() -]{1,64}$`)
)

// ValidTagKey reports whether key is a usable tag key.
func ValidTagKey(key string) bool { return tagKeyPattern.MatchString(key) }

// ValidTagValue reports whether value is a usable tag value.
func ValidTagValue(value string) bool {
	return tagValuePattern.MatchString(value) && strings.TrimSpace(value) == value
}

// ValidReportedAttributeKey reports whether key may appear in
// reported_attributes.
func ValidReportedAttributeKey(key string) bool { return reportedAttributeKey.MatchString(key) }

func cloneLabels(m map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (r Record) validateTelemetryFields() error {
	for name, value := range map[string]string{"harness": r.Harness, "harness_version": r.HarnessVersion, "session_id": r.SessionID, "client_request_id": r.ClientRequestID, "caller_span_id": r.CallerSpanID} {
		if !validText(value, false) {
			return invalid("%s is not bounded printable text", name)
		}
	}
	if len(r.ReportedAttributes) > MaxReportedAttributes {
		return invalid("reported_attributes is bounded to %d entries", MaxReportedAttributes)
	}
	for k, v := range r.ReportedAttributes {
		if !reportedAttributeKey.MatchString(k) || len(v) > MaxLabelValueLength || !validText(v, false) {
			return invalid("reported_attributes entries need a lowercase key and bounded printable text")
		}
	}
	if len(r.Tags) > MaxTags {
		return invalid("tags is bounded to %d entries", MaxTags)
	}
	for k, v := range r.Tags {
		if !tagKeyPattern.MatchString(k) || !tagValuePattern.MatchString(v) {
			return invalid("tags entries need a key of [a-z][a-z0-9_.-]{0,31} and a value of up to %d characters of [A-Za-z0-9_.:/@+() -]", MaxTagValueLength)
		}
	}
	return nil
}

// SortedKeys returns a map's keys in order (stable output).
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
