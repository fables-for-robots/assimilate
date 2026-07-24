package ownership

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestStripMarker(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		in   string
		want string
	}{
		{"yaml without marker", "x.yaml", "apiVersion: v1\n", "apiVersion: v1\n"},
		{"yaml with marker", "x.yaml", MarkerPrefix + "abc\napiVersion: v1\n", "apiVersion: v1\n"},
		{"yml with marker", "x.yml", MarkerPrefix + "abc\nkind: X\n", "kind: X\n"},
		{"json unchanged", "x.json", `{"a":1}` + "\n", `{"a":1}` + "\n"},
		{"yaml only marker no body", "x.yaml", MarkerPrefix + "abc\n", ""},
		{"yaml marker without newline", "x.yaml", MarkerPrefix + "abc", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripMarker(tc.path, []byte(tc.in)); string(got) != tc.want {
				t.Fatalf("StripMarker(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestComputeBodyHash(t *testing.T) {
	body := []byte("apiVersion: v1\nkind: ConfigMap\n")
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])

	// Marked and unmarked YAML hash identically: the marker never hashes
	// itself. JSON hashes the whole body (the marker lives in the sidecar).
	for name, got := range map[string]string{
		"yaml unmarked": ComputeBodyHash("x.yaml", body),
		"yaml marked":   ComputeBodyHash("x.yaml", append([]byte(MarkerPrefix+"deadbeef\n"), body...)),
		"json":          ComputeBodyHash("x.json", body),
	} {
		if got != want {
			t.Errorf("%s hash = %s, want %s", name, got, want)
		}
	}
}

func TestWriteMarkedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deploy.yaml")
	body := []byte("apiVersion: apps/v1\nkind: Deployment\n")

	if err := WriteMarked(path, body); err != nil {
		t.Fatalf("WriteMarked: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := MarkerPrefix + ComputeBodyHash(path, body) + "\n" + string(body)
	if string(written) != want {
		t.Fatalf("file = %q, want %q", written, want)
	}
}

func TestWriteMarkedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := []byte(`{"a":1}` + "\n")

	if err := WriteMarked(path, body); err != nil {
		t.Fatalf("WriteMarked: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != string(body) {
		t.Fatalf("JSON body modified: got %q, want %q", written, body)
	}
	sidecar, err := os.ReadFile(path + SidecarExt)
	if err != nil {
		t.Fatal(err)
	}
	if want := ComputeBodyHash(path, body) + "\n"; string(sidecar) != want {
		t.Fatalf("sidecar = %q, want %q", sidecar, want)
	}
}

func TestWriteMarkedUnsupportedExtension(t *testing.T) {
	if err := WriteMarked(filepath.Join(t.TempDir(), "x.txt"), []byte("hi")); err == nil {
		t.Fatal("no error for unsupported extension")
	}
}

func TestStatus(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ownedYAML := filepath.Join(dir, "owned.yaml")
	if err := WriteMarked(ownedYAML, []byte("kind: X\n")); err != nil {
		t.Fatal(err)
	}
	ownedJSON := filepath.Join(dir, "owned.json")
	if err := WriteMarked(ownedJSON, []byte(`{"a":1}`+"\n")); err != nil {
		t.Fatal(err)
	}
	editedJSON := filepath.Join(dir, "edited.json")
	if err := WriteMarked(editedJSON, []byte(`{"a":1}`+"\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(editedJSON, []byte(`{"a":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
		want FileStatus
	}{
		{"missing", filepath.Join(dir, "gone.yaml"), FileStatus{}},
		{"owned yaml", ownedYAML, FileStatus{Exists: true, Owned: true, Matches: true}},
		{"owned json", ownedJSON, FileStatus{Exists: true, Owned: true, Matches: true}},
		{"unmarked yaml", write("plain.yaml", "kind: X\n"), FileStatus{Exists: true}},
		{"unmarked json", write("plain.json", `{"a":1}`), FileStatus{Exists: true}},
		{"edited yaml", write("edited.yaml", MarkerPrefix+ComputeBodyHash("x.yaml", []byte("a: 1\n"))+"\na: 2\n"), FileStatus{Exists: true, Owned: true}},
		{"edited json", editedJSON, FileStatus{Exists: true, Owned: true}},
		{"malformed marker", write("bad.yaml", MarkerPrefix+"not-hex\nkind: X\n"), FileStatus{Exists: true, Owned: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := Status(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if st != tc.want {
				t.Fatalf("Status = %+v, want %+v", st, tc.want)
			}
		})
	}
}
