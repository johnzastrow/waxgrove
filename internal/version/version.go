// Package version is the single place that knows what this build is.
//
// Two different things, deliberately kept apart:
//
//   - Version is a semantic version, written by hand and changed when the
//     software changes. It is what a person cites.
//   - Commit and BuiltAt are stamped by the build. They identify one artifact
//     exactly, which is what you need when a deployed instance is behaving
//     unexpectedly and "0.4.0" is not specific enough.
//
// A release binary carries both. A `go build` with no flags carries only the
// version, and says so rather than pretending to be a release.
package version

import "runtime/debug"

// Version is the released version. Bump it in the same commit as the change,
// and add the CHANGELOG entry with it.
const Version = "0.5.1"

// Commit and BuiltAt are set with -ldflags at build time. See the Makefile.
var (
	Commit  = ""
	BuiltAt = ""
)

// Full renders the version for display: the semantic version, plus the commit
// when there is one.
//
//	0.4.0 (aa966b5)
//	0.4.0 (devel)
func Full() string {
	c := Commit
	if c == "" {
		c = vcsFromBuildInfo()
	}
	if c == "" {
		return Version
	}
	return Version + " (" + c + ")"
}

// vcsFromBuildInfo recovers the revision Go embeds when a binary is built
// inside a git checkout without ldflags — so a developer build still says which
// commit it is rather than nothing at all.
func vcsFromBuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return ""
}

// Info is what the API reports.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	BuiltAt string `json:"built_at,omitempty"`
	Full    string `json:"full"`
}

// Current returns this build's identity.
func Current() Info {
	return Info{
		Version: Version,
		Commit:  Commit,
		BuiltAt: BuiltAt,
		Full:    Full(),
	}
}
