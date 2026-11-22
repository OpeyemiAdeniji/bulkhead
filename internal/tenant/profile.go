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
	{name: ProfileCapsule, notApplicable: map[string]string{
		// Measured on Capsule 0.15.2: the owners get the cluster role admin in each namespace,
		// on purpose, so that they manage access to their namespaces themselves.
		"GRD-05": "Capsule gives tenant owners the admin role in their namespaces, so that they can bind others to it (self-service by design)",
		"ACC-04": "a Capsule tenant has owners, and no read-only viewer role",
		"ACC-05": "a Capsule tenant has owners, and no read-only viewer role",
	}},
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
var planned = []string{"vcluster"}

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
