package query

import (
	"fmt"
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
		{"json", `{  "port": 8080}`, true},
		{"json", `{"a": 1, "port" : 2}`, true},
		{"json", `{"report": 1}`, false},
		{"json", `{"x": "port"}`, false},
	} {
		got := len(changedKeyLines(tt.format, []string{tt.line}, "port", wirePaths{})) > 0
		if got != tt.want {
			t.Errorf("%s %q = %v, want %v", tt.format, tt.line, got, tt.want)
		}
	}
}

func TestConfigKeys(t *testing.T) {
	paths := func(keys []configKey) []string {
		var out []string
		for _, k := range keys {
			out = append(out, fmt.Sprintf("%d:%s", k.line, strings.Join(k.path, ".")))
		}
		return out
	}
	for _, tt := range []struct {
		format, content string
		want            []string
	}{
		{"toml", "name = 1\n[server]\nport = 2\n[servers.a]\nport = 3\n[[peers]]\nhost.port = 4\ns = \"\"\"\nport = 5\n\"\"\"\n",
			[]string{"1:name", "3:server.port", "5:servers.a.port", "7:peers.host.port", "8:peers.s"}},
		{"yaml", "server:\n  port: 1\n  tls:\n    port: 2\nclient:\n  - port: 3\n    host: h\ntext: |\n  port: 4\n---\nport: 5\n",
			[]string{"1:server", "2:server.port", "3:server.tls", "4:server.tls.port", "5:client", "6:client.port", "7:client.host", "8:text", "11:port"}},
		{"json", "{\n  \"server\": {\"port\": 1},\n  \"list\": [{\"port\": 2}, \"port\"],\n  \"port\": 3\n}",
			[]string{"2:server", "2:server.port", "3:list", "3:list.port", "4:port"}},
	} {
		got := paths(configKeys(tt.format, splitFileLines(tt.content)))
		if strings.Join(got, " ") != strings.Join(tt.want, " ") {
			t.Errorf("%s keys = %v, want %v", tt.format, got, tt.want)
		}
	}
}

// confGo declares a root Config holding a ServerConfig and a ClientConfig, both
// with a Port, and a type held only by an anonymous struct's field.
const confGo = "package lib\n\ntype Config struct {\n\tName   string       `toml:\"name\" yaml:\"name\"`\n\tServer ServerConfig `toml:\"server\" yaml:\"server\"`\n\tClient ClientConfig `toml:\"client\"`\n\tPeers  map[string]*ServerConfig `toml:\"peers\"`\n\tLegacy struct {\n\t\tOld OldConfig `toml:\"old\"`\n\t}\n}\n\ntype (\n\tServerConfig struct {\n\t\tPort int `toml:\"port\" yaml:\"port\"`\n\t}\n\tClientConfig struct {\n\t\tPort int `toml:\"port\"`\n\t}\n)\n\ntype OldConfig struct {\n\tPort int `toml:\"port\"`\n}\n\nfunc use(c ServerConfig) { var s ServerConfig; _ = s }\n"

// configRepo commits files to a repo the done check reads.
func configRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
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
	return root
}

func fieldSymbol(container, field string) impact.ChangedSymbol {
	return impact.ChangedSymbol{Name: field, File: "lib/conf.go", SymbolID: "scip-go gomod example.com/m v1 `example.com/m/lib`/" + container + "#" + field + "."}
}

