package unwired

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestEntryStrings(t *testing.T) {
	cases := map[string][]string{
		// esbuild from a build script, Lebenslauf's shape
		`await build({ ...opts, entryPoints: [join(root, "src/render/main.ts")], outfile: join(out, "main.mjs") });`: {"src/render/main.ts"},
		`build({ entryPoints: ['a.ts', "b.tsx"], bundle: true })`:                                                    {"a.ts", "b.tsx"},
		`entryPoints: { worker: "./src/worker.ts" },`:                                                                {"./src/worker.ts"},
		"export default defineConfig({ entry: [`src/index.ts`] })":                                                   {"src/index.ts"},
		`build: { lib: { entry: "src/lib.ts" }, rollupOptions: { input: { main: "index.html", w: "src/w.mjs" } } }`:  {"src/lib.ts", "src/w.mjs"},
		`const entryPoints = ["src/cli.ts"];
await build({ entryPoints, outdir: "dist" });`: {"src/cli.ts"},
		`"entry": "./src/app.js"`: {"./src/app.js"},
		// not an entry
		`for (const entry of files) copy(entry, "x.ts");`: nil,
		`const ok = entry === "a.ts";`:                    nil,
		`entryPoints: [file]`:                             nil,
		"entryPoints: [`src/${name}.ts`]":                 nil,
	}
	for content, want := range cases {
		if got := entryStrings(content); !reflect.DeepEqual(got, want) {
			t.Errorf("entryStrings(%q) = %q, want %q", content, got, want)
		}
	}
}

func TestCommandEntries(t *testing.T) {
	type res struct{ entries, files []string }
	cases := map[string]res{
		"node scripts/build-render.mjs":                           {nil, []string{"scripts/build-render.mjs"}},
		"esbuild src/worker.ts --bundle --outfile=dist/worker.js": {[]string{"src/worker.ts"}, nil},
		"tsc && npx esbuild src/a.ts src/b.ts --outdir=dist":      {[]string{"src/a.ts", "src/b.ts"}, nil},
		"tsup src/index.ts --format esm":                          {[]string{"src/index.ts"}, nil},
		"webpack --entry ./src/main.js --mode production":         {[]string{"./src/main.js"}, nil},
		"webpack --entry=./src/main.js":                           {[]string{"./src/main.js"}, nil},
		"next build":                                              {nil, nil},
		"node build/render/main.mjs; esbuild src/x.ts":            {[]string{"src/x.ts"}, []string{"build/render/main.mjs"}},
	}
	for cmd, want := range cases {
		entries, files := commandEntries(cmd)
		if !reflect.DeepEqual(entries, want.entries) || !reflect.DeepEqual(files, want.files) {
			t.Errorf("commandEntries(%q) = %q %q, want %q %q", cmd, entries, files, want.entries, want.files)
		}
	}
}

// A repo whose workers nothing imports: one built by a script package.json
// runs, one by an esbuild call in package.json, one by vite's lib entry.
func TestDetectScriptEntrypointsBundles(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"scripts": {
		"build": "next build",
		"build:render": "node scripts/build-render.mjs",
		"build:edge": "esbuild src/edge.ts --bundle --outfile=dist/edge.js"
	}}`)
	write("scripts/build-render.mjs", `const root = join(dirname(fileURLToPath(import.meta.url)), "..");
await build({ entryPoints: [join(root, "src/render/main.ts")], outfile: "build/main.mjs" });
await build({ entryPoints: ["../src/render/child.ts"] });`)
	write("vite.config.ts", `export default { build: { lib: { entry: "src/lib.ts" } } }`)
	for _, f := range []string{"src/render/main.ts", "src/render/child.ts", "src/edge.ts", "src/lib.ts"} {
		write(f, "export {}\n")
	}

	d := NewDetector(nil, root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var got []string
	for _, ep := range d.DetectScriptEntrypoints() {
		got = append(got, ep.FileId)
	}
	sort.Strings(got)
	// The script itself is run, not built: .mjs isn't among the script
	// entrypoints, and it is read only for what it bundles.
	want := []string{"src/edge.ts", "src/lib.ts", "src/render/child.ts", "src/render/main.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entrypoints = %q, want %q", got, want)
	}
}
