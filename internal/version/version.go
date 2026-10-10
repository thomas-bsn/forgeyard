// Package version is the commit Forgeyard was built from, set at build time:
//
//	go build -ldflags "-X github.com/thomas-bsn/forgeyard/internal/version.Commit=$(git rev-parse HEAD)"
//
// The server and its agents compare theirs: an agent built from another commit updates itself to the
// server's (see docs/nodes/agent.md).
package version

// Commit is the full commit hash, or "" for a build without one (go run, tests).
var Commit = ""

// Short is the commit as people read it, or "dev" without one.
func Short() string {
	if len(Commit) < 12 {
		if Commit == "" {
			return "dev"
		}
		return Commit
	}
	return Commit[:12]
}
