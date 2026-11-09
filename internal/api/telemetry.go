package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

var telemetryID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func telemetryJSON(r *http.Request, dst any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, (1<<20)+1))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid or oversized telemetry JSON")
	}
	return nil
}
func telemetryLimit(r *http.Request, fallback int) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 || n > 1000 {
		return 0, errors.New("limit must be between 1 and 1000")
	}
	return n, nil
}
func telemetryLabel(v string, required bool) bool {
	return (!required || strings.TrimSpace(v) != "") && len(v) <= 512 && !strings.ContainsAny(v, "\r\n\x00")
}
func traceHex(v string, n int) bool {
	if len(v) != 2*n || v != strings.ToLower(v) || v == strings.Repeat("0", 2*n) {
		return false
	}
	_, e := hex.DecodeString(v)
	return e == nil
}
func validateTelemetryTrace(trace, span, parent string) error {
	if trace != "" && !traceHex(trace, 16) || span != "" && !traceHex(span, 8) || parent != "" && !traceHex(parent, 8) || span != "" && span == parent {
		return errors.New("invalid W3C trace/span identifiers")
	}
	return nil
}
func reportedBy(r *http.Request) string {
	if identity := GetWIFIdentity(r.Context()); identity != nil {
		return identity.PrincipalID
	}
	return ""
}

func concreteCapabilities(scopes []string, prefix string) []string {
	out := make([]string, 0)
	for _, s := range scopes {
		if strings.HasPrefix(s, prefix) {
			n := strings.TrimPrefix(s, prefix)
			if n != "" && !strings.Contains(n, "*") && !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}
func deployedTasks(inst *models.Instance, tmpl *models.Template) []string {
	if inst.Scopes != nil {
		return concreteCapabilities(inst.Scopes, "a2a:task:")
	}
	return slices.Clone(tmpl.TaskTypes)
}
func deployedSkills(inst *models.Instance, tmpl *models.Template) []models.SkillInfo {
	if inst.Scopes == nil {
		return slices.Clone(tmpl.Skills)
	}
	out := make([]models.SkillInfo, 0)
	for _, name := range concreteCapabilities(inst.Scopes, "skill:invoke:") {
		skill := models.SkillInfo{Name: name}
		for _, declared := range tmpl.Skills {
			if declared.Name == name {
				skill = declared
				break
			}
		}
		out = append(out, skill)
	}
	return out
}
func validateDelegationTiming(d *models.Delegation, now time.Time) error {
	if d.DurationMs < 0 || d.DurationMs > int64((100*365*24*time.Hour)/time.Millisecond) || d.StartedAt.After(now.Add(5*time.Minute)) {
		return errors.New("invalid delegation timestamp or duration")
	}
	terminal := d.Status == "completed" || d.Status == "failed"
	if !terminal && d.CompletedAt != nil {
		return errors.New("active delegation cannot have a completion timestamp")
	}
	if terminal {
		if d.CompletedAt == nil {
			at := now
			if d.DurationMs > 0 {
				at = d.StartedAt.Add(time.Duration(d.DurationMs) * time.Millisecond)
			}
			d.CompletedAt = &at
		}
		if d.CompletedAt.Before(d.StartedAt) || d.CompletedAt.After(now.Add(5*time.Minute)) {
			return errors.New("completion must follow start and cannot be in the future")
		}
		actual := d.CompletedAt.Sub(d.StartedAt).Milliseconds()
		if d.DurationMs != 0 && d.DurationMs != actual {
			return errors.New("delegation duration and timestamps disagree")
		}
		d.DurationMs = actual
	}
	return nil
}
