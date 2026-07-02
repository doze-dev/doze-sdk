package plugin

import (
	"testing"

	"github.com/doze-dev/doze-sdk/engine"
)

// wrapCaps must make each presence-discovered capability satisfy its interface
// *only* when advertised — a plugin that did not advertise capConverger must not
// satisfy engine.Converger (the silent-no-op bug) — while non-presence caps that
// every plugin has (Spawner) keep resolving through the wrappers.
func TestWrapCapsDiscovery(t *testing.T) {
	type want struct {
		converger, inventory, pruner  bool
		versionless, templater, admin bool
	}
	cases := []struct {
		name string
		caps []string
		want want
	}{
		{"bare", nil, want{}},
		{"converger trio", []string{capConverger, capInventory, capPruner},
			want{converger: true, inventory: true, pruner: true}},
		{"templater + structural (postgres)", []string{capTemplater, capConverger, capInventory, capPruner},
			want{converger: true, inventory: true, pruner: true, templater: true}},
		{"versionless + admin + structural (aws)", []string{capVersionless, capAdmin, capConverger, capInventory, capPruner},
			want{converger: true, inventory: true, pruner: true, versionless: true, admin: true}},
		{"versionless + templater + admin, no structural", []string{capVersionless, capTemplater, capAdmin},
			want{versionless: true, templater: true, admin: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &pluginDriver{caps: map[string]bool{}}
			for _, c := range tc.caps {
				d.caps[c] = true
			}
			got := wrapCaps(d)

			// Spawner must ALWAYS resolve — the regression guard for the wrapping
			// (every plugin runs via a SpawnPlan).
			if _, ok := got.(engine.Spawner); !ok {
				t.Fatalf("Spawner must always resolve through the wrappers")
			}
			check := func(name string, got, want bool) {
				if got != want {
					t.Errorf("%s: got %v, want %v", name, got, want)
				}
			}
			_, cv := got.(engine.Converger)
			_, iv := got.(engine.Inventory)
			_, pr := got.(engine.Pruner)
			_, vl := got.(engine.Versionless)
			_, tm := got.(engine.Templater)
			_, ad := got.(engine.Admin)
			check("Converger", cv, tc.want.converger)
			check("Inventory", iv, tc.want.inventory)
			check("Pruner", pr, tc.want.pruner)
			check("Versionless", vl, tc.want.versionless)
			check("Templater", tm, tc.want.templater)
			check("Admin", ad, tc.want.admin)
		})
	}
}
