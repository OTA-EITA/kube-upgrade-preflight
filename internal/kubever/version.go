// Package kubever parses and compares Kubernetes minor versions.
package kubever

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version is a Kubernetes major.minor version. Patch releases never change
// API availability or skew rules, so they are deliberately ignored.
type Version struct {
	Major int
	Minor int
}

// versionRE accepts "1.35", "v1.35", "v1.35.0" and vendor suffixes such as
// "v1.34.1-eks-113cf36" or "v1.33.5+k3s1".
var versionRE = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.\d+)?(?:[-+].*)?$`)

// Parse reads a version string.
func Parse(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("invalid Kubernetes version %q (want e.g. v1.35)", s)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return Version{Major: major, Minor: minor}, nil
}

// MustParse is Parse for constants; it panics on invalid input.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// String formats the version as "v1.35".
func (v Version) String() string { return fmt.Sprintf("v%d.%d", v.Major, v.Minor) }

// Compare returns -1, 0 or 1.
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		return sign(v.Major - o.Major)
	default:
		return sign(v.Minor - o.Minor)
	}
}

// Less reports whether v is older than o.
func (v Version) Less(o Version) bool { return v.Compare(o) < 0 }

// MinorsBehind returns how many minor releases v is behind o within the same
// major version. It is negative when v is newer than o.
func (v Version) MinorsBehind(o Version) int { return o.Minor - v.Minor }

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}
