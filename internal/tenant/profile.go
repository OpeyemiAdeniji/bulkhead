package tenant

import (
	"fmt"
	"slices"
	"strings"
)

// Profiles say how a platform models its tenants (docs/adr/0022-tenancy-profiles.md): what
// --tenant names, which namespace the checks use, the identities the checker acts as, and
// which checks do not apply.

// The profiles. Namespaces is the default.
const (
	// ProfileNamespaces: a tenant is a namespace, and its admins and viewers are the subjects
	// of two role bindings in it, as Bulkhead's tenant chart makes them.
	ProfileNamespaces = "namespaces"
)

// profile describes one profile.
type profile struct {
	name string
	// notApplicable maps the checks that do not apply to the reason, which the report shows.
	notApplicable map[string]string
}

var profiles = []profile{
	{name: ProfileNamespaces},
}

// Profiles returns the names of the profiles.
func Profiles() []string {
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.name)
	}
	return names
}

// planned are the profiles of ADR 0022 that are not built yet.
var planned = []string{"capsule", "vcluster"}

// CheckProfile returns an error for an unknown profile.
func CheckProfile(name string) error {
	if slices.Contains(Profiles(), name) {
		return nil
	}
	if slices.Contains(planned, name) {
		return fmt.Errorf("the %s profile is not built yet (docs/adr/0022-tenancy-profiles.md); available: %s", name, strings.Join(Profiles(), ", "))
	}
	return fmt.Errorf("unknown profile %q (available: %s)", name, strings.Join(Profiles(), ", "))
}

// NotApplicable returns why a check does not apply to the profile, or "".
func NotApplicable(profileName, checkID string) string {
	for _, p := range profiles {
		if p.name == profileName {
			return p.notApplicable[checkID]
		}
	}
	return ""
}
