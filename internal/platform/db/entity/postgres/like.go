package postgres

import "strings"

//nolint:gochecknoglobals // immutable replacer, safe for concurrent use.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// LikePrefix turns user input into a lower-cased LIKE prefix pattern with the wildcards escaped.
func LikePrefix(s string) string {
	return likeEscaper.Replace(strings.ToLower(s)) + "%"
}
