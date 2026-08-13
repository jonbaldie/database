// Package buildinfo owns the small, stable identity reported by the database
// executable.  It deliberately contains no implementation or storage detail.
package buildinfo

import "runtime"

// These values are replaced by the release build with -ldflags. Keeping
// useful development defaults makes local builds and tests self-describing.
var (
	ProductVersion = "0.2.0-dev"
	BuildIdentity  = "development"
)

// Info is the machine-readable version contract. New fields may be added in a
// later schema version, but existing fields keep their meaning within schema v1.
type Info struct {
	Schema                               string `json:"schema"`
	ProductVersion                       string `json:"product_version"`
	BuildIdentity                        string `json:"build_identity"`
	Platform                             string `json:"platform"`
	GoVersion                            string `json:"go_version"`
	DataCompatibility                    string `json:"data_compatibility"`
	BackupCompatibility                  string `json:"backup_compatibility"`
	MySQLApplicationCompatibilityProfile string `json:"mysql_application_compatibility_profile"`
}

// Current returns the identity of this executable. The platform is derived
// from the binary at runtime, so a cross-compiled binary reports its target.
func Current() Info {
	return Info{
		Schema:                               "database.version/v1",
		ProductVersion:                       ProductVersion,
		BuildIdentity:                        BuildIdentity,
		Platform:                             runtime.GOOS + "/" + runtime.GOARCH,
		GoVersion:                            runtime.Version(),
		DataCompatibility:                    "0.1.x",
		BackupCompatibility:                  "0.1.x",
		MySQLApplicationCompatibilityProfile: "0.1",
	}
}
