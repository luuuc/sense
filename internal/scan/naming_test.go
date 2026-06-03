package scan

import (
	"slices"
	"testing"
)

func TestSingularize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"WorkPackages", "WorkPackage"},
		{"Categories", "Category"},
		{"Addresses", "Address"},
		{"Users", "User"},
		{"Status", "Status"},
		{"Class", "Class"},
		{"Address", "Address"},
		{"", ""},
		{"s", "s"},
		{"Boxes", "Box"},
	}
	for _, tc := range cases {
		got := singularize(tc.in)
		if got != tc.want {
			t.Errorf("singularize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitQualified(t *testing.T) {
	cases := []struct {
		in       string
		wantBare string
		wantNS   string
	}{
		{"WorkPackages::CreateService", "CreateService", "WorkPackages"},
		{"Admin::WorkPackages::CreateService", "CreateService", "WorkPackages"},
		{"WorkPackagesController", "WorkPackagesController", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		bare, ns := splitQualified(tc.in)
		if bare != tc.wantBare || ns != tc.wantNS {
			t.Errorf("splitQualified(%q) = (%q, %q), want (%q, %q)",
				tc.in, bare, ns, tc.wantBare, tc.wantNS)
		}
	}
}

func TestModelPrefixes(t *testing.T) {
	cases := []struct {
		qualified string
		want      []string
		ok        bool
	}{
		{"WorkPackagesController", []string{"WorkPackages"}, true},
		{"WorkPackages::CreateService", []string{"Create", "WorkPackages"}, true},
		{"UserMailer", []string{"User"}, true},
		{"UserError", nil, false},
		{"UserGateway", nil, false},
		{"User", nil, false},
		{"Payments::StripeAdapter", nil, false},
	}
	for _, tc := range cases {
		got, ok := modelPrefixes(tc.qualified)
		if ok != tc.ok {
			t.Errorf("modelPrefixes(%q) ok = %v, want %v", tc.qualified, ok, tc.ok)
			continue
		}
		if ok && !slices.Equal(got, tc.want) {
			t.Errorf("modelPrefixes(%q) = %v, want %v", tc.qualified, got, tc.want)
		}
	}
}
