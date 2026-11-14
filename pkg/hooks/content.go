package hooks

import (
	"errors"
	"fmt"
)

// Part kinds.
const (
	KindText          = "text"
	KindSystem        = "system"
	KindToolArguments = "tool_arguments"
	KindToolResult    = "tool_result"
)

// Part is one piece of text extracted from a native body. Locator is a stable
// JSON path into the body ("messages[0].content"); Index is the part's
// position in Content.Parts and is what a transform refers to.
type Part struct {
	Index   int    `json:"index"`
	Locator string `json:"locator"`
	Role    string `json:"role,omitempty"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	// spans are the JSON string literals the text came from. A part
	// assembled from stream fragments has one span per fragment, possibly in
	// different documents (doc indexes into Content.docs).
	spans []span
	// repeats are complete copies of the text elsewhere in a stream (a
	// "done" event, a completed response). A transform writes the whole
	// replacement into each, so no copy of the original survives.
	repeats []span
}

type span struct {
	doc        int
	start, end int
}

// Uninspected is a part AIPlex reports but cannot inspect: an image, audio,
// a file, an encrypted reasoning block, an unknown block type.
type Uninspected struct {
	Locator string `json:"locator"`
	Type    string `json:"type"`
}

// Content is the inspectable view of one native body (or one buffered
// stream). It is produced by Extract and written back by Apply.
type Content struct {
	Format      string
	Parts       []Part
	Uninspected []Uninspected
	docs        [][]byte
	// stream is set for a buffered SSE stream; Apply re-emits the events.
	stream *sseStream
	// scrubs are string literals that carry no text but could hold a copy of
	// it (a delta's padding). Whenever a transform changes the stream they
	// are emptied.
	scrubs []span
}

// ErrUninspectable reports a body AIPlex cannot inspect: a format with no
// extractor, or a body that does not parse as that format.
var ErrUninspectable = errors.New("content cannot be inspected")

func uninspectable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUninspectable, fmt.Sprintf(format, args...))
}

type collector struct {
	content *Content
	doc     int
	// join, when set, makes the text parts of one candidate a single part
	// (per join key): see textJoined.
	join  string
	joins map[string]int
}

// textJoined adds a text to the part of its join key, creating it on the
// first. The text parts of a Gemini candidate are one text to the SDKs'
// accessors; a value split around another part must be seen whole, and a
// transform writes the whole replacement into the first span and empties the
// rest.
func (c *collector) textJoined(key string, n *node, loc, role, kind string) {
	s, ok := n.stringValue()
	if !ok {
		return
	}
	if i, seen := c.joins[key]; seen {
		c.content.Parts[i].Text += s
		c.content.Parts[i].spans = append(c.content.Parts[i].spans, span{c.doc, n.start, n.end})
		return
	}
	if c.joins == nil {
		c.joins = map[string]int{}
	}
	c.joins[key] = len(c.content.Parts)
	c.text(n, loc, role, kind)
}

func (c *collector) text(n *node, loc, role, kind string) {
	s, ok := n.stringValue()
	if !ok {
		return
	}
	c.content.Parts = append(c.content.Parts, Part{Index: len(c.content.Parts), Locator: loc, Role: role, Kind: kind, Text: s, spans: []span{{c.doc, n.start, n.end}}})
}

func (c *collector) skip(loc, typ string) {
	c.content.Uninspected = append(c.content.Uninspected, Uninspected{Locator: loc, Type: typ})
}

// leaves extracts every string leaf of a JSON value (tool arguments given as
// an object). Object keys are not extracted.
func (c *collector) leaves(n *node, loc, role, kind string) {
	if n == nil {
		return
	}
	switch n.kind {
	case 's':
		c.text(n, loc, role, kind)
	case 'o':
		for i, k := range n.keys {
			c.leaves(n.vals[i], locator(loc, k), role, kind)
		}
	case 'a':
		for i, e := range n.elems {
			c.leaves(e, index(loc, i), role, kind)
		}
	}
}

// Texts returns the part texts in order.
func (c *Content) Texts() []string {
	out := make([]string, len(c.Parts))
	for i, p := range c.Parts {
		out[i] = p.Text
	}
	return out
}

// Apply writes replacement texts (by part index) back into the native body
// and returns the new bytes. Parts not in replacements, and every byte
// outside the replaced string literals, are unchanged; with no replacement
// that differs from the original the original bytes are returned as is.
func (c *Content) Apply(replacements map[int]string) []byte {
	if c.stream != nil {
		return c.stream.apply(c, replacements)
	}
	if len(c.docs) == 0 {
		return nil
	}
	var edits []edit
	for i, text := range replacements {
		if i < 0 || i >= len(c.Parts) || c.Parts[i].Text == text {
			continue
		}
		for j, s := range c.Parts[i].spans {
			value := ""
			if j == 0 {
				value = text
			}
			edits = append(edits, edit{s.start, s.end, value})
		}
		for _, s := range c.Parts[i].repeats {
			edits = append(edits, edit{s.start, s.end, text})
		}
	}
	if len(edits) == 0 {
		return c.docs[0]
	}
	return splice(c.docs[0], edits)
}
