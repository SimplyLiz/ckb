package query

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateTestRunCommand_JSRunner(t *testing.T) {
	tests := []AffectedTest{{FilePath: "tests/a.test.ts"}}
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"vitest config", map[string]string{"vitest.config.mts": ""}, "npx vitest run tests/a.test.ts"},
		{"vitest dependency", map[string]string{"package.json": `{"devDependencies":{"vitest":"^5.0.0"}}`}, "npx vitest run tests/a.test.ts"},
		{"jest config", map[string]string{"jest.config.js": ""}, "npm test -- tests/a.test.ts"},
		{"nothing known", nil, "npx jest tests/a.test.ts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range c.files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := generateTestRunCommand(root, tests)
			if strings.TrimSpace(got) != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
