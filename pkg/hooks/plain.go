package hooks

import "encoding/json"

// PlainText builds content from bare texts, for a dry run of a policy. Apply
// returns a JSON array of the resulting texts; see PlainTexts.
func PlainText(texts []string) *Content {
	doc, _ := json.Marshal(texts)
	root, err := parseJSON(doc)
	content := &Content{Format: "text", docs: [][]byte{doc}}
	if err != nil {
		return content
	}
	c := &collector{content: content}
	for i, e := range root.elems {
		c.text(e, index("texts", i), "user", KindText)
	}
	return content
}

// PlainTexts decodes the body Apply produced for PlainText content.
func PlainTexts(body []byte) ([]string, error) {
	var out []string
	err := json.Unmarshal(body, &out)
	return out, err
}
