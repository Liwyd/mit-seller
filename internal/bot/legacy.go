package bot

import "strings"

// Pre-rebrand compatibility: conversation steps and inline-button callback
// data used to be spelled with "nexra". Both are still accepted, so an admin
// who was mid-flow when the bot was upgraded, or who taps a button on a
// message sent before it, gets the same answer as before instead of silence.

// preRebrand maps a stored value onto its current spelling.
func preRebrand(s string) string { return strings.ReplaceAll(s, "nexra", "mit") }

// normStep is preRebrand for a conversation step read back from the database.
func normStep(s string) string { return preRebrand(s) }
