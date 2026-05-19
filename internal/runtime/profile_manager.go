package runtime

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	SecurityProfileCompat   = "compat"
	SecurityProfileBalanced = "balanced"
	SecurityProfileHighRisk = "high_risk"
)

type ProfileBundle struct {
	APIVersion string              `json:"apiVersion"`
	Version    string              `json:"version"`
	Profiles   []ProfileDefinition `json:"profiles"`
}

type ProfileDefinition struct {
	ID              string              `json:"id"`
	Region          string              `json:"region,omitempty"`
	Title           string              `json:"title,omitempty"`
	SecurityProfile string              `json:"securityProfile"`
	Revision        int                 `json:"revision"`
	Routing         ProfileRouting      `json:"routing,omitempty"`
	DNS             ProfileDNS          `json:"dns,omitempty"`
	TUN             ProfileTUN          `json:"tun,omitempty"`
	Bridge          ProfileBridgePolicy `json:"bridge,omitempty"`
}

type ProfileRouting struct {
	Strategy      string            `json:"strategy,omitempty"`
	Source        string            `json:"source,omitempty"`
	RulesetRef    string            `json:"rulesetRef,omitempty"`
	DefaultAction string            `json:"defaultAction,omitempty"`
	DirectCIDRs   []string          `json:"directCidrs,omitempty"`
	BGP           ProfileRoutingBGP `json:"bgp,omitempty"`
}

type ProfileRoutingBGP struct {
	Enabled       bool   `json:"enabled,omitempty"`
	Neighbor      string `json:"neighbor,omitempty"`
	NeighborAS    uint32 `json:"neighborAs,omitempty"`
	LocalAS       uint32 `json:"localAs,omitempty"`
	HoldTimeSec   int    `json:"holdTimeSec,omitempty"`
	KeepaliveSec  int    `json:"keepaliveSec,omitempty"`
	MaxPrefixes   int    `json:"maxPrefixes,omitempty"`
	ImportPolicy  string `json:"importPolicy,omitempty"`
	PrefixSetName string `json:"prefixSetName,omitempty"`
}

type ProfileDNS struct {
	Mode        string   `json:"mode,omitempty"`
	TemplateRef string   `json:"templateRef,omitempty"`
	Bootstrap   []string `json:"bootstrap,omitempty"`
}

type ProfileTUN struct {
	Mode     string `json:"mode,omitempty"`
	Lockdown bool   `json:"lockdown,omitempty"`
}

type ProfileBridgePolicy struct {
	AllowLocalTCPBridge  bool `json:"allowLocalTCPBridge,omitempty"`
	AllowLocalControlAPI bool `json:"allowLocalControlAPI,omitempty"`
}

type ProfileManager struct {
	mu       sync.RWMutex
	current  *ProfileBundle
	lastGood *ProfileBundle
}

func NewProfileManager() *ProfileManager {
	return &ProfileManager{}
}

func (m *ProfileManager) Apply(bundle ProfileBundle) error {
	if err := ValidateProfileBundle(bundle); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current != nil {
		prev := cloneProfileBundle(*m.current)
		m.lastGood = &prev
	}
	cur := cloneProfileBundle(bundle)
	m.current = &cur
	return nil
}

func (m *ProfileManager) Rollback() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.lastGood == nil {
		return false
	}
	prev := cloneProfileBundle(*m.lastGood)
	m.current = &prev
	return true
}

func (m *ProfileManager) Current() (ProfileBundle, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.current == nil {
		return ProfileBundle{}, false
	}
	out := cloneProfileBundle(*m.current)
	return out, true
}

