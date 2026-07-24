// Package ownership marks rendered manifest files as assimilate-generated so
// later runs can tell their own output apart from files a human wrote or
// edited. YAML files carry the marker inline as a first-line comment; JSON
// cannot hold comments, so a JSON file's marker lives in a sidecar file next
// to it. The marker records the SHA-256 of the file body: a present, matching
// marker means "assimilate wrote this and nobody touched it since".
package ownership

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MarkerPrefix is the prefix of the YAML marker comment. The full line is
// MarkerPrefix + <hex sha256> + "\n".
const MarkerPrefix = "# assimilate-hash: "

// SidecarExt is appended to a JSON file path to form its marker sidecar path.
const SidecarExt = ".assimilate"

// isYAML reports whether path has a YAML extension.
func isYAML(path string) bool {
	ext := filepath.Ext(path)
	return ext == ".yaml" || ext == ".yml"
}

// isJSON reports whether path has a JSON extension.
func isJSON(path string) bool {
	return filepath.Ext(path) == ".json"
}

// hasMarkerLine reports whether body starts with the YAML marker prefix.
func hasMarkerLine(body []byte) bool {
	return bytes.HasPrefix(body, []byte(MarkerPrefix))
}

// StripMarker returns the portion of body that participates in the hash.
// For YAML files with a marker line as the first line, the marker line and its
// trailing newline are removed. All other inputs are returned unchanged.
func StripMarker(path string, body []byte) []byte {
	if !isYAML(path) || !hasMarkerLine(body) {
		return body
	}
	nl := bytes.IndexByte(body, '\n')
	if nl < 0 {
		return nil
	}
	return body[nl+1:]
}

// ComputeBodyHash returns the lowercase-hex SHA-256 of the hash-relevant
// portion of body (see StripMarker).
func ComputeBodyHash(path string, body []byte) string {
	sum := sha256.Sum256(StripMarker(path, body))
	return hex.EncodeToString(sum[:])
}

// WriteMarked writes body to path and records assimilate's ownership marker.
// For YAML files, the marker is prepended as a comment line. For JSON files,
// the marker is stored in a sidecar file named <path>+SidecarExt. Parent
// directories are created as needed.
func WriteMarked(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	hash := ComputeBodyHash(path, body)

	switch {
	case isYAML(path):
		out := make([]byte, 0, len(MarkerPrefix)+len(hash)+1+len(body))
		out = append(out, MarkerPrefix...)
		out = append(out, hash...)
		out = append(out, '\n')
		out = append(out, body...)
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return fmt.Errorf("write yaml %s: %w", path, err)
		}
	case isJSON(path):
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return fmt.Errorf("write json %s: %w", path, err)
		}
		if err := os.WriteFile(path+SidecarExt, []byte(hash+"\n"), 0o644); err != nil {
			return fmt.Errorf("write sidecar %s: %w", path+SidecarExt, err)
		}
	default:
		return fmt.Errorf("WriteMarked: unsupported extension for %s", path)
	}
	return nil
}

// FileStatus describes a target path's ownership state.
type FileStatus struct {
	// Exists reports whether the file is present on disk.
	Exists bool
	// Owned reports whether an assimilate marker is present (YAML header or
	// JSON sidecar). False when Exists is false.
	Owned bool
	// Matches reports whether the recorded marker hash equals the current
	// body's hash. False when Owned is false or when the marker is malformed.
	Matches bool
}

// Status inspects path and returns its ownership status. A missing file
// produces FileStatus{} (all false) with a nil error. I/O errors other than
// "not exist" are returned.
func Status(path string) (FileStatus, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return FileStatus{}, nil
	}
	if err != nil {
		return FileStatus{}, fmt.Errorf("read %s: %w", path, err)
	}

	st := FileStatus{Exists: true}

	markerHash, owned, err := readMarker(path, body)
	if err != nil {
		return FileStatus{}, err
	}
	st.Owned = owned
	if !owned {
		return st, nil
	}

	if !isHex64(markerHash) {
		return st, nil // owned but malformed → Matches stays false
	}

	st.Matches = ComputeBodyHash(path, body) == markerHash
	return st, nil
}

// readMarker returns the recorded hash and whether a marker was found.
// For YAML, the marker is the first-line comment. For JSON, the marker is the
// sidecar file. body is the file body for YAML; for JSON it is unused.
func readMarker(path string, body []byte) (hash string, owned bool, err error) {
	switch {
	case isYAML(path):
		if !hasMarkerLine(body) {
			return "", false, nil
		}
		nl := bytes.IndexByte(body, '\n')
		if nl < 0 {
			return "", true, nil
		}
		line := string(body[:nl])
		return strings.TrimSpace(strings.TrimPrefix(line, MarkerPrefix)), true, nil
	case isJSON(path):
		side, err := os.ReadFile(path + SidecarExt)
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("read sidecar %s: %w", path+SidecarExt, err)
		}
		return strings.TrimSpace(string(side)), true, nil
	default:
		return "", false, nil
	}
}

// isHex64 reports whether s is exactly 64 lowercase hex characters.
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
