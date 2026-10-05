package panels

// Pre-rebrand compatibility: data written by the nexra-branded bot keeps
// working after the rebrand. Panel rows are still typed "nexra" in databases
// that were set up before it, and their login token is still cached under the
// old key — both are read as their current equivalents instead of being
// rewritten, so nothing has to be migrated by hand.

// IsMitType reports whether a marzban_panel.type value belongs to the Mit
// (formerly Nexra) panel driver.
func IsMitType(t string) bool { return t == "mit" || t == "nexra" }

// adoptLoginCache lifts a token cached under the pre-rebrand key onto the
// current one, so an upgraded instance reuses the session it already has.
func adoptLoginCache(c map[string]any) {
	if c == nil {
		return
	}
	if _, ok := c["mit"]; ok {
		return
	}
	if e, ok := c["nexra"]; ok {
		c["mit"] = e
	}
}
