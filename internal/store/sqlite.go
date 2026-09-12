package store

import (
	"net/url"
	"path/filepath"
	"strings"
)

// SQLiteDSN returns a file URI for the host filesystem path with the given
// connection pragmas. Resolve relative paths before URI encoding so they cannot
// become URI authorities. ToSlash preserves literal backslashes on Unix and
// converts Windows separators; a drive-letter path needs a leading URI slash.
func SQLiteDSN(path string, pragmas ...string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	uriPath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	query := url.Values{}
	for _, pragma := range pragmas {
		query.Add("_pragma", pragma)
	}
	return (&url.URL{Scheme: "file", Path: uriPath, RawQuery: query.Encode()}).String(), nil
}
