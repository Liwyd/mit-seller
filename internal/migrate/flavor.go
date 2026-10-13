package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Flavor says which PHP product a bot directory came from. nexra and
// mirzapanel are one flavour on purpose: their config.php and their table.php
// are the same file, so a mirzapanel database needs nothing the Go bot's own
// schema upgrade does not already do. mirza_pro diverged, and compat.go puts
// back what it renamed.
type Flavor string

const (
	// FlavorLegacy is nexra and mirzapanel — interchangeable here.
	FlavorLegacy Flavor = ""
	// FlavorMirza is mirza_pro.
	FlavorMirza Flavor = "mirza_pro"
)

func (f Flavor) String() string {
	if f == "" {
		return "nexra/mirzapanel"
	}
	return string(f)
}

// DetectFlavor recognises mirza_pro by cronbot/: the dispatcher directory the
// mirza_pro admin panel tells the operator to curl every minute, which nexra
// and mirzapanel do not have. The name is unique to mirza_pro in all three
// sources, so this cannot mis-fire on a nexra or mirzapanel install.
func DetectFlavor(dir string) Flavor {
	if st, err := os.Stat(filepath.Join(dir, "cronbot")); err == nil && st.IsDir() {
		return FlavorMirza
	}
	return FlavorLegacy
}

// BotRoot is one PHP bot directory found on this server.
type BotRoot struct {
	Dir    string
	Flavor Flavor
}

// DefaultScanRoots are the directories the PHP installers put bot folders in.
// Only the top level is scanned: a bot directory holds config.php directly.
var DefaultScanRoots = []string{"/var/www/html"}

// DetectBotRoots returns every PHP bot directory. Explicit directories (from
// PHP_DIR, --php-dir or MIGRATE_DIRS) come first, in the order given, and then
// one level under each scan root, sorted, so the numbers migrate-all hands out
// are the same on every run. A directory counts as a bot only when its own
// config.php exists, which picks up mirzapanel's "additional bot" folders with
// whatever the operator named them.
func DetectBotRoots(dirs, scanRoots []string) ([]BotRoot, error) {
	seen := map[string]bool{}
	var out []BotRoot
	add := func(dir string) {
		dir = strings.TrimRight(dir, "/")
		if dir == "" {
			return
		}
		if _, err := os.Stat(filepath.Join(dir, "config.php")); err != nil {
			return
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			real = dir
		}
		if seen[real] {
			return
		}
		seen[real] = true
		out = append(out, BotRoot{Dir: real, Flavor: DetectFlavor(real)})
	}
	for _, d := range dirs {
		d = strings.TrimRight(d, "/")
		if _, err := os.Stat(filepath.Join(d, "config.php")); err != nil {
			return nil, fmt.Errorf("%s has no config.php", d)
		}
		add(d)
	}
	for _, root := range scanRoots {
		ents, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		var names []string
		for _, e := range ents {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			add(filepath.Join(root, n))
		}
	}
	return out, nil
}

// SplitDirs splits a MIGRATE_DIRS-style list on the platform separator.
func SplitDirs(list string) []string {
	var out []string
	for _, p := range strings.Split(list, string(os.PathListSeparator)) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
