// Package version holds the version string of this build.
package version

// Version is set at release build time from the VERSION file, through
// -ldflags "-X rawconv/internal/version.Version=..." (see scripts/package.sh
// and CONTRIBUTING.md, "Releasing"). Local builds keep the default.
var Version = "dev"
