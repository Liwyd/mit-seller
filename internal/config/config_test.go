package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bot.env")
	in := &Config{BotToken: "1:x", AdminID: "5", Domain: "b.example.com", MitSecret: `s'e cr$t #1`, DBHost: "localhost", DBPort: "3306",
		DBName: "db", DBUser: "u", DBPass: `p"a$s\w '`, Listen: "127.0.0.1:18007", APIOwnerKey: "o", APIManagerKey: "m", DataDir: "/var/lib/mitseller"}
	if err := in.Write(path); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, 0640)
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.DBPass != in.DBPass || out.MitSecret != in.MitSecret || out.Listen != in.Listen || out.APIOwnerKey != "o" {
		t.Fatalf("round trip lost data: %q %q", out.DBPass, out.MitSecret)
	}
	// rewriting keeps the mode a service user depends on
	if err := out.Write(path); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0640 {
		t.Fatalf("mode changed to %v", st.Mode().Perm())
	}
}

// The config file and environment variables written before the rebrand must
// keep working, so an upgraded bot does not lose its secret or its settings.
func TestLoadReadsPreRebrandNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bot.env")
	body := "BOT_TOKEN=1:x\nADMIN_ID=5\nDOMAIN=b.example.com\nNEXRA_SECRET=from-old-file\nDB_NAME=db\nDB_USER=u\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.MitSecret != "from-old-file" {
		t.Fatalf("legacy NEXRA_SECRET not read: %q", c.MitSecret)
	}
	// current env name wins over the file
	t.Setenv("MITSELLER_MIT_SECRET", "from-new-env")
	if c, err = Load(path); err != nil {
		t.Fatal(err)
	}
	if c.MitSecret != "from-new-env" {
		t.Fatalf("MITSELLER_MIT_SECRET not read: %q", c.MitSecret)
	}
	// pre-rebrand env names still win over the file
	t.Setenv("MITSELLER_MIT_SECRET", "")
	os.Unsetenv("MITSELLER_MIT_SECRET")
	t.Setenv("NEXRABOT_NEXRA_SECRET", "from-old-env")
	if c, err = Load(path); err != nil {
		t.Fatal(err)
	}
	if c.MitSecret != "from-old-env" {
		t.Fatalf("legacy NEXRABOT_NEXRA_SECRET not read: %q", c.MitSecret)
	}
}
