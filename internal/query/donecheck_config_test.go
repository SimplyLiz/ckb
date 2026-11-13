package query

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"

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

func TestGoTagOnlyChange(t *testing.T) {
	for _, tt := range []struct {
		before, after string
		want          bool
	}{
		{"Port int `toml:\"port\"`", "Port int `toml:\"listen_port\"`", true},
		{"Port int `toml:\"port\"` // the port", "Port   int `toml:\"listen_port\"`", true},
		{"Port int `toml:\"port\"`", "Port int", true},
		{"Port int", "Port int `json:\"port\"`", true},
		{"Port int `toml:\"port\"`", "Port int64 `toml:\"listen_port\"`", false},
		{"Port int `toml:\"port\"`", "ListenPort int `toml:\"port\"`", false},
		{"func F(a int)", "func F(a int64)", false},
		{"Port int", "Port int", false},
	} {
		if got := goTagOnlyChange(tt.before, tt.after); got != tt.want {
			t.Errorf("goTagOnlyChange(%q, %q) = %v, want %v", tt.before, tt.after, got, tt.want)
		}
	}
}

const (
	symConfig     = "scip-go gomod example.com/m v1 `example.com/m/lib`/Config#"
	symConfigPort = "scip-go gomod example.com/m v1 `example.com/m/lib`/Config#Port."
	symServer     = "scip-go gomod example.com/m v1 `example.com/m/lib`/ServerConfig#"
	symServerPort = "scip-go gomod example.com/m v1 `example.com/m/lib`/ServerConfig#Port."
	symUse        = "scip-go gomod example.com/m v1 `example.com/m/app`/Use()."
)

// configFixture is a repo with a nested config struct, a Go function using its
// fields, and config files writing them.
func configFixture(t *testing.T, files map[string]string) (*Engine, string) {
	t.Helper()
	field := func(symbol string, line int32, kind scippb.SymbolInformation_Kind) (*scippb.SymbolInformation, *scippb.Occurrence) {
		return &scippb.SymbolInformation{Symbol: symbol, Kind: kind},
			&scippb.Occurrence{Symbol: symbol, Range: []int32{line, 1, 5}, SymbolRoles: int32(scippb.SymbolRole_Definition), EnclosingRange: []int32{line, 0, line, 40}}
	}
	conf := &scippb.Document{RelativePath: "lib/conf.go", Language: "go"}
	for _, f := range []struct {
		symbol string
		line   int32
		kind   scippb.SymbolInformation_Kind
	}{
		{symConfig, 2, scippb.SymbolInformation_Struct},
		{symConfigPort, 3, scippb.SymbolInformation_Field},
		{symServer, 7, scippb.SymbolInformation_Struct},
		{symServerPort, 8, scippb.SymbolInformation_Field},
	} {
		info, occ := field(f.symbol, f.line, f.kind)
		conf.Symbols = append(conf.Symbols, info)
		conf.Occurrences = append(conf.Occurrences, occ)
	}
	conf.Occurrences[0].EnclosingRange = []int32{2, 0, 5, 1}
	conf.Occurrences[2].EnclosingRange = []int32{7, 0, 9, 1}
	use := fixtureDoc("app/use.go", []fixtureFunc{{symUse, 4, 8}},
		[]fixtureRef{{symConfigPort, 5}, {symServerPort, 6}})
	engine := changeSetEngine(t, conf, use)
	root := engine.repoRoot
	writeRepoFile(t, root, ".gitignore", ".ckb/\nindex.scip\n")
	for path, content := range doneCheckFiles {
		writeRepoFile(t, root, path, content)
	}
	writeRepoFile(t, root, "lib/conf.go", "package lib\n\ntype Config struct {\n\tPort   int          `toml:\"port\"`\n\tServer ServerConfig `toml:\"server\"`\n}\n\ntype ServerConfig struct {\n\tPort int `toml:\"port\"`\n}\n")
	writeRepoFile(t, root, "app/use.go", "package app\n\nimport \"example.com/m/lib\"\n\nfunc Use(c lib.Config) {\n\tc.Port = 1\n\tc.Server.Port = 2\n\t_ = c\n}\n")
	for path, content := range files {
		writeRepoFile(t, root, path, content)
	}
	gitInit(t, root)
	engine.scipAdapter.GetIndex().IndexedCommit = headCommit(t, root)
	return engine, root
}

// Only the tag of a field changed: `c.Port` compiles as before, so the Go use isn't
// open; the config file still writing the old key is.
func TestDoneCheck_TagOnlyChangeListsOnlyConfigSites(t *testing.T) {
	engine, root := configFixture(t, map[string]string{"config/example.toml": "port = 1\n"})
	editRepoFile(t, root, "lib/conf.go", "Port   int          `toml:\"port\"`", "Port   int          `toml:\"listen_port\"`")

	resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})

	check := resp.DoneCheck
	if check.Status != DoneCheckOpen {
		t.Fatalf("status = %s: %s", check.Status, check.Note)
	}
	if len(check.OpenSites) != 1 || check.OpenSites[0].File != "config/example.toml" {
		t.Fatalf("openSites = %+v, want only the config line", check.OpenSites)
	}
	if got := strings.Join(resp.ConsumerFiles.ContractChanged, ","); got != "config/example.toml" {
		t.Errorf("contractChanged = %q, want the config file only", got)
	}
}
