package tenant

import (
	"strings"
	"testing"
)

func TestProfiles(t *testing.T) {
	if err := CheckProfile(ProfileNamespaces); err != nil {
		t.Errorf("namespaces: %v", err)
	}
	if err := CheckProfile("capsule"); err == nil || !strings.Contains(err.Error(), "not built yet") {
		t.Errorf("capsule is planned, and the message must say so: %v", err)
	}
	if err := CheckProfile("hnc"); err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Errorf("an unknown profile: %v", err)
	}
	// The namespaces profile is the model the checks are written for: every check applies.
	if why := NotApplicable(ProfileNamespaces, "GRD-05"); why != "" {
		t.Errorf("GRD-05 must apply to the namespaces profile: %s", why)
	}
	err := Options{Profile: "nope", Namespaces: []string{"tenant-a"}, AdminBinding: "x"}.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Errorf("Validate must check the profile: %v", err)
	}
}
