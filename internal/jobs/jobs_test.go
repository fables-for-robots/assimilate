package jobs

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fables-for-robots/jobs-iroh/wire"

	"github.com/fables-for-robots/assimilate/internal/spec"
)

func TestDefaultDataDir(t *testing.T) {
	cases := []struct {
		name               string
		dataDir, xdg, home string
		want               string
	}{
		{name: "explicit", dataDir: "/data/assim", xdg: "/xdg", home: "/home/u", want: "/data/assim"},
		{name: "xdg", xdg: "/xdg", home: "/home/u", want: filepath.Join("/xdg", "assimilate")},
		{name: "home", home: "/home/u", want: filepath.Join("/home/u", ".local", "share", "assimilate")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ASSIMILATE_DATA_DIR", tc.dataDir)
			t.Setenv("XDG_DATA_HOME", tc.xdg)
			t.Setenv("HOME", tc.home)
			if got := DefaultDataDir(); got != tc.want {
				t.Fatalf("DefaultDataDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TestLocalIngestDefinitionKey drives the offline half against a real store
// and a fixture source tree: K is 64-hex, stable across repeated ingests and
// calls, distinct per args, and moves when a source file changes.
func TestLocalIngestDefinitionKey(t *testing.T) {
	ctx := context.Background()
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	srcDir := t.TempDir()
	writeFile(t, srcDir, "BUILD.jobs", "# recipe\n")
	writeFile(t, srcDir, "svc/main.go", "package main\n")

	src, err := l.Ingest(ctx, srcDir)
	if err != nil {
		t.Fatal(err)
	}
	if !hex64.MatchString(src.String()) {
		t.Fatalf("source key %q is not 64-hex", src.String())
	}

	s := spec.BuildSpec{Path: "/", Platform: "linux/amd64", Args: map[string]string{"variant": "slim"}}
	k1, err := l.DefinitionKey(src, s)
	if err != nil {
		t.Fatal(err)
	}
	if !hex64.MatchString(k1) {
		t.Fatalf("K %q is not 64-hex", k1)
	}

	// Stable: same call, and a fresh ingest of unchanged sources.
	if k2, err := l.DefinitionKey(src, s); err != nil || k2 != k1 {
		t.Fatalf("repeat DefinitionKey = %q, %v; want %q", k2, err, k1)
	}
	src2, err := l.Ingest(ctx, srcDir)
	if err != nil {
		t.Fatal(err)
	}
	if src2.String() != src.String() {
		t.Fatalf("re-ingest key %s, want %s", src2, src)
	}
	if k2, err := l.DefinitionKey(src2, s); err != nil || k2 != k1 {
		t.Fatalf("re-ingest DefinitionKey = %q, %v; want %q", k2, err, k1)
	}

	// Distinct per definition knobs.
	for name, other := range map[string]spec.BuildSpec{
		"args":       {Path: "/", Platform: "linux/amd64", Args: map[string]string{"variant": "full"}},
		"platform":   {Path: "/", Platform: "linux/arm64", Args: map[string]string{"variant": "slim"}},
		"build-file": {Path: "/", Platform: "linux/amd64", Args: map[string]string{"variant": "slim"}, BuildFile: "BUILD.prod"},
	} {
		if k, err := l.DefinitionKey(src, other); err != nil || k == k1 {
			t.Fatalf("%s variant: K = %q, %v; must differ from %q", name, k, err, k1)
		}
	}

	// A source change moves the tree key and therefore K.
	writeFile(t, srcDir, "svc/main.go", "package main // changed\n")
	src3, err := l.Ingest(ctx, srcDir)
	if err != nil {
		t.Fatal(err)
	}
	if src3.String() == src.String() {
		t.Fatal("changed source must change the tree key")
	}
	k3, err := l.DefinitionKey(src3, s)
	if err != nil {
		t.Fatal(err)
	}
	if k3 == k1 {
		t.Fatal("changed source must change K")
	}

	// The zero Source is for fakes only — the real path must refuse it.
	if _, err := l.DefinitionKey(Source{}, s); err == nil {
		t.Fatal("DefinitionKey of a zero Source must fail")
	}
}

// TestOpenConflict: the store flock is single-process; a second Open of the
// same data dir fails fast with the descriptive error.
func TestOpenConflict(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "another assimilate is using") {
		t.Fatalf("second Open error = %v, want 'another assimilate is using …'", err)
	}
}

func TestScratchRef(t *testing.T) {
	re := regexp.MustCompile(`^client-push/[0-9a-f]{16}$`)
	a, err := scratchRef()
	if err != nil {
		t.Fatal(err)
	}
	b, err := scratchRef()
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString(a) || !re.MatchString(b) {
		t.Fatalf("scratch refs %q, %q do not match %v", a, b, re)
	}
	if a == b {
		t.Fatalf("scratch refs must be fresh per push, got %q twice", a)
	}
}

func TestCountsSummary(t *testing.T) {
	cases := []struct {
		counts wire.Counts
		want   string
	}{
		{wire.Counts{Total: 7, Done: 3, Running: 1}, "3/7 built · 1 running"},
		{wire.Counts{Total: 7, Done: 7}, "7/7 built"},
		{wire.Counts{Total: 5, Done: 2, Running: 2, Failed: 1}, "2/5 built · 2 running · 1 failed"},
		{wire.Counts{Total: 4, Done: 1, Failed: 3}, "1/4 built · 3 failed"},
		{wire.Counts{}, "0/0 built"},
	}
	for _, tc := range cases {
		if got := countsSummary(tc.counts); got != tc.want {
			t.Errorf("countsSummary(%+v) = %q, want %q", tc.counts, got, tc.want)
		}
	}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
