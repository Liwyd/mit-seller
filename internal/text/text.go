// Package text holds the bot's fixed strings, extracted verbatim from the PHP
// bot's text.php so every reply reads exactly as before.
package text

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed text.json
var raw []byte

var tree map[string]any

func init() {
	if err := json.Unmarshal(raw, &tree); err != nil {
		panic("text.json: " + err.Error())
	}
}

// Get returns $textbotlang[a][b]... joined by dots, e.g. T("users.start").
// Missing keys return "" like a PHP null would when used as a string.
func T(path string) string {
	s, _ := Lookup(path)
	return s
}

// Lookup is T that also reports whether the key exists.
func Lookup(path string) (string, bool) {
	var cur any = tree
	parts := strings.Split(path, ".")
	for i := 0; i < len(parts); i++ {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		next, ok := m[parts[i]]
		// a few keys contain a dot themselves (e.g. "Charged.")
		for j := i + 1; !ok && j < len(parts); j++ {
			next, ok = m[strings.Join(parts[i:j+1], ".")]
			if ok {
				i = j
			}
		}
		if !ok {
			return "", false
		}
		cur = next
	}
	s, ok := cur.(string)
	return s, ok
}
