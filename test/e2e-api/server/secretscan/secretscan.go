// Package secretscan searches captured bytes for a secret value (packet M1-T2 section
// 6.1, rule R8). It looks for the raw value, its standard base64 encoding in all
// three byte alignments and its base64url encoding, in the bytes as given and in
// their gzip or raw-deflate (permessage-deflate) decompression. Every assertion of
// zero findings in the e2e tests has a positive control that finds a planted value
// (TestS10_SearcherPositiveControl).
package secretscan

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/base64"
	"io"
)

// Count returns how many times secret, in any searched form, occurs in data.
func Count(data []byte, secret string) int {
	if secret == "" {
		return 0
	}
	// Red stub (rework 2 red B): the raw form only, no decoding or decompression.
	return bytes.Count(data, []byte(secret))
}

// Forms returns the searched encodings of secret.
func Forms(secret string) [][]byte {
	raw := []byte(secret)
	forms := [][]byte{raw, []byte(base64.RawURLEncoding.EncodeToString(raw))}
	// Standard base64 at each alignment: after 0, 1 or 2 prefix bytes, keep only the
	// characters whose 6 bits come from the secret alone.
	for shift := 0; shift < 3; shift++ {
		enc := base64.StdEncoding.EncodeToString(append(make([]byte, shift), raw...))
		start := (8*shift + 5) / 6
		end := 8 * (shift + len(raw)) / 6
		if end > start {
			forms = append(forms, []byte(enc[start:end]))
		}
	}
	return forms
}

func countForms(data, secret []byte) int {
	n := 0
	seen := map[string]bool{}
	for _, f := range Forms(string(secret)) {
		if len(f) < 8 || seen[string(f)] {
			continue
		}
		seen[string(f)] = true
		n += bytes.Count(data, f)
	}
	return n
}

func gunzip(data []byte) ([]byte, bool) {
	i := bytes.Index(data, []byte{0x1f, 0x8b})
	if i < 0 {
		return nil, false
	}
	r, err := gzip.NewReader(bytes.NewReader(data[i:]))
	if err != nil {
		return nil, false
	}
	out, _ := io.ReadAll(io.LimitReader(r, 64<<20))
	return out, len(out) > 0
}

// inflate tries raw deflate with the permessage-deflate tail (RFC 7692 section 7.2.2).
func inflate(data []byte) ([]byte, bool) {
	r := flate.NewReader(io.MultiReader(bytes.NewReader(data), bytes.NewReader([]byte{0x00, 0x00, 0xff, 0xff})))
	out, _ := io.ReadAll(io.LimitReader(r, 64<<20))
	return out, len(out) > 0
}