// A renamed toml key of a nested struct: the example config that still writes it
// under the struct's table is open, the same key under another table isn't, one at
// the top only matched by name (the struct may be decoded on its own), one the diff
// already changed counts as updated, and a file of another format isn't looked at.
func TestCheckConfigKeys(t *testing.T) {
	root := configRepo(t, map[string]string{
		"lib/conf.go": confGo,
		// Another package's ServerConfig, and an unexported field: neither is a parent.
		"other/conf.go": "package other\n\ntype ServerConfig struct{}\n\ntype Wrapper struct {\n\tServer ServerConfig `toml:\"other\"`\n\tcfg    *lib.ServerConfig\n}\n",
		// An import of lib: Main is.
		"app/app.go":                "package app\n\nimport (\n\tl \"example.com/m/lib\"\n)\n\ntype App struct {\n\tMain l.ServerConfig `toml:\"main\"`\n}\n",
		"config/example.toml":       "name = \"x\"\n[server]\nport = 8080\nhost = \"x\"\n[client]\nport = 9\n[other]\nport = 3\n[main]\nport = 4\n",
		"config/peers.toml":         "[peers.a]\nport = 1\n[peers.a.tls]\nport = 2\n",
		"config/other.toml":         "port = 1\n",
		"config/local.example.toml": "[server]\nlisten_port = 1\n",
		"deploy/values.yaml":        "port: 9\n",
		"node_modules/x/a.toml":     "[server]\nport = 1\n",
	})
	// The diff rewrote local.example.toml's old `port = 1`.
	parsed := &impact.ParsedDiff{Files: []impact.ChangedFile{{
		OldPath: "config/local.example.toml", NewPath: "config/local.example.toml",
		Hunks: []impact.ChangedHunk{{OldStart: 2, NewStart: 2, OldText: []string{"port = 1"}, NewText: []string{"listen_port = 1"}, Removed: []int{2}, Added: []int{2}}},
	}}}
	c := newDoneChecker(root, AnalyzeChangeSetOptions{}, parsed)
	c.oldLines["config/local.example.toml"] = []string{"[server]", "port = 1"}

	open := c.checkConfigKeys(fieldSymbol("ServerConfig", "Port"), []wireKeyChange{{format: "toml", old: "port", now: "listen_port"}})
	if strings.Join(open, ",") != "config/example.toml,config/other.toml,config/peers.toml" {
		t.Fatalf("open files = %v, want example.toml, other.toml and peers.toml", open)
	}
	var got []string
	for _, s := range c.sites {
		got = append(got, fmt.Sprintf("%s:%d:%s", s.File, s.Line, s.Use))
	}
	if strings.Join(got, " ") != "config/example.toml:3: config/example.toml:10: config/other.toml:1:leaf-key config/peers.toml:2:" {
		t.Fatalf("sites = %v, want [server], [main] and [peers.a] port, and the top-level port by name", got)
	}
	if s := c.sites[0]; s.Via != `toml key "port" -> "listen_port"` || s.Change != "changed" {
		t.Errorf("site = %+v", s)
	}
	if c.updated != 1 {
		t.Errorf("updated = %d, want 1 (local.example.toml)", c.updated)
	}
	if c.filesRead != 0 {
		t.Errorf("config files spent %d of the use budget", c.filesRead)
	}
}

// A struct no field holds is a file's top: a key there is a site, the same key in a
// table only matched by name. A struct held by an anonymous struct's field has no
// known path: every line writing the key is matched by name alone.
func TestCheckConfigKeys_RootAndUnknownPaths(t *testing.T) {
	root := configRepo(t, map[string]string{
		"lib/conf.go":     confGo,
		"config/a.toml":   "name = \"x\"\n[server]\nname = \"y\"\nport = 1\n",
		"deploy/app.yaml": "name: x\nserver:\n  port: 1\n  name: y\n",
	})
	c := newDoneChecker(root, AnalyzeChangeSetOptions{}, &impact.ParsedDiff{})
	c.checkConfigKeys(fieldSymbol("Config", "Name"), []wireKeyChange{{format: "toml", old: "name", now: "title"}, {format: "yaml", old: "name", now: "title"}})
	c.checkConfigKeys(fieldSymbol("OldConfig", "Port"), []wireKeyChange{{format: "toml", old: "port"}})
	var got []string
	for _, s := range c.sites {
		got = append(got, fmt.Sprintf("%s:%s:%d:%s", s.Symbol, s.File, s.Line, s.Use))
	}
	want := "Name:config/a.toml:1: Name:config/a.toml:3:leaf-key Name:deploy/app.yaml:1: Name:deploy/app.yaml:4:leaf-key Port:config/a.toml:4:leaf-key"
	if strings.Join(got, " ") != want {
		t.Errorf("sites =\n%s\nwant\n%s", strings.Join(got, " "), want)
	}
	check := c.result("x", true, false, "")
	if len(check.OpenSites) != 2 || check.RarelyCount != 3 {
		t.Errorf("open %d, rarely %d: want the two top-level names open, the rest rarely", len(check.OpenSites), check.RarelyCount)
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

// Through AnalyzeChangeSet: ServerConfig.Port's key is open under [server]; where
// Config.Port writes the same key at the top it only matches by name.
func TestDoneCheck_NestedConfigKeyFollowsTheTable(t *testing.T) {
	engine, root := configFixture(t, map[string]string{"config/example.toml": "port = 1\n[server]\nport = 2\n"})
	editRepoFile(t, root, "lib/conf.go", "Port int `toml:\"port\"`", "Port int `toml:\"listen_port\"`")

	resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})

	check := resp.DoneCheck
	if len(check.OpenSites) != 1 || check.OpenSites[0].Line != 3 || check.RarelyCount != 1 {
		t.Fatalf("openSites = %+v, rarely %d: want [server] port only", check.OpenSites, check.RarelyCount)
	}
}