func ValidateProfileBundle(bundle ProfileBundle) error {
	if len(bundle.Profiles) == 0 {
		return errors.New("profile bundle must include at least one profile")
	}

	seen := make(map[string]struct{}, len(bundle.Profiles))
	for i := range bundle.Profiles {
		p := normalizeProfileDefinition(bundle.Profiles[i])
		if p.ID == "" {
			return fmt.Errorf("profiles[%d].id is required", i)
		}
		if _, ok := seen[p.ID]; ok {
			return fmt.Errorf("profiles[%d].id %q is duplicated", i, p.ID)
		}
		seen[p.ID] = struct{}{}

		switch p.SecurityProfile {
		case SecurityProfileCompat, SecurityProfileBalanced, SecurityProfileHighRisk:
		default:
			return fmt.Errorf("profiles[%d].securityProfile %q is unsupported", i, p.SecurityProfile)
		}
		if p.Revision < 0 {
			return fmt.Errorf("profiles[%d].revision must be >= 0", i)
		}
		switch strings.ToLower(strings.TrimSpace(p.Routing.Source)) {
		case "", "static", "ruleset", "bgp":
		default:
			return fmt.Errorf("profiles[%d].routing.source %q is unsupported", i, p.Routing.Source)
		}
		if p.Routing.BGP.HoldTimeSec < 0 || p.Routing.BGP.KeepaliveSec < 0 || p.Routing.BGP.MaxPrefixes < 0 {
			return fmt.Errorf("profiles[%d].routing.bgp numeric values must be >= 0", i)
		}
		if p.SecurityProfile == SecurityProfileHighRisk {
			if strings.ToLower(strings.TrimSpace(p.TUN.Mode)) != "full" {
				return fmt.Errorf("profiles[%d] high_risk requires tun.mode=full", i)
			}
			if !p.TUN.Lockdown {
				return fmt.Errorf("profiles[%d] high_risk requires tun.lockdown=true", i)
			}
			if p.Bridge.AllowLocalTCPBridge {
				return fmt.Errorf("profiles[%d] high_risk requires bridge.allowLocalTCPBridge=false", i)
			}
			if p.Bridge.AllowLocalControlAPI {
				return fmt.Errorf("profiles[%d] high_risk requires bridge.allowLocalControlAPI=false", i)
			}
		}
	}
	return nil
}

func normalizeProfileDefinition(in ProfileDefinition) ProfileDefinition {
	in.ID = strings.TrimSpace(in.ID)
	in.Region = strings.TrimSpace(in.Region)
	in.Title = strings.TrimSpace(in.Title)
	in.SecurityProfile = strings.ToLower(strings.TrimSpace(in.SecurityProfile))
	in.Routing.Strategy = strings.TrimSpace(in.Routing.Strategy)
	in.Routing.Source = strings.TrimSpace(in.Routing.Source)
	in.Routing.RulesetRef = strings.TrimSpace(in.Routing.RulesetRef)
	in.Routing.DefaultAction = strings.TrimSpace(in.Routing.DefaultAction)
	in.Routing.BGP.Neighbor = strings.TrimSpace(in.Routing.BGP.Neighbor)
	in.Routing.BGP.ImportPolicy = strings.TrimSpace(in.Routing.BGP.ImportPolicy)
	in.Routing.BGP.PrefixSetName = strings.TrimSpace(in.Routing.BGP.PrefixSetName)
	in.DNS.Mode = strings.TrimSpace(in.DNS.Mode)
	in.DNS.TemplateRef = strings.TrimSpace(in.DNS.TemplateRef)
	in.TUN.Mode = strings.TrimSpace(in.TUN.Mode)
	return in
}

func cloneProfileBundle(in ProfileBundle) ProfileBundle {
	out := ProfileBundle{
		APIVersion: strings.TrimSpace(in.APIVersion),
		Version:    strings.TrimSpace(in.Version),
		Profiles:   make([]ProfileDefinition, 0, len(in.Profiles)),
	}
	for _, p := range in.Profiles {
		cp := normalizeProfileDefinition(p)
		cp.Routing.DirectCIDRs = append([]string(nil), p.Routing.DirectCIDRs...)
		cp.DNS.Bootstrap = append([]string(nil), p.DNS.Bootstrap...)
		out.Profiles = append(out.Profiles, cp)
	}
	return out
}
