// Package version exposes build metadata stamped at link time via -ldflags.
package version

import (
	_ "embed"
	"strconv"
	"strings"
)

//go:embed VERSION
var embedded string

// Version, Commit, BuildTime are stamped at link time via `go build -ldflags "-X ...=..."`. NOTE: keep as bare string literals — function-init breaks -X.
var (
	Version   = ""
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Default returns Version if stamped, else the checked-in `version/VERSION` file.
func Default() string {
	if Version != "" {
		return Version
	}
	return strings.TrimSpace(embedded)
}

type Info struct {
	Version   string
	Commit    string
	BuildTime string
}

func Get() Info {
	return Info{Version: Default(), Commit: Commit, BuildTime: BuildTime}
}

func String() string {
	return "openwa " + Default() + " (commit " + Commit + ", built " + BuildTime + ")"
}

// Triple returns the MAJOR, MINOR, PATCH numbers of Default(), or zeros when it does not parse.
func Triple() [3]uint32 {
	s := strings.TrimPrefix(Default(), "v")
	s, _, _ = strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	var out [3]uint32
	if len(parts) != 3 {
		return out
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return [3]uint32{}
		}
		out[i] = uint32(n)
	}
	return out
}
