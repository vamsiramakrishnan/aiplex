package hooks

import (
	"encoding/json"
	"fmt"
)

// slotSet assembles the text of a buffered stream whose protocol repeats
// content: OpenAI Responses sends a delta and then the whole text again in
// several later events, and a Gemini chunk list carries one text in many
// pieces.
//
// Every string literal that carries model output is registered under a slot
// (one logical piece of output). Deltas of a slot concatenate. A complete
// copy of a slot's text (a "done" event, a completed response, a content
// part) whose text equals the assembled deltas, or an earlier complete copy,
// joins that group as a repeat and is inspected once; a complete copy that
// differs from everything before it is its own part and is inspected on its
// own. A transform therefore rewrites every copy, and no copy escapes the
// hooks whatever the event keys say.
type slotSet struct {
	content *Content
	byKey   map[string]*slot
	order   []*slot
}

type slot struct {
	loc, role, kind string
	frags           []slotFragment
	fulls           []slotFull
}

type slotFragment struct {
	raw []byte // the literal's content between the quotes, still escaped
	sp  span
}

type slotFull struct {
	text string
	sp   span
}

func newSlotSet(c *Content) *slotSet {
	return &slotSet{content: c, byKey: map[string]*slot{}}
}

func (s *slotSet) slot(key, loc, role, kind string) *slot {
	sl, ok := s.byKey[key]
	if !ok {
		sl = &slot{loc: loc, role: role, kind: kind}
		s.byKey[key] = sl
		s.order = append(s.order, sl)
	}
	return sl
}

// textNode accepts an absent or null value as "no text" and refuses any
// other value that is not a string.
func textNode(n *node, loc string) (bool, error) {
	if n == nil || n.kind == 'z' {
		return false, nil
	}
	if n.kind != 's' {
		return false, errorf("%s must be a string", loc)
	}
	return true, nil
}

// fragment adds one delta to a slot. data is the document n was parsed from.
func (s *slotSet) fragment(key, loc, role, kind string, doc int, data []byte, n *node) error {
	ok, err := textNode(n, loc)
	if err != nil || !ok {
		return err
	}
	sl := s.slot(key, loc, role, kind)
	sl.frags = append(sl.frags, slotFragment{raw: data[n.start+1 : n.end-1], sp: span{doc, n.start, n.end}})
	return nil
}

// full adds a complete copy of a slot's text.
func (s *slotSet) full(key, loc, role, kind string, doc int, n *node) error {
	ok, err := textNode(n, loc)
	if err != nil || !ok {
		return err
	}
	sl := s.slot(key, loc, role, kind)
	sl.fulls = append(sl.fulls, slotFull{text: n.str, sp: span{doc, n.start, n.end}})
	return nil
}

type slotGroup struct {
	text           string
	spans, repeats []span
}

// finish turns the slots into the content's parts.
func (s *slotSet) finish() error {
	for _, sl := range s.order {
		var groups []*slotGroup
		if len(sl.frags) > 0 {
			// Concatenate the escaped literals before decoding: a character
			// written as a surrogate pair of escapes may be split across
			// deltas, and decoding each delta alone would corrupt it.
			raw := []byte{'"'}
			g := &slotGroup{}
			for _, f := range sl.frags {
				raw = append(raw, f.raw...)
				g.spans = append(g.spans, f.sp)
			}
			raw = append(raw, '"')
			if err := json.Unmarshal(raw, &g.text); err != nil {
				return uninspectable("stream text at %s cannot be assembled", sl.loc)
			}
			groups = append(groups, g)
		}
		byText := map[string]*slotGroup{}
		for _, g := range groups {
			byText[g.text] = g
		}
		for _, f := range sl.fulls {
			target := byText[f.text]
			if target == nil {
				target = &slotGroup{text: f.text, spans: []span{f.sp}}
				byText[f.text] = target
				groups = append(groups, target)
				continue
			}
			target.repeats = append(target.repeats, f.sp)
		}
		for _, g := range groups {
			if g.text == "" {
				// Nothing to inspect and nothing a transform could hide.
				continue
			}
			i := len(s.content.Parts)
			s.content.Parts = append(s.content.Parts, Part{Index: i, Locator: sl.loc, Role: sl.role, Kind: sl.kind, Text: g.text, spans: g.spans, repeats: g.repeats})
		}
	}
	return nil
}

// scrub registers a string that carries no text (a delta's padding) to be
// emptied whenever the stream is rewritten.
func (s *slotSet) scrub(n *node, doc int, what string) error {
	if n == nil || n.kind == 'z' {
		return nil
	}
	if n.kind != 's' {
		return errorf("%s must be a string", what)
	}
	s.content.scrubs = append(s.content.scrubs, span{doc, n.start, n.end})
	return nil
}

// onlyKeys refuses an object that has a member outside the allowed list. A
// member under another letter case is outside the list too.
func onlyKeys(n *node, what string, allowed ...string) error {
	if n == nil || n.kind != 'o' {
		return errorf("%s must be an object", what)
	}
	for _, k := range n.keys {
		found := false
		for _, a := range allowed {
			if k == a {
				found = true
				break
			}
		}
		if !found {
			return errorf("%s has the field %q, which cannot be inspected", what, k)
		}
	}
	return nil
}

// emptyList accepts an absent, null or empty array. Annotations and log
// probabilities repeat or index the text; anything in them is refused.
func emptyList(n *node, loc string) error {
	if n == nil || n.kind == 'z' || (n.kind == 'a' && len(n.elems) == 0) {
		return nil
	}
	return errorf("%s is not empty and cannot be kept consistent with a transformed text", loc)
}

func indexOf(data []byte, n *node, loc string) (int, error) {
	v, ok := intValue(data, n)
	if !ok || v < 0 {
		return 0, errorf("%s is missing or not a non-negative integer", loc)
	}
	return v, nil
}

func slotKey(parts ...any) string { return fmt.Sprint(parts...) }
