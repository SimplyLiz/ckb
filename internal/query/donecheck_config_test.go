package query

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

func TestGoWireKeyChanges(t *testing.T) {
	tests := []struct {
		name, before, after string
		removed             bool
		want                []wireKeyChange
	}{
		{"renamed", "Port int `toml:\"port\"`", "Port int `toml:\"listen_port\"`", false, []wireKeyChange{{"toml", "port", "listen_port"}}},
		{"removed", "Port int `toml:\"port\" json:\"port\"`", "", true, []wireKeyChange{{"json", "port", ""}, {"toml", "port", ""}}},
		{"tag dropped is not listed", "Port int `toml:\"port\"`", "Port int", false, nil},
		{"options kept", "Port int `json:\"port,omitempty\"`", "Port int `json:\"port\"`", false, nil},
		{"type only", "Port int `yaml:\"port\"`", "Port int64 `yaml:\"port\"`", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := goWireKeyChanges(tt.before, tt.after, tt.removed)
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %+v, want %+v", got[i], tt.want[i])
				}
			}
		})
	}
}

func TestConfigKeyLine(t *testing.T) {
	for _, tt := range []struct {
		format, line string
		want         bool
	}{
		{"toml", "port = 8080", true},
		{"toml", `  "port"=8080`, true},
		{"toml", "port_range = 1", false},
		{"toml", "# port = 8080", false},
		{"toml", `url = "port = 1"`, false},
		{"yaml", "  port: 8080", true},
		{"yaml", "- port: 1", true},
		{"yaml", "port:", true},
		{"yaml", "ports: [1]", false},
		{"yaml", "url: http://port:1", false},
		{"json", `  "port": 8080,`, true},
		{"json", `{"a": 1, "port" : 2}`, true},
		{"json", `"report": 1`, false},
		{"json", `"x": "port"`, false},
	} {
		if got := configKeyLine(tt.format, tt.line, "port"); got != tt.want {
			t.Errorf("%s %q = %v, want %v", tt.format, tt.line, got, tt.want)
		}
	}
}

// A renamed toml key: the example config that still writes it is open, one the diff
// already changed counts as updated, and a file of another format isn't looked at.
func TestCheckConfigKeys(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"config/example.toml":       "[server]\nport = 8080\nhost = \"x\"\n",
		"config/local.example.toml": "listen_port = 1\n",
		"deploy/values.yaml":        "port: 9\n",
		"node_modules/x/a.toml":     "port = 1\n",
	}
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A", "-f"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	// The diff rewrote local.example.toml's old `port = 1`.
	parsed := &impact.ParsedDiff{Files: []impact.ChangedFile{{
		OldPath: "config/local.example.toml", NewPath: "config/local.example.toml",
		Hunks: []impact.ChangedHunk{{OldStart: 1, NewStart: 1, OldText: []string{"port = 1"}, NewText: []string{"listen_port = 1"}, Removed: []int{1}, Added: []int{1}}},
	}}}
	c := newDoneChecker(root, AnalyzeChangeSetOptions{}, parsed)
	c.oldLines["config/local.example.toml"] = []string{"port = 1"}

	open := c.checkConfigKeys("Config.Port", []wireKeyChange{{format: "toml", old: "port", now: "listen_port"}})
	if len(open) != 1 || open[0] != "config/example.toml" {
		t.Fatalf("open files = %v, want config/example.toml", open)
	}
	if len(c.sites) != 1 || c.sites[0].Line != 2 || c.sites[0].Via != `toml key "port" -> "listen_port"` || c.sites[0].Change != "changed" {
		t.Fatalf("sites = %+v", c.sites)
	}
	if c.updated != 1 {
		t.Errorf("updated = %d, want 1 (local.example.toml)", c.updated)
	}
	if c.filesRead != 0 {
		t.Errorf("config files spent %d of the use budget", c.filesRead)
	}
}
