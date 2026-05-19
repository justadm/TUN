package runtime

import "testing"

func TestValidateProfileBundle(t *testing.T) {
	bundle := ProfileBundle{
		APIVersion: "2026-04-14",
		Version:    "v1",
		Profiles: []ProfileDefinition{
			{
				ID:              "ru-balanced",
				SecurityProfile: SecurityProfileBalanced,
				Revision:        1,
				TUN:             ProfileTUN{Mode: "full", Lockdown: false},
			},
			{
				ID:              "ru-high",
				SecurityProfile: SecurityProfileHighRisk,
				Revision:        2,
				TUN:             ProfileTUN{Mode: "full", Lockdown: true},
				Bridge: ProfileBridgePolicy{
					AllowLocalTCPBridge:  false,
					AllowLocalControlAPI: false,
				},
			},
		},
	}
	if err := ValidateProfileBundle(bundle); err != nil {
		t.Fatalf("validate bundle: %v", err)
	}
}

func TestValidateProfileBundleRejectsHighRiskUnsafeBridge(t *testing.T) {
	bundle := ProfileBundle{
		APIVersion: "2026-04-14",
		Version:    "v1",
		Profiles: []ProfileDefinition{
			{
				ID:              "ru-high",
				SecurityProfile: SecurityProfileHighRisk,
				Revision:        1,
				TUN:             ProfileTUN{Mode: "full", Lockdown: true},
				Bridge: ProfileBridgePolicy{
					AllowLocalTCPBridge: true,
				},
			},
		},
	}
	if err := ValidateProfileBundle(bundle); err == nil {
		t.Fatalf("expected validation error for unsafe high_risk bridge policy")
	}
}

func TestProfileManagerApplyAndRollback(t *testing.T) {
	m := NewProfileManager()
	first := ProfileBundle{
		APIVersion: "2026-04-14",
		Version:    "v1",
		Profiles: []ProfileDefinition{
			{
				ID:              "ru-balanced",
				SecurityProfile: SecurityProfileBalanced,
				Revision:        1,
			},
		},
	}
	second := ProfileBundle{
		APIVersion: "2026-04-14",
		Version:    "v2",
		Profiles: []ProfileDefinition{
			{
				ID:              "ru-high",
				SecurityProfile: SecurityProfileHighRisk,
				Revision:        2,
				TUN:             ProfileTUN{Mode: "full", Lockdown: true},
				Bridge: ProfileBridgePolicy{
					AllowLocalTCPBridge:  false,
					AllowLocalControlAPI: false,
				},
			},
		},
	}

	if err := m.Apply(first); err != nil {
		t.Fatalf("apply first bundle: %v", err)
	}
	if err := m.Apply(second); err != nil {
		t.Fatalf("apply second bundle: %v", err)
	}
	cur, ok := m.Current()
	if !ok {
		t.Fatalf("expected current bundle")
	}
	if cur.Version != "v2" {
		t.Fatalf("expected current version v2, got %q", cur.Version)
	}
	if !m.Rollback() {
		t.Fatalf("expected rollback to succeed")
	}
	cur, ok = m.Current()
	if !ok {
		t.Fatalf("expected current bundle after rollback")
	}
	if cur.Version != "v1" {
		t.Fatalf("expected rolled back version v1, got %q", cur.Version)
	}
}

func TestValidateProfileBundleAllowsBGPRouting(t *testing.T) {
	bundle := ProfileBundle{
		APIVersion: "2026-04-19",
		Version:    "v1",
		Profiles: []ProfileDefinition{
			{
				ID:              "ru-bgp",
				SecurityProfile: SecurityProfileBalanced,
				Revision:        1,
				Routing: ProfileRouting{
					Source: "bgp",
					BGP: ProfileRoutingBGP{
						Enabled:      true,
						Neighbor:     "45.154.73.71",
						NeighborAS:   65432,
						HoldTimeSec:  240,
						KeepaliveSec: 80,
						MaxPrefixes:  50000,
					},
				},
			},
		},
	}
	if err := ValidateProfileBundle(bundle); err != nil {
		t.Fatalf("validate bundle: %v", err)
	}
}

func TestValidateProfileBundleRejectsUnknownRoutingSource(t *testing.T) {
	bundle := ProfileBundle{
		APIVersion: "2026-04-19",
		Version:    "v1",
		Profiles: []ProfileDefinition{
			{
				ID:              "ru-unknown",
				SecurityProfile: SecurityProfileBalanced,
				Revision:        1,
				Routing: ProfileRouting{
					Source: "unknown",
				},
			},
		},
	}
	if err := ValidateProfileBundle(bundle); err == nil {
		t.Fatalf("expected error for unknown routing source")
	}
}
