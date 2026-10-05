package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bot.env")
	in := &Config{BotToken: "1:x", AdminID: "5", Domain: "b.example.com", NexraSecret: `s'e cr$t #1`, DBHost: "localhost", DBPort: "3306",
		DBName: "db", DBUser: "u", DBPass: `p"a$s\w '`, Listen: "127.0.0.1:18007", APIOwnerKey: "o", APIManagerKey: "m", DataDir: "/var/lib/nexrabot"}
	if err := in.Write(path); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, 0640)
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.DBPass != in.DBPass || out.NexraSecret != in.NexraSecret || out.Listen != in.Listen || out.APIOwnerKey != "o" {
		t.Fatalf("round trip lost data: %q %q", out.DBPass, out.NexraSecret)
	}
	// rewriting keeps the mode a service user depends on
	if err := out.Write(path); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0640 {
		t.Fatalf("mode changed to %v", st.Mode().Perm())
	}
}
