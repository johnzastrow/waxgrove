package sqlite

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"strings"
)

// newID returns an opaque, sortable-enough identifier. Opaque TEXT rather than
// an autoincrement integer so records can be merged and exported without
// renumbering, and so an ID never leaks a count.
func newID() (string, error) {
	b := make([]byte, 15)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

func str(ns sql.NullString) string { return ns.String }
