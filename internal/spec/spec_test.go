package spec

import "testing"

// Regression: the old encoding flattened args as sorted "k=v" joined with
// \x01, so the crafted value "1\x01b=2" collided with the two-entry map
// {a: 1, b: 2} and silently deduped two different builds onto one image.
func TestKeyInjectiveCraftedArgValue(t *testing.T) {
	a := BuildSpec{Path: "/svc", Platform: "linux/amd64", Args: map[string]string{"a": "1\x01b=2"}}
	b := BuildSpec{Path: "/svc", Platform: "linux/amd64", Args: map[string]string{"a": "1", "b": "2"}}
	if a.Key() == b.Key() {
		t.Fatalf("crafted arg value collides: %q", a.Key())
	}
}

func TestKeyInjectiveBoundaries(t *testing.T) {
	pairs := []struct {
		name string
		a, b BuildSpec
	}{
		{"field boundary", BuildSpec{Path: "/a", BuildFile: "b"}, BuildSpec{Path: "/ab", BuildFile: ""}},
		{"arg key/value boundary", BuildSpec{Args: map[string]string{"ab": "c"}}, BuildSpec{Args: map[string]string{"a": "bc"}}},
		{"value/next-key boundary", BuildSpec{Args: map[string]string{"a": "xb", "": "y"}}, BuildSpec{Args: map[string]string{"a": "x", "b": "y"}}},
	}
	for _, p := range pairs {
		if p.a.Key() == p.b.Key() {
			t.Errorf("%s: distinct specs share key %q", p.name, p.a.Key())
		}
	}
}

func TestKeyArgsOrderInsensitive(t *testing.T) {
	a := BuildSpec{Path: "/svc", Platform: "linux/amd64", Args: map[string]string{}}
	b := BuildSpec{Path: "/svc", Platform: "linux/amd64", Args: map[string]string{}}
	for _, k := range []string{"x", "y", "z", "variant"} {
		a.Args[k] = k + "-v"
	}
	for _, k := range []string{"variant", "z", "y", "x"} {
		b.Args[k] = k + "-v"
	}
	if a.Key() != b.Key() {
		t.Fatalf("same args, different keys: %q vs %q", a.Key(), b.Key())
	}
	if a.Key() != a.Key() {
		t.Fatal("Key is not deterministic")
	}
}
