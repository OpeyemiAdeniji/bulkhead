// Package version reports the version of the bulkhead binary.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set at build time by the release with -ldflags "-X github.com/OpeyemiAdeniji/bulkhead/internal/version.version=...".
var (
	version = ""
	commit  = ""
	date    = ""
)

// Info describes the binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get returns the version information. Values not set at build time come from the
// build information that Go records, so `go install` and `go build` still report the commit.
func Get() Info {
	info := Info{Version: version, Commit: commit, Date: date, GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		modified := false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
		if modified && info.Commit != "" && commit == "" {
			info.Commit += "-modified"
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	if info.Date == "" {
		info.Date = "unknown"
	}
	return info
}

// String returns one line for humans.
func (i Info) String() string {
	return fmt.Sprintf("bulkhead %s (commit %s, built %s, %s %s)", i.Version, i.Commit, i.Date, i.GoVersion, i.Platform)
}
