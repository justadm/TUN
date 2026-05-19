package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"tun/internal/core"
	"tun/internal/engine"
	"tun/internal/runtime"
	"tun/internal/transport"
	"tun/internal/transport/tlsstream"
	"tun/internal/tun"
)

type TunConfig struct {
	Name           string   `json:"name"`
	MTU            int      `json:"mtu"`
	SkipUp         bool     `json:"skipUp"`
	Addresses      []string `json:"addresses"`
	Routes         []string `json:"routes"`
	ConfigMode     string   `json:"configMode"`
	CleanupOnClose bool     `json:"cleanupOnClose"`
}

type RoutePolicy struct {
	Strategy      string         `json:"strategy,omitempty"`
	Source        string         `json:"source,omitempty"`
	RulesetRef    string         `json:"rulesetRef,omitempty"`
	DefaultAction string         `json:"defaultAction,omitempty"`
	DirectCIDRs   []string       `json:"directCidrs,omitempty"`
	BGP           BGPRoutePolicy `json:"bgp,omitempty"`
}

type BGPRoutePolicy struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	Neighbor      string `json:"neighbor,omitempty"`
	NeighborAS    uint32 `json:"neighborAs,omitempty"`
	LocalAS       uint32 `json:"localAs,omitempty"`
	HoldTimeSec   int    `json:"holdTimeSec,omitempty"`
	KeepaliveSec  int    `json:"keepaliveSec,omitempty"`
	MaxPrefixes   int    `json:"maxPrefixes,omitempty"`
	ImportPolicy  string `json:"importPolicy,omitempty"`
	PrefixSetName string `json:"prefixSetName,omitempty"`
}

type DNSPolicy struct {
	Mode        string   `json:"mode,omitempty"`
	TemplateRef string   `json:"templateRef,omitempty"`
	Bootstrap   []string `json:"bootstrap,omitempty"`
}

type BridgePolicy struct {
	AllowLocalTCPBridge  *bool `json:"allowLocalTCPBridge,omitempty"`
	AllowLocalControlAPI *bool `json:"allowLocalControlAPI,omitempty"`
}

type SupportConfig struct {
	RuntimeVersion string `json:"runtimeVersion"`
	BuildInfo      string `json:"buildInfo"`
	Ring           string `json:"ring"`
	HostID         string `json:"hostID"`
	SigningKeyFile string `json:"signingKeyFile"`
	SigningKeyID   string `json:"signingKeyID"`
}

type GatewayHints struct {
	Priority  int `json:"priority,omitempty"`
	LoadScore int `json:"loadScore,omitempty"`
	RTTScore  int `json:"rttScore,omitempty"`
}

type GatewayEndpointConfig struct {
	Addr       string `json:"addr"`
	ServerName string `json:"serverName,omitempty"`
}

type GatewayConfig struct {
	GatewayID string                  `json:"gatewayID"`
	Region    string                  `json:"region,omitempty"`
	Health    string                  `json:"health,omitempty"`
	Endpoints []GatewayEndpointConfig `json:"endpoints"`
	Hints     GatewayHints            `json:"hints,omitempty"`
}

type GatewayPolicy struct {
	AutoSelectEnabled *bool         `json:"autoSelectEnabled,omitempty"`
	ForceGatewayID    string        `json:"forceGatewayID,omitempty"`
	StickyDuration    time.Duration `json:"stickyDuration,omitempty"`
	CooldownMin       time.Duration `json:"cooldownMin,omitempty"`
	CooldownMax       time.Duration `json:"cooldownMax,omitempty"`
	SwitchHysteresis  int           `json:"switchHysteresis,omitempty"`
}

type RekeyPolicy struct {
	Enabled       *bool         `json:"enabled,omitempty"`
	AckRetries    int           `json:"ackRetries,omitempty"`
	AckRetryDelay time.Duration `json:"ackRetryDelay,omitempty"`

	InitInterval   time.Duration `json:"initInterval,omitempty"`
	InitAckTimeout time.Duration `json:"initAckTimeout,omitempty"`
	InitRetries    int           `json:"initRetries,omitempty"`
	InitRetryDelay time.Duration `json:"initRetryDelay,omitempty"`
	InitOverlap    time.Duration `json:"initOverlap,omitempty"`
}

type ProfileBootstrap struct {
	ProfileID          string          `json:"profileID,omitempty"`
	Region             string          `json:"region,omitempty"`
	SecurityProfile    string          `json:"securityProfile,omitempty"`
	Addr               string          `json:"addr"`
	ServerName         string          `json:"serverName"`
	Insecure           bool            `json:"insecure"`
	ClientID           string          `json:"clientID"`
	ServerID           string          `json:"serverID,omitempty"`
	ServerStaticPubB64 string          `json:"serverStaticPub"`
	Plain              bool            `json:"plain"`
	MaxRetries         int             `json:"maxRetries"`
	ConnectTimeout     time.Duration   `json:"connectTimeout"`
	Gateways           []GatewayConfig `json:"gateways,omitempty"`
	GatewayPolicy      GatewayPolicy   `json:"gatewayPolicy,omitempty"`
	RekeyPolicy        RekeyPolicy     `json:"rekeyPolicy,omitempty"`
	Routing            RoutePolicy     `json:"routing,omitempty"`
	DNS                DNSPolicy       `json:"dns,omitempty"`
	Bridge             BridgePolicy    `json:"bridge,omitempty"`
	Tun                TunConfig       `json:"tun"`
	Support            SupportConfig   `json:"support"`
}

type StartRequest struct {
	ProfileBootstrap ProfileBootstrap `json:"profileBootstrap"`
	DeviceID         string           `json:"deviceID"`
}

type ValidateBootstrapRequest struct {
	ProfileBootstrap ProfileBootstrap `json:"profileBootstrap"`
}

type StopRequest struct {
	Timeout time.Duration `json:"timeout"`
}

type CollectSupportBundleRequest struct {
	Support SupportConfig `json:"support"`
}

type SecurityEvaluateRequest struct {
	TenantID             string `json:"tenantID,omitempty"`
	DeviceID             string `json:"deviceID,omitempty"`
	ClientIP             string `json:"clientIP,omitempty"`
	ASN                  string `json:"asn,omitempty"`
	GeoIPDetected        bool   `json:"geoipDetected"`
	DirectDetected       bool   `json:"directDetected"`
	IndirectDetected     bool   `json:"indirectDetected"`
	HostingRisk          bool   `json:"hostingRisk"`
	TorRisk              bool   `json:"torRisk"`
	VPNReputationRisk    bool   `json:"vpnReputationRisk"`
	CorporateWhitelisted bool   `json:"corporateWhitelisted"`
	ServerCountry        string `json:"serverCountry,omitempty"`
	ClientCountry        string `json:"clientCountry,omitempty"`
	ClientRegion         string `json:"clientRegion,omitempty"`
	ICloudPrivateRelay   bool   `json:"icloudPrivateRelay"`
	RoamingLikely        bool   `json:"roamingLikely"`
	RepeatOffenseCount   int    `json:"repeatOffenseCount"`
	SignalTimestamp      int64  `json:"signalTimestamp,omitempty"`
	SignalNonce          string `json:"signalNonce,omitempty"`
	SignalSignature      string `json:"signalSignature,omitempty"`
	ReputationIP         string `json:"reputationIP,omitempty"`
}

type SecurityEvaluateResponse struct {
	OK             bool     `json:"ok"`
	Decision       string   `json:"decision"`
	ProtectionPlan []string `json:"protectionPlan"`
	RiskScore      int      `json:"riskScore"`
	Reasons        []string `json:"reasons"`
	PolicyProfile  string   `json:"policyProfile,omitempty"`
	HardBlock      bool     `json:"hardBlock"`
	Provenance     string   `json:"provenance,omitempty"`
}

type SecurityReputationUpsertRequest struct {
	TenantID       string        `json:"tenantID,omitempty"`
	IP             string        `json:"ip"`
	Source         string        `json:"source"`
	RiskType       string        `json:"riskType"`
	Confidence     int           `json:"confidence"`
	TTL            time.Duration `json:"ttl"`
	ObservedAtUnix int64         `json:"observedAtUnix,omitempty"`
}

type SecurityReputationEntry struct {
	TenantID    string    `json:"tenantID,omitempty"`
	IP          string    `json:"ip"`
	Source      string    `json:"source"`
	RiskType    string    `json:"riskType"`
	Confidence  int       `json:"confidence"`
	ExpiresAt   time.Time `json:"expiresAt"`
	ObservedAt  time.Time `json:"observedAt"`
	SourceScore int       `json:"sourceScore"`
}

type SecurityReputationUpsertResponse struct {
	OK    bool                    `json:"ok"`
	Entry SecurityReputationEntry `json:"entry"`
}

type SecurityTenantPolicy struct {
	TenantID            string `json:"tenantID"`
	Profile             string `json:"profile"`
	Enforce             bool   `json:"enforce"`
	HysteresisThreshold int    `json:"hysteresisThreshold"`
	HysteresisWindowSec int    `json:"hysteresisWindowSec"`
}

type SecurityTenantPolicyResponse struct {
	OK     bool                 `json:"ok"`
	Policy SecurityTenantPolicy `json:"policy"`
}

type CorporateAllowRule struct {
	TenantID  string    `json:"tenantID,omitempty"`
	ASN       string    `json:"asn,omitempty"`
	CIDR      string    `json:"cidr,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

type CorporateAllowRuleUpsertRequest struct {
	TenantID string        `json:"tenantID,omitempty"`
	ASN      string        `json:"asn,omitempty"`
	CIDR     string        `json:"cidr,omitempty"`
	Reason   string        `json:"reason,omitempty"`
	TTL      time.Duration `json:"ttl"`
}

type CorporateAllowRuleUpsertResponse struct {
	OK   bool               `json:"ok"`
	Rule CorporateAllowRule `json:"rule"`
}

type SecurityAuditEntry struct {
	At       time.Time `json:"at"`
	TenantID string    `json:"tenantID,omitempty"`
	DeviceID string    `json:"deviceID,omitempty"`
	Event    string    `json:"event"`
	Detail   string    `json:"detail,omitempty"`
}

type SecuritySignalIngestRequest struct {
	Signal           SecurityEvaluateRequest `json:"signal"`
	Evaluate         *bool                   `json:"evaluate,omitempty"`
	RequireSignature *bool                   `json:"requireSignature,omitempty"`
}

type SecuritySignalIngestResponse struct {
	OK         bool                      `json:"ok"`
	IngestedAt time.Time                 `json:"ingestedAt"`
	TenantID   string                    `json:"tenantID"`
	DeviceID   string                    `json:"deviceID,omitempty"`
	Evaluated  bool                      `json:"evaluated"`
	Result     *SecurityEvaluateResponse `json:"result,omitempty"`
}

type SecurityPolicyRolloutRequest struct {
	DefaultProfile string   `json:"defaultProfile"`
	StrictTenants  []string `json:"strictTenants,omitempty"`
}

type SecurityPolicyRolloutResponse struct {
	OK             bool     `json:"ok"`
	DefaultProfile string   `json:"defaultProfile"`
	StrictTenants  []string `json:"strictTenants"`
}

type GatewaySelectRequest struct {
	GatewayID string `json:"gatewayID"`
}

type LinkActionResponse struct {
	OK     bool               `json:"ok"`
	Action string             `json:"action"`
	Link   LinkStatusResponse `json:"link"`
}

type LinkReadiness struct {
	ContractVersion     string   `json:"contractVersion"`
	Ready               bool     `json:"ready"`
	Running             bool     `json:"running"`
	Established         bool     `json:"established"`
	SessionPresent      bool     `json:"sessionPresent"`
	HandshakeObserved   bool     `json:"handshakeObserved"`
	HandshakeFresh      bool     `json:"handshakeFresh"`
	HandshakeMaxAgeSec  int64    `json:"handshakeMaxAgeSec"`
	LastHandshakeAgeSec *int64   `json:"lastHandshakeAgeSec,omitempty"`
	Reasons             []string `json:"reasons,omitempty"`
}

type HealthResponse struct {
	Live          bool          `json:"live"`
	Ready         bool          `json:"ready"`
	Running       bool          `json:"running"`
	State         runtime.State `json:"state"`
	LinkReadiness LinkReadiness `json:"linkReadiness"`
}

type LinkStatusResponse struct {
	LinkID           string             `json:"linkID"`
	DeviceID         string             `json:"deviceID,omitempty"`
	ProfileID        string             `json:"profileID,omitempty"`
	SecurityProfile  string             `json:"securityProfile,omitempty"`
	Role             runtime.Role       `json:"role"`
	DesiredState     string             `json:"desiredState"`
	ObservedState    runtime.State      `json:"observedState"`
	Health           string             `json:"health"`
	Running          bool               `json:"running"`
	SessionID        string             `json:"sessionID,omitempty"`
	LeaseOwner       string             `json:"leaseOwner,omitempty"`
	LeaseID          string             `json:"leaseID,omitempty"`
	LastError        string             `json:"lastError,omitempty"`
	ErrorClass       runtime.ErrorClass `json:"errorClass,omitempty"`
	LastEventAt      *time.Time         `json:"lastEventAt,omitempty"`
	LastTransitionAt *time.Time         `json:"lastTransitionAt,omitempty"`
	LastHandshakeAt  *time.Time         `json:"lastHandshakeAt,omitempty"`
	LastRxAt         *time.Time         `json:"lastRxAt,omitempty"`
	LastTxAt         *time.Time         `json:"lastTxAt,omitempty"`
	RxBytes          uint64             `json:"rxBytes"`
	TxBytes          uint64             `json:"txBytes"`
	GatewayID        string             `json:"gatewayID,omitempty"`
	GatewayAddr      string             `json:"gatewayAddr,omitempty"`
	TunName          string             `json:"tunName,omitempty"`
	Readiness        LinkReadiness      `json:"readiness"`
	Snapshot         *runtime.Snapshot  `json:"snapshot,omitempty"`
}

type ValidateBootstrapResponse struct {
	OK         bool                `json:"ok"`
	Normalized ProfileBootstrap    `json:"normalized"`
	Preflight  tun.PreflightReport `json:"preflight"`
}

type LeaseAcquireRequest struct {
	Owner string        `json:"owner"`
	TTL   time.Duration `json:"ttl"`
}

type LeaseRenewRequest struct {
	LeaseID string        `json:"leaseId"`
	TTL     time.Duration `json:"ttl"`
}

type LeaseHeartbeatRequest struct {
	LeaseID string        `json:"leaseId"`
	TTL     time.Duration `json:"ttl"`
}

type LeaseTakeoverRequest struct {
	Owner       string        `json:"owner"`
	TTL         time.Duration `json:"ttl"`
	PrevLeaseID string        `json:"prevLeaseId"`
}

type LeaseReleaseRequest struct {
	LeaseID string `json:"leaseId"`
}

type LeaseStatusResponse struct {
	Active    bool       `json:"active"`
	LeaseID   string     `json:"leaseId,omitempty"`
	Owner     string     `json:"owner,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type StatusResponse struct {
	Running         bool                `json:"running"`
	DeviceID        string              `json:"deviceID,omitempty"`
	ProfileID       string              `json:"profileID,omitempty"`
	SecurityProfile string              `json:"securityProfile,omitempty"`
	StartedAt       *time.Time          `json:"startedAt,omitempty"`
	StoppedAt       *time.Time          `json:"stoppedAt,omitempty"`
	LastError       string              `json:"lastError,omitempty"`
	LastEventAt     *time.Time          `json:"lastEventAt,omitempty"`
	LastState       runtime.State       `json:"lastState,omitempty"`
	LastClass       runtime.ErrorClass  `json:"lastErrorClass,omitempty"`
	LastSnapshot    *runtime.Snapshot   `json:"lastSnapshot,omitempty"`
	Lease           LeaseStatusResponse `json:"lease"`
}

type HelperSchemaResponse struct {
	APIVersion              string                  `json:"apiVersion"`
	GatewayPoolVersion      string                  `json:"gatewayPoolVersion,omitempty"`
	SecurityContractVersion string                  `json:"securityContractVersion,omitempty"`
	ProfileBundleVersion    string                  `json:"profileBundleVersion,omitempty"`
	AuthRequired            bool                    `json:"authRequired"`
	AuthSchemes             []string                `json:"authSchemes,omitempty"`
	RequestID               string                  `json:"requestIdHeader,omitempty"`
	LeaseHeader             string                  `json:"leaseIdHeader,omitempty"`
	Idempotency             string                  `json:"idempotency,omitempty"`
	Bootstrap               HelperBootstrapContract `json:"bootstrap"`
	Endpoints               []string                `json:"endpoints"`
	Legacy                  []string                `json:"legacyAliases"`
}

const helperAPIVersion = "v1"
const helperGatewayPoolVersion = "2026-04-10"
const helperSecurityContractVersion = "2026-04-13"
const helperProfileBundleVersion = "2026-04-19"
const helperBootstrapSchemaVersion = "2026-04-19"
const helperLinkReadinessContractVersion = "2026-04-27"
const helperLinkReadinessHandshakeMaxAge = 30 * time.Minute

var (
	errLinkNotFound            = errors.New("link not found")
	errRuntimeNotRunning       = errors.New("runtime is not running")
	errNoBootstrapConfigured   = errors.New("no bootstrap applied")
	errGatewayIDRequired       = errors.New("gatewayID is required")
	errGatewayPoolUnavailable  = errors.New("gateway pool is not configured")
	errGatewayNotFoundInPolicy = errors.New("gatewayID is not present in profileBootstrap.gateways")
)

type HelperBootstrapContract struct {
	SchemaVersion             string   `json:"schemaVersion"`
	RequiredBootstrapFields   []string `json:"requiredBootstrapFields"`
	OptionalBootstrapFields   []string `json:"optionalBootstrapFields"`
	GatewayPoolSupported      bool     `json:"gatewayPoolSupported"`
	GatewayPolicySupported    bool     `json:"gatewayPolicySupported"`
	RekeyPolicySupported      bool     `json:"rekeyPolicySupported"`
	ProfileBundleSupported    bool     `json:"profileBundleSupported"`
	SecurityProfileSupported  bool     `json:"securityProfileSupported"`
	SupportedSecurityProfiles []string `json:"supportedSecurityProfiles,omitempty"`
}

type BridgeLeaseConfig struct {
	Owner    string        `json:"owner"`
	TTL      time.Duration `json:"ttl"`
	Takeover *bool         `json:"takeover,omitempty"`
	Ensure   *bool         `json:"ensure,omitempty"`
}

type BridgeStartupRequest struct {
	ProfileBootstrap ProfileBootstrap       `json:"profileBootstrap"`
	ProfileBundle    *runtime.ProfileBundle `json:"profileBundle,omitempty"`
	DeviceID         string                 `json:"deviceID"`
	Lease            BridgeLeaseConfig      `json:"lease"`
	Wait             *bool                  `json:"wait,omitempty"`
	WaitState        runtime.State          `json:"waitState"`
	WaitTimeout      time.Duration          `json:"waitTimeout"`
}

type BridgeStartupResponse struct {
	OK       bool                       `json:"ok"`
	Lease    LeaseStatusResponse        `json:"lease"`
	Validate *ValidateBootstrapResponse `json:"validate,omitempty"`
	Applied  bool                       `json:"applied"`
	Started  bool                       `json:"started"`
	Waited   bool                       `json:"waited"`
	Status   *StatusResponse            `json:"status,omitempty"`
	Profile  *runtime.ProfileBundle     `json:"profile,omitempty"`
}

type BridgeShutdownRequest struct {
	Lease      BridgeLeaseConfig `json:"lease"`
	Timeout    time.Duration     `json:"timeout"`
	BestEffort *bool             `json:"bestEffort,omitempty"`
}

type BridgeShutdownResponse struct {
	OK           bool                `json:"ok"`
	Lease        LeaseStatusResponse `json:"lease"`
	Stopped      bool                `json:"stopped"`
	Released     bool                `json:"released"`
	StopError    string              `json:"stopError,omitempty"`
	ReleaseError string              `json:"releaseError,omitempty"`
}

type BridgeReconcileRequest struct {
	Lease BridgeLeaseConfig `json:"lease"`
}

type BridgeReconcileResponse struct {
	OK     bool                `json:"ok"`
	Plan   string              `json:"plan"`
	Lease  LeaseStatusResponse `json:"lease"`
	Status StatusResponse      `json:"status"`
	Health HealthResponse      `json:"health"`
}

type BridgeAutopilotRequest struct {
	Startup      BridgeStartupRequest   `json:"startup"`
	Shutdown     BridgeShutdownRequest  `json:"shutdown"`
	Reconcile    BridgeReconcileRequest `json:"reconcile"`
	MaxSteps     int                    `json:"maxSteps"`
	AllowRestart *bool                  `json:"allowRestart,omitempty"`
}

type BridgeAutopilotStep struct {
	Step   int    `json:"step"`
	Plan   string `json:"plan"`
	Action string `json:"action"`
}

type BridgeAutopilotResponse struct {
	OK        bool                  `json:"ok"`
	FinalPlan string                `json:"finalPlan"`
	Steps     []BridgeAutopilotStep `json:"steps"`
}

type BridgeAutopilotDaemonRequest struct {
	Autopilot       BridgeAutopilotRequest `json:"autopilot"`
	Interval        time.Duration          `json:"interval"`
	Duration        time.Duration          `json:"duration"`
	ContinueOnError *bool                  `json:"continueOnError,omitempty"`
}

type BridgeAutopilotDaemonTick struct {
	Run       int                     `json:"run"`
	At        time.Time               `json:"at"`
	OK        bool                    `json:"ok"`
	Error     string                  `json:"error,omitempty"`
	Autopilot BridgeAutopilotResponse `json:"autopilot"`
}

type BridgeAutopilotDaemonResponse struct {
	OK    bool                        `json:"ok"`
	Runs  int                         `json:"runs"`
	Ticks []BridgeAutopilotDaemonTick `json:"ticks"`
}

type ProfileApplyRequest struct {
	Bundle runtime.ProfileBundle `json:"bundle"`
}

type ProfileApplyResponse struct {
	OK      bool                  `json:"ok"`
	Current runtime.ProfileBundle `json:"current"`
}

type apiErrorEnvelope struct {
	OK    bool     `json:"ok"`
	Error apiError `json:"error"`
}

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

type idempotencyRecord struct {
	Status      int
	ContentType string
	Body        []byte
}

type helperManager struct {
	mu sync.Mutex

	startedAt    *time.Time
	stoppedAt    *time.Time
	lastError    string
	deviceID     string
	bootstrap    ProfileBootstrap
	hasBootstrap bool

	running     bool
	cancel      context.CancelFunc
	done        chan struct{}
	lastEvent   runtime.Event
	lastEventAt *time.Time
	linkID      string

	collector    *runtime.SupportBundleCollector
	stateFile    string
	subscribers  map[int]chan runtime.Event
	nextSubID    int
	daemonSubs   map[int]chan BridgeAutopilotDaemonTick
	nextDaemonID int
	idempotency  map[string]idempotencyRecord
	lease        *leaseState
	security     securityState
	profiles     *runtime.ProfileManager

	runFn               func(context.Context, ProfileBootstrap, func(runtime.Event)) error
	validateBootstrapFn func(ValidateBootstrapRequest) (ValidateBootstrapResponse, error)
}

type securityState struct {
	signalHMACKey       []byte
	usedSignalNonces    map[string]time.Time
	reputationCache     map[string]SecurityReputationEntry
	reputationAudit     []SecurityAuditEntry
	ingestAudit         []SecuritySignalIngestResponse
	tenantPolicies      map[string]SecurityTenantPolicy
	decisionHistory     map[string][]securityDecisionRecord
	corporateAllowRules []CorporateAllowRule
}

type securityDecisionRecord struct {
	At       time.Time
	Decision string
}

type leaseState struct {
	ID        string
	Owner     string
	ExpiresAt time.Time
}

func newHelperManager(stateFile string) *helperManager {
	m := &helperManager{
		collector:   runtime.NewSupportBundleCollector(5000),
		stateFile:   strings.TrimSpace(stateFile),
		subscribers: make(map[int]chan runtime.Event),
		daemonSubs:  make(map[int]chan BridgeAutopilotDaemonTick),
		idempotency: make(map[string]idempotencyRecord),
		security: securityState{
			signalHMACKey:       []byte(strings.TrimSpace(os.Getenv("SECURITY_SIGNAL_HMAC_KEY"))),
			usedSignalNonces:    make(map[string]time.Time),
			reputationCache:     make(map[string]SecurityReputationEntry),
			reputationAudit:     make([]SecurityAuditEntry, 0, 256),
			ingestAudit:         make([]SecuritySignalIngestResponse, 0, 256),
			tenantPolicies:      make(map[string]SecurityTenantPolicy),
			decisionHistory:     make(map[string][]securityDecisionRecord),
			corporateAllowRules: make([]CorporateAllowRule, 0, 64),
		},
		profiles: runtime.NewProfileManager(),
	}
	m.runFn = m.runRuntimeClient
	_ = m.loadState()
	return m
}

func (m *helperManager) start(req StartRequest) error {
	return m.startWithOptions(req, true)
}

func (m *helperManager) startWithOptions(req StartRequest, syncProfile bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return errors.New("runtime already running")
	}
	if err := normalizeBootstrap(&req.ProfileBootstrap); err != nil {
		return err
	}
	prev, hadPrev := m.snapshotBootstrapStateLocked()
	if syncProfile {
		if err := m.applyProfileBundleLocked(req.ProfileBootstrap); err != nil {
			return err
		}
	} else {
		m.syncBootstrapFromCurrentProfileLocked(&req.ProfileBootstrap)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	now := time.Now().UTC()
	m.startedAt = &now
	m.stoppedAt = nil
	m.lastError = ""
	m.deviceID = strings.TrimSpace(req.DeviceID)
	m.bootstrap = req.ProfileBootstrap
	m.hasBootstrap = true
	m.linkID = helperLinkID(m.deviceID, m.bootstrap)
	if err := m.saveStateLocked(); err != nil {
		m.restoreBootstrapStateLocked(prev, hadPrev)
		return err
	}
	m.running = true
	m.cancel = cancel
	m.done = done
	m.lastEvent = runtime.Event{}
	m.lastEventAt = nil
	m.collector = runtime.NewSupportBundleCollector(5000)

	go func(bootstrap ProfileBootstrap) {
		err := m.runFn(runCtx, bootstrap, m.onEvent)
		m.mu.Lock()
		defer m.mu.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) {
			m.lastError = err.Error()
		}
		now := time.Now().UTC()
		m.stoppedAt = &now
		m.running = false
		m.cancel = nil
		close(done)
	}(req.ProfileBootstrap)

	return nil
}

func (m *helperManager) applyBootstrap(req StartRequest) error {
	return m.applyBootstrapWithOptions(req, true)
}

func (m *helperManager) applyBootstrapWithOptions(req StartRequest, syncProfile bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := normalizeBootstrap(&req.ProfileBootstrap); err != nil {
		return err
	}
	prev, hadPrev := m.snapshotBootstrapStateLocked()
	if syncProfile {
		if err := m.applyProfileBundleLocked(req.ProfileBootstrap); err != nil {
			return err
		}
	} else {
		m.syncBootstrapFromCurrentProfileLocked(&req.ProfileBootstrap)
	}
	m.deviceID = strings.TrimSpace(req.DeviceID)
	m.bootstrap = req.ProfileBootstrap
	m.hasBootstrap = true
	m.linkID = helperLinkID(m.deviceID, m.bootstrap)
	if err := m.saveStateLocked(); err != nil {
		m.restoreBootstrapStateLocked(prev, hadPrev)
		return err
	}
	return nil
}

func (m *helperManager) validateBootstrap(req ValidateBootstrapRequest) (ValidateBootstrapResponse, error) {
	if m.validateBootstrapFn != nil {
		return m.validateBootstrapFn(req)
	}
	pb := req.ProfileBootstrap
	if err := normalizeBootstrap(&pb); err != nil {
		return ValidateBootstrapResponse{}, err
	}
	report, err := tun.BuildPreflightReport(tun.OpenOptions{
		Name:           pb.Tun.Name,
		MTU:            pb.Tun.MTU,
		SkipUp:         pb.Tun.SkipUp,
		Addresses:      pb.Tun.Addresses,
		Routes:         pb.Tun.Routes,
		ConfigMode:     pb.Tun.ConfigMode,
		CleanupOnClose: pb.Tun.CleanupOnClose,
	})
	if err != nil {
		return ValidateBootstrapResponse{
			OK:         false,
			Normalized: pb,
			Preflight:  report,
		}, err
	}
	return ValidateBootstrapResponse{
		OK:         true,
		Normalized: pb,
		Preflight:  report,
	}, nil
}

func (m *helperManager) leaseAcquire(req LeaseAcquireRequest) (LeaseStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLeaseLocked(time.Now())
	if m.lease != nil {
		return LeaseStatusResponse{
			Active:    true,
			LeaseID:   m.lease.ID,
			Owner:     m.lease.Owner,
			ExpiresAt: ptrTime(m.lease.ExpiresAt),
		}, errors.New("lease already active")
	}
	ttl := normalizeLeaseTTL(req.TTL)
	now := time.Now().UTC()
	m.lease = &leaseState{
		ID:        generateRequestID(),
		Owner:     strings.TrimSpace(req.Owner),
		ExpiresAt: now.Add(ttl),
	}
	if m.lease.Owner == "" {
		m.lease.Owner = "unknown"
	}
	return LeaseStatusResponse{
		Active:    true,
		LeaseID:   m.lease.ID,
		Owner:     m.lease.Owner,
		ExpiresAt: ptrTime(m.lease.ExpiresAt),
	}, nil
}

func (m *helperManager) leaseRenew(req LeaseRenewRequest) (LeaseStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLeaseLocked(time.Now())
	if m.lease == nil {
		return LeaseStatusResponse{Active: false}, errors.New("no active lease")
	}
	if strings.TrimSpace(req.LeaseID) == "" || req.LeaseID != m.lease.ID {
		return LeaseStatusResponse{
			Active:    true,
			LeaseID:   m.lease.ID,
			Owner:     m.lease.Owner,
			ExpiresAt: ptrTime(m.lease.ExpiresAt),
		}, errors.New("lease id mismatch")
	}
	ttl := normalizeLeaseTTL(req.TTL)
	m.lease.ExpiresAt = time.Now().UTC().Add(ttl)
	return LeaseStatusResponse{
		Active:    true,
		LeaseID:   m.lease.ID,
		Owner:     m.lease.Owner,
		ExpiresAt: ptrTime(m.lease.ExpiresAt),
	}, nil
}

func (m *helperManager) leaseHeartbeat(req LeaseHeartbeatRequest, headerLeaseID string) (LeaseStatusResponse, error) {
	return m.leaseRenew(LeaseRenewRequest{
		LeaseID: coalesceLeaseID(req.LeaseID, headerLeaseID),
		TTL:     req.TTL,
	})
}

func (m *helperManager) leaseTakeover(req LeaseTakeoverRequest) (LeaseStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLeaseLocked(time.Now())
	if m.lease == nil {
		ttl := normalizeLeaseTTL(req.TTL)
		now := time.Now().UTC()
		m.lease = &leaseState{
			ID:        generateRequestID(),
			Owner:     strings.TrimSpace(req.Owner),
			ExpiresAt: now.Add(ttl),
		}
		if m.lease.Owner == "" {
			m.lease.Owner = "unknown"
		}
		return m.leaseStatusLocked(), nil
	}
	prev := strings.TrimSpace(req.PrevLeaseID)
	if prev == "" || prev != m.lease.ID {
		return m.leaseStatusLocked(), errors.New("prev lease id mismatch")
	}
	ttl := normalizeLeaseTTL(req.TTL)
	now := time.Now().UTC()
	m.lease = &leaseState{
		ID:        generateRequestID(),
		Owner:     strings.TrimSpace(req.Owner),
		ExpiresAt: now.Add(ttl),
	}
	if m.lease.Owner == "" {
		m.lease.Owner = "unknown"
	}
	return m.leaseStatusLocked(), nil
}

func (m *helperManager) leaseRelease(req LeaseReleaseRequest) (LeaseStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLeaseLocked(time.Now())
	if m.lease == nil {
		return LeaseStatusResponse{Active: false}, nil
	}
	if strings.TrimSpace(req.LeaseID) != "" && req.LeaseID != m.lease.ID {
		return LeaseStatusResponse{
			Active:    true,
			LeaseID:   m.lease.ID,
			Owner:     m.lease.Owner,
			ExpiresAt: ptrTime(m.lease.ExpiresAt),
		}, errors.New("lease id mismatch")
	}
	m.lease = nil
	return LeaseStatusResponse{Active: false}, nil
}

func (m *helperManager) leaseStatus() LeaseStatusResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLeaseLocked(time.Now())
	return m.leaseStatusLocked()
}

func (m *helperManager) checkLease(reqLeaseID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLeaseLocked(time.Now())
	if m.lease == nil {
		return nil
	}
	reqLeaseID = strings.TrimSpace(reqLeaseID)
	if reqLeaseID == "" || reqLeaseID != m.lease.ID {
		return errors.New("active lease requires matching X-Helper-Lease-ID")
	}
	return nil
}

func (m *helperManager) expireLeaseLocked(now time.Time) {
	if m.lease == nil {
		return
	}
	if now.After(m.lease.ExpiresAt) {
		m.lease = nil
	}
}

func normalizeLeaseTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return 60 * time.Second
	}
	if ttl < 5*time.Second {
		return 5 * time.Second
	}
	if ttl > 10*time.Minute {
		return 10 * time.Minute
	}
	return ttl
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func coalesceLeaseID(payloadLeaseID, headerLeaseID string) string {
	if lid := strings.TrimSpace(payloadLeaseID); lid != "" {
		return lid
	}
	return strings.TrimSpace(headerLeaseID)
}

func boolOrDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

func hasBootstrapPayload(pb ProfileBootstrap) bool {
	return strings.TrimSpace(pb.ClientID) != "" ||
		strings.TrimSpace(pb.ServerStaticPubB64) != "" ||
		strings.TrimSpace(pb.Addr) != "" ||
		len(pb.Gateways) > 0
}

func (m *helperManager) ensureLease(owner string, ttl time.Duration, allowTakeover bool) (LeaseStatusResponse, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "bridge"
	}
	cur := m.leaseStatus()
	if !cur.Active {
		return m.leaseAcquire(LeaseAcquireRequest{
			Owner: owner,
			TTL:   ttl,
		})
	}
	if cur.Owner == owner {
		return m.leaseHeartbeat(LeaseHeartbeatRequest{
			LeaseID: cur.LeaseID,
			TTL:     ttl,
		}, "")
	}
	if !allowTakeover {
		return cur, errors.New("active lease belongs to another owner and takeover is disabled")
	}
	if strings.TrimSpace(cur.LeaseID) == "" {
		return cur, errors.New("active lease is missing leaseId")
	}
	return m.leaseTakeover(LeaseTakeoverRequest{
		Owner:       owner,
		TTL:         ttl,
		PrevLeaseID: cur.LeaseID,
	})
}

func (m *helperManager) bridgeStartup(req BridgeStartupRequest) (BridgeStartupResponse, error) {
	waitEnabled := boolOrDefault(req.Wait, true)
	waitState := req.WaitState
	if strings.TrimSpace(string(waitState)) == "" {
		waitState = runtime.StateEstablished
	}
	waitTimeout := req.WaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = 20 * time.Second
	}
	lease, err := m.ensureLease(req.Lease.Owner, req.Lease.TTL, boolOrDefault(req.Lease.Takeover, true))
	if err != nil {
		return BridgeStartupResponse{Lease: lease}, err
	}
	out := BridgeStartupResponse{Lease: lease}
	hasBundle := req.ProfileBundle != nil
	syncProfileFromBootstrap := !hasBundle
	prev, hadPrev := m.snapshotBootstrapState()

	if hasBundle {
		if err := validateBundleBootstrapConsistency(req.ProfileBootstrap, *req.ProfileBundle); err != nil {
			return out, err
		}
		cur, err := m.applyProfileBundle(*req.ProfileBundle)
		if err != nil {
			return out, err
		}
		out.Profile = &cur
	}

	if hasBootstrapPayload(req.ProfileBootstrap) {
		validate, err := m.validateBootstrap(ValidateBootstrapRequest{ProfileBootstrap: req.ProfileBootstrap})
		out.Validate = &validate
		if err != nil {
			if hasBundle {
				_ = m.restoreBootstrapState(prev, hadPrev)
			}
			return out, err
		}
		if !validate.OK {
			if hasBundle {
				_ = m.restoreBootstrapState(prev, hadPrev)
			}
			return out, errors.New("bootstrap preflight failed")
		}
		if err := m.applyBootstrapWithOptions(StartRequest{
			ProfileBootstrap: validate.Normalized,
			DeviceID:         req.DeviceID,
		}, syncProfileFromBootstrap); err != nil {
			if hasBundle {
				_ = m.restoreBootstrapState(prev, hadPrev)
			}
			return out, err
		}
		out.Applied = true
		if err := m.startWithOptions(StartRequest{
			ProfileBootstrap: validate.Normalized,
			DeviceID:         req.DeviceID,
		}, syncProfileFromBootstrap); err != nil {
			_ = m.restoreBootstrapState(prev, hadPrev)
			return out, err
		}
	} else {
		if err := m.startStoredWithOptions(syncProfileFromBootstrap); err != nil {
			if hasBundle {
				_ = m.restoreBootstrapState(prev, hadPrev)
			}
			return out, err
		}
	}
	out.Started = true
	if waitEnabled {
		st, err := m.waitForState(waitState, waitTimeout)
		out.Waited = true
		out.Status = &st
		if err != nil {
			return out, err
		}
	}
	if profile, ok := m.currentProfileBundle(); ok {
		p := profile
		out.Profile = &p
	}
	out.OK = true
	return out, nil
}

type bootstrapSnapshot struct {
	DeviceID     string
	Bootstrap    ProfileBootstrap
	HasBootstrap bool
	LinkID       string
}

func (m *helperManager) snapshotBootstrapState() (bootstrapSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotBootstrapStateLocked()
}

func (m *helperManager) snapshotBootstrapStateLocked() (bootstrapSnapshot, bool) {
	if !m.hasBootstrap {
		return bootstrapSnapshot{}, false
	}
	return bootstrapSnapshot{
		DeviceID:     m.deviceID,
		Bootstrap:    m.bootstrap,
		HasBootstrap: m.hasBootstrap,
		LinkID:       m.linkID,
	}, true
}

func (m *helperManager) restoreBootstrapState(snapshot bootstrapSnapshot, hadSnapshot bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restoreBootstrapStateLocked(snapshot, hadSnapshot)
	return m.saveStateLocked()
}

func (m *helperManager) restoreBootstrapStateLocked(snapshot bootstrapSnapshot, hadSnapshot bool) {
	if !hadSnapshot {
		m.deviceID = ""
		m.bootstrap = ProfileBootstrap{}
		m.hasBootstrap = false
		m.linkID = ""
		_ = m.profiles.Rollback()
		return
	}
	m.deviceID = snapshot.DeviceID
	m.bootstrap = snapshot.Bootstrap
	m.hasBootstrap = snapshot.HasBootstrap
	m.linkID = snapshot.LinkID
	_ = m.applyProfileBundleLocked(snapshot.Bootstrap)
}

func (m *helperManager) syncBootstrapFromCurrentProfileLocked(pb *ProfileBootstrap) {
	if pb == nil || m.profiles == nil {
		return
	}
	cur, ok := m.profiles.Current()
	if !ok || len(cur.Profiles) == 0 {
		return
	}
	p := cur.Profiles[0]
	pb.ProfileID = strings.TrimSpace(p.ID)
	pb.Region = strings.TrimSpace(p.Region)
	pb.SecurityProfile = strings.TrimSpace(p.SecurityProfile)
	pb.Routing = RoutePolicy{
		Strategy:      strings.TrimSpace(p.Routing.Strategy),
		Source:        strings.TrimSpace(p.Routing.Source),
		RulesetRef:    strings.TrimSpace(p.Routing.RulesetRef),
		DefaultAction: strings.TrimSpace(p.Routing.DefaultAction),
		DirectCIDRs:   append([]string(nil), p.Routing.DirectCIDRs...),
		BGP: BGPRoutePolicy{
			Enabled:       boolPtr(p.Routing.BGP.Enabled),
			Neighbor:      strings.TrimSpace(p.Routing.BGP.Neighbor),
			NeighborAS:    p.Routing.BGP.NeighborAS,
			LocalAS:       p.Routing.BGP.LocalAS,
			HoldTimeSec:   p.Routing.BGP.HoldTimeSec,
			KeepaliveSec:  p.Routing.BGP.KeepaliveSec,
			MaxPrefixes:   p.Routing.BGP.MaxPrefixes,
			ImportPolicy:  strings.TrimSpace(p.Routing.BGP.ImportPolicy),
			PrefixSetName: strings.TrimSpace(p.Routing.BGP.PrefixSetName),
		},
	}
	pb.DNS = DNSPolicy{
		Mode:        strings.TrimSpace(p.DNS.Mode),
		TemplateRef: strings.TrimSpace(p.DNS.TemplateRef),
		Bootstrap:   append([]string(nil), p.DNS.Bootstrap...),
	}
	pb.Bridge = BridgePolicy{
		AllowLocalTCPBridge:  boolPtr(p.Bridge.AllowLocalTCPBridge),
		AllowLocalControlAPI: boolPtr(p.Bridge.AllowLocalControlAPI),
	}
}

func validateBundleBootstrapConsistency(pb ProfileBootstrap, bundle runtime.ProfileBundle) error {
	if len(bundle.Profiles) == 0 {
		return errors.New("profile bundle must include at least one profile")
	}
	p := bundle.Profiles[0]
	if want := strings.TrimSpace(pb.ProfileID); want != "" && want != strings.TrimSpace(p.ID) {
		return fmt.Errorf("profileBootstrap.profileID=%q does not match profileBundle.profiles[0].id=%q", want, strings.TrimSpace(p.ID))
	}
	if want := strings.TrimSpace(pb.Region); want != "" && want != strings.TrimSpace(p.Region) {
		return fmt.Errorf("profileBootstrap.region=%q does not match profileBundle.profiles[0].region=%q", want, strings.TrimSpace(p.Region))
	}
	wantSPRaw := strings.TrimSpace(pb.SecurityProfile)
	haveSP := normalizeSecurityProfile(p.SecurityProfile)
	if wantSPRaw != "" && normalizeSecurityProfile(wantSPRaw) != haveSP {
		wantSP := normalizeSecurityProfile(wantSPRaw)
		return fmt.Errorf("profileBootstrap.securityProfile=%q does not match profileBundle.profiles[0].securityProfile=%q", wantSP, haveSP)
	}
	return nil
}

func (m *helperManager) applyProfileBundleLocked(pb ProfileBootstrap) error {
	if m.profiles == nil {
		m.profiles = runtime.NewProfileManager()
	}
	return m.profiles.Apply(profileBundleFromBootstrap(pb))
}

func profileBundleFromBootstrap(pb ProfileBootstrap) runtime.ProfileBundle {
	profileID := strings.TrimSpace(pb.ProfileID)
	if profileID == "" {
		profileID = "bootstrap-default"
	}
	securityProfile := strings.TrimSpace(pb.SecurityProfile)
	if securityProfile == "" {
		securityProfile = runtime.SecurityProfileBalanced
	}
	tunMode := strings.TrimSpace(pb.Tun.ConfigMode)
	lockdown := false
	if securityProfile == runtime.SecurityProfileHighRisk {
		if tunMode == "" {
			tunMode = "full"
		}
		lockdown = true
	}
	return runtime.ProfileBundle{
		APIVersion: helperProfileBundleVersion,
		Version:    helperProfileBundleVersion,
		Profiles: []runtime.ProfileDefinition{
			{
				ID:              profileID,
				Region:          strings.TrimSpace(pb.Region),
				Title:           profileID,
				SecurityProfile: securityProfile,
				Revision:        1,
				Routing: runtime.ProfileRouting{
					Strategy:      strings.TrimSpace(pb.Routing.Strategy),
					Source:        strings.TrimSpace(pb.Routing.Source),
					RulesetRef:    strings.TrimSpace(pb.Routing.RulesetRef),
					DefaultAction: strings.TrimSpace(pb.Routing.DefaultAction),
					DirectCIDRs:   append([]string(nil), pb.Routing.DirectCIDRs...),
					BGP: runtime.ProfileRoutingBGP{
						Enabled:       boolDeref(pb.Routing.BGP.Enabled),
						Neighbor:      strings.TrimSpace(pb.Routing.BGP.Neighbor),
						NeighborAS:    pb.Routing.BGP.NeighborAS,
						LocalAS:       pb.Routing.BGP.LocalAS,
						HoldTimeSec:   pb.Routing.BGP.HoldTimeSec,
						KeepaliveSec:  pb.Routing.BGP.KeepaliveSec,
						MaxPrefixes:   pb.Routing.BGP.MaxPrefixes,
						ImportPolicy:  strings.TrimSpace(pb.Routing.BGP.ImportPolicy),
						PrefixSetName: strings.TrimSpace(pb.Routing.BGP.PrefixSetName),
					},
				},
				DNS: runtime.ProfileDNS{
					Mode:        strings.TrimSpace(pb.DNS.Mode),
					TemplateRef: strings.TrimSpace(pb.DNS.TemplateRef),
					Bootstrap:   append([]string(nil), pb.DNS.Bootstrap...),
				},
				TUN: runtime.ProfileTUN{
					Mode:     tunMode,
					Lockdown: lockdown,
				},
				Bridge: runtime.ProfileBridgePolicy{
					AllowLocalTCPBridge:  boolDeref(pb.Bridge.AllowLocalTCPBridge),
					AllowLocalControlAPI: boolDeref(pb.Bridge.AllowLocalControlAPI),
				},
			},
		},
	}
}

func (m *helperManager) applyProfileBundle(bundle runtime.ProfileBundle) (runtime.ProfileBundle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.profiles == nil {
		m.profiles = runtime.NewProfileManager()
	}
	if err := m.profiles.Apply(bundle); err != nil {
		return runtime.ProfileBundle{}, err
	}

	if len(bundle.Profiles) > 0 {
		p := bundle.Profiles[0]
		m.bootstrap.ProfileID = strings.TrimSpace(p.ID)
		m.bootstrap.Region = strings.TrimSpace(p.Region)
		m.bootstrap.SecurityProfile = strings.TrimSpace(p.SecurityProfile)
		m.bootstrap.Routing = RoutePolicy{
			Strategy:      strings.TrimSpace(p.Routing.Strategy),
			Source:        strings.TrimSpace(p.Routing.Source),
			RulesetRef:    strings.TrimSpace(p.Routing.RulesetRef),
			DefaultAction: strings.TrimSpace(p.Routing.DefaultAction),
			DirectCIDRs:   append([]string(nil), p.Routing.DirectCIDRs...),
			BGP: BGPRoutePolicy{
				Enabled:       boolPtr(p.Routing.BGP.Enabled),
				Neighbor:      strings.TrimSpace(p.Routing.BGP.Neighbor),
				NeighborAS:    p.Routing.BGP.NeighborAS,
				LocalAS:       p.Routing.BGP.LocalAS,
				HoldTimeSec:   p.Routing.BGP.HoldTimeSec,
				KeepaliveSec:  p.Routing.BGP.KeepaliveSec,
				MaxPrefixes:   p.Routing.BGP.MaxPrefixes,
				ImportPolicy:  strings.TrimSpace(p.Routing.BGP.ImportPolicy),
				PrefixSetName: strings.TrimSpace(p.Routing.BGP.PrefixSetName),
			},
		}
		m.bootstrap.DNS = DNSPolicy{
			Mode:        strings.TrimSpace(p.DNS.Mode),
			TemplateRef: strings.TrimSpace(p.DNS.TemplateRef),
			Bootstrap:   append([]string(nil), p.DNS.Bootstrap...),
		}
		m.bootstrap.Bridge = BridgePolicy{
			AllowLocalTCPBridge:  boolPtr(p.Bridge.AllowLocalTCPBridge),
			AllowLocalControlAPI: boolPtr(p.Bridge.AllowLocalControlAPI),
		}
		if err := m.saveStateLocked(); err != nil {
			_ = m.profiles.Rollback()
			return runtime.ProfileBundle{}, err
		}
	}
	current, ok := m.profiles.Current()
	if !ok {
		return runtime.ProfileBundle{}, errors.New("profile bundle apply succeeded but no current bundle is present")
	}
	return current, nil
}

func (m *helperManager) currentProfileBundle() (runtime.ProfileBundle, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.profiles == nil {
		return runtime.ProfileBundle{}, false
	}
	return m.profiles.Current()
}

func (m *helperManager) bridgeShutdown(req BridgeShutdownRequest) (BridgeShutdownResponse, error) {
	bestEffort := boolOrDefault(req.BestEffort, true)
	lease, err := m.ensureLease(req.Lease.Owner, req.Lease.TTL, boolOrDefault(req.Lease.Takeover, true))
	if err != nil {
		return BridgeShutdownResponse{Lease: lease}, err
	}
	out := BridgeShutdownResponse{Lease: lease}
	stopErr := m.stop(req.Timeout)
	if stopErr != nil {
		out.StopError = stopErr.Error()
	} else {
		out.Stopped = true
	}
	releaseOut, releaseErr := m.leaseRelease(LeaseReleaseRequest{LeaseID: lease.LeaseID})
	if releaseErr != nil {
		out.ReleaseError = releaseErr.Error()
	} else if !releaseOut.Active {
		out.Released = true
	}
	if !bestEffort {
		if stopErr != nil {
			return out, stopErr
		}
		if releaseErr != nil {
			return out, releaseErr
		}
	}
	out.OK = stopErr == nil && releaseErr == nil
	if bestEffort {
		return out, nil
	}
	return out, nil
}

func (m *helperManager) bridgeReconcile(req BridgeReconcileRequest) (BridgeReconcileResponse, error) {
	ensureLease := boolOrDefault(req.Lease.Ensure, true)
	var (
		lease LeaseStatusResponse
		err   error
	)
	if ensureLease {
		lease, err = m.ensureLease(req.Lease.Owner, req.Lease.TTL, boolOrDefault(req.Lease.Takeover, true))
		if err != nil {
			return BridgeReconcileResponse{Lease: lease}, err
		}
	} else {
		lease = m.leaseStatus()
	}
	status := m.status()
	health := m.health()
	plan := "running-ok"
	if !status.Running {
		plan = "startup-needed"
	} else if !health.Ready {
		plan = "restart-needed"
	} else if m.gatewayPolicyAutoTuneNeeded(status) {
		plan = "gateway-policy-tune-needed"
	}
	return BridgeReconcileResponse{
		OK:     true,
		Plan:   plan,
		Lease:  lease,
		Status: status,
		Health: health,
	}, nil
}

func (m *helperManager) bridgeAutopilot(req BridgeAutopilotRequest) (BridgeAutopilotResponse, error) {
	maxSteps := req.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 3
	}
	allowRestart := boolOrDefault(req.AllowRestart, true)
	out := BridgeAutopilotResponse{}
	for i := 0; i < maxSteps; i++ {
		reconcile, err := m.bridgeReconcile(req.Reconcile)
		if err != nil {
			return out, err
		}
		step := BridgeAutopilotStep{
			Step: i + 1,
			Plan: reconcile.Plan,
		}
		out.FinalPlan = reconcile.Plan
		switch reconcile.Plan {
		case "running-ok":
			step.Action = "noop"
			out.Steps = append(out.Steps, step)
			out.OK = true
			return out, nil
		case "gateway-policy-tune-needed":
			changed, err := m.applyGatewayPolicyAutoTune()
			if err != nil {
				return out, err
			}
			if !changed {
				step.Action = "policy-tune-noop"
				out.Steps = append(out.Steps, step)
				continue
			}
			if !allowRestart {
				return out, errors.New("bridge.autopilot policy tune requires restart while restart is disabled")
			}
			if _, err := m.bridgeShutdown(req.Shutdown); err != nil {
				return out, err
			}
			if _, err := m.bridgeStartup(req.Startup); err != nil {
				return out, err
			}
			step.Action = "policy-tune-restart"
			out.Steps = append(out.Steps, step)
		case "startup-needed":
			if _, err := m.bridgeStartup(req.Startup); err != nil {
				return out, err
			}
			step.Action = "startup"
			out.Steps = append(out.Steps, step)
		case "restart-needed":
			if !allowRestart {
				return out, errors.New("bridge.autopilot got restart-needed while restart is disabled")
			}
			if _, err := m.bridgeShutdown(req.Shutdown); err != nil {
				return out, err
			}
			if _, err := m.bridgeStartup(req.Startup); err != nil {
				return out, err
			}
			step.Action = "restart"
			out.Steps = append(out.Steps, step)
		default:
			return out, fmt.Errorf("bridge.autopilot got unknown plan %q", reconcile.Plan)
		}
	}
	return out, fmt.Errorf("bridge.autopilot exceeded max steps (%d), last plan=%s", maxSteps, out.FinalPlan)
}

func (m *helperManager) gatewayPolicyAutoTuneNeeded(status StatusResponse) bool {
	if !status.Running || status.LastSnapshot == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasBootstrap || len(m.bootstrap.Gateways) < 2 {
		return false
	}
	if m.bootstrap.GatewayPolicy.AutoSelectEnabled != nil && !*m.bootstrap.GatewayPolicy.AutoSelectEnabled {
		return false
	}
	s := status.LastSnapshot
	if s.GatewaySelections < 8 || s.GatewaySwitches < 4 {
		return false
	}
	switchRate := float64(s.GatewaySwitches) / float64(maxInt(s.GatewaySelections, 1))
	if switchRate < 0.35 {
		return false
	}
	p := m.bootstrap.GatewayPolicy
	// Need tuning if anti-flap keeps are low and policy is still weak.
	if s.GatewayHysteresisKeeps == 0 && p.SwitchHysteresis < 40 {
		return true
	}
	if s.GatewayCooldownSkips == 0 && p.CooldownMin < 10*time.Second {
		return true
	}
	return false
}

func (m *helperManager) applyGatewayPolicyAutoTune() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasBootstrap || len(m.bootstrap.Gateways) < 2 {
		return false, nil
	}
	if m.bootstrap.GatewayPolicy.AutoSelectEnabled != nil && !*m.bootstrap.GatewayPolicy.AutoSelectEnabled {
		return false, nil
	}
	s := m.lastEvent.Snapshot
	if s.GatewaySelections < 8 || s.GatewaySwitches < 4 {
		return false, nil
	}
	switchRate := float64(s.GatewaySwitches) / float64(maxInt(s.GatewaySelections, 1))
	if switchRate < 0.35 {
		return false, nil
	}

	changed := false
	p := m.bootstrap.GatewayPolicy
	if s.GatewayHysteresisKeeps == 0 && p.SwitchHysteresis < 40 {
		next := p.SwitchHysteresis + 10
		if next > 40 {
			next = 40
		}
		if next != p.SwitchHysteresis {
			p.SwitchHysteresis = next
			changed = true
		}
	}
	if s.GatewayCooldownSkips == 0 && p.CooldownMin < 10*time.Second {
		next := p.CooldownMin + 2*time.Second
		if next <= 0 {
			next = 3 * time.Second
		}
		if next > 10*time.Second {
			next = 10 * time.Second
		}
		if next != p.CooldownMin {
			p.CooldownMin = next
			changed = true
		}
	}
	if p.CooldownMax < p.CooldownMin {
		p.CooldownMax = p.CooldownMin
		changed = true
	}
	if p.CooldownMax > 0 && p.CooldownMax < p.CooldownMin*2 {
		p.CooldownMax = p.CooldownMin * 2
		changed = true
	}
	if !changed {
		return false, nil
	}
	m.bootstrap.GatewayPolicy = p
	if err := m.saveStateLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func (m *helperManager) bridgeAutopilotDaemonRun(ctx context.Context, req BridgeAutopilotDaemonRequest, emit func(BridgeAutopilotDaemonTick)) (BridgeAutopilotDaemonResponse, error) {
	interval := req.Interval
	if interval <= 0 {
		interval = 20 * time.Second
	}
	if interval < 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	if req.Duration < 0 {
		return BridgeAutopilotDaemonResponse{}, errors.New("duration must be >= 0")
	}
	continueOnError := boolOrDefault(req.ContinueOnError, true)
	out := BridgeAutopilotDaemonResponse{}
	started := time.Now()
	run := 0
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
		run++
		tick := BridgeAutopilotDaemonTick{
			Run: run,
			At:  time.Now().UTC(),
		}
		ap, err := m.bridgeAutopilot(req.Autopilot)
		if err != nil {
			tick.OK = false
			tick.Error = err.Error()
		} else {
			tick.OK = true
			tick.Autopilot = ap
		}
		out.Ticks = append(out.Ticks, tick)
		out.Runs = run
		m.onDaemonTick(tick)
		if emit != nil {
			emit(tick)
		}
		if err != nil && !continueOnError {
			return out, err
		}
		if req.Duration <= 0 {
			out.OK = err == nil
			return out, nil
		}
		if time.Since(started) >= req.Duration {
			out.OK = true
			if err != nil && !continueOnError {
				out.OK = false
			}
			return out, nil
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(interval)
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-timer.C:
		}
	}
}

func (m *helperManager) bridgeAutopilotDaemon(req BridgeAutopilotDaemonRequest) (BridgeAutopilotDaemonResponse, error) {
	return m.bridgeAutopilotDaemonRun(context.Background(), req, nil)
}

func ptrTime(t time.Time) *time.Time {
	tt := t
	return &tt
}

type persistedState struct {
	DeviceID         string           `json:"deviceID"`
	ProfileBootstrap ProfileBootstrap `json:"profileBootstrap"`
	HasBootstrap     bool             `json:"hasBootstrap"`
}

func (m *helperManager) loadState() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stateFile == "" {
		return nil
	}
	raw, err := os.ReadFile(m.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var st persistedState
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	if st.HasBootstrap {
		if err := normalizeBootstrap(&st.ProfileBootstrap); err != nil {
			return err
		}
		m.bootstrap = st.ProfileBootstrap
		m.deviceID = strings.TrimSpace(st.DeviceID)
		m.hasBootstrap = true
		m.linkID = helperLinkID(m.deviceID, m.bootstrap)
	}
	return nil
}

func (m *helperManager) saveStateLocked() error {
	if m.stateFile == "" {
		return nil
	}
	st := persistedState{
		DeviceID:         m.deviceID,
		ProfileBootstrap: m.bootstrap,
		HasBootstrap:     m.hasBootstrap,
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.stateFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := m.stateFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.stateFile); err != nil {
		return err
	}
	return nil
}

func (m *helperManager) startStored() error {
	return m.startStoredWithOptions(true)
}

func (m *helperManager) startStoredWithOptions(syncProfile bool) error {
	m.mu.Lock()
	bootstrap := m.bootstrap
	deviceID := m.deviceID
	hasBootstrap := m.hasBootstrap
	m.mu.Unlock()
	if !hasBootstrap {
		return errors.New("no bootstrap applied")
	}
	return m.startWithOptions(StartRequest{
		ProfileBootstrap: bootstrap,
		DeviceID:         deviceID,
	}, syncProfile)
}

func (m *helperManager) refresh(timeout time.Duration) error {
	if err := m.stop(timeout); err != nil {
		return err
	}
	return m.startStored()
}

func (m *helperManager) stop(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	m.mu.Lock()
	if !m.running || m.cancel == nil {
		m.mu.Unlock()
		return nil
	}
	cancel := m.cancel
	done := m.done
	m.mu.Unlock()

	cancel()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return errors.New("timeout waiting runtime stop")
	}
}

func (m *helperManager) onEvent(e runtime.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	if e.Snapshot.LinkID != "" {
		m.linkID = e.Snapshot.LinkID
	}
	m.lastEvent = e
	m.lastEventAt = &now
	if m.collector != nil {
		m.collector.OnEvent(e)
	}
	for _, ch := range m.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
}

func (m *helperManager) onDaemonTick(tick BridgeAutopilotDaemonTick) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.daemonSubs {
		select {
		case ch <- tick:
		default:
		}
	}
}

func (m *helperManager) status() StatusResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLeaseLocked(time.Now())
	out := StatusResponse{
		Running:         m.running,
		DeviceID:        m.deviceID,
		ProfileID:       strings.TrimSpace(m.bootstrap.ProfileID),
		SecurityProfile: strings.TrimSpace(m.bootstrap.SecurityProfile),
		StartedAt:       m.startedAt,
		StoppedAt:       m.stoppedAt,
		LastError:       m.lastError,
		Lease:           m.leaseStatusLocked(),
	}
	if m.lastEventAt != nil {
		out.LastEventAt = m.lastEventAt
		out.LastState = m.lastEvent.State
		out.LastClass = m.lastEvent.ErrorClass
		s := m.lastEvent.Snapshot
		out.LastSnapshot = &s
	}
	return out
}

func (m *helperManager) leaseStatusLocked() LeaseStatusResponse {
	if m.lease == nil {
		return LeaseStatusResponse{Active: false}
	}
	return LeaseStatusResponse{
		Active:    true,
		LeaseID:   m.lease.ID,
		Owner:     m.lease.Owner,
		ExpiresAt: ptrTime(m.lease.ExpiresAt),
	}
}

func (m *helperManager) stats() map[string]any {
	st := m.status()
	out := map[string]any{
		"running": st.Running,
		"links":   m.links(),
		"profile": map[string]any{
			"profileID":       st.ProfileID,
			"securityProfile": st.SecurityProfile,
		},
		"rekey": map[string]any{
			"health":           "ok",
			"epoch":            0,
			"initiated":        0,
			"completed":        0,
			"fallbacks":        0,
			"acksRejected":     0,
			"ackSendFailures":  0,
			"initSendFailures": 0,
			"initTimeouts":     0,
		},
	}
	if st.LastSnapshot != nil {
		out["snapshot"] = st.LastSnapshot
		rekeyHealth := "ok"
		if st.LastSnapshot.RekeyInitTimeouts > 0 || st.LastSnapshot.RekeyAcksRejected > 0 || st.LastSnapshot.RekeyFallbacks > 0 {
			rekeyHealth = "degraded"
		}
		if st.LastSnapshot.RekeyInitTimeouts >= 3 || st.LastSnapshot.RekeyFallbacks >= 3 {
			rekeyHealth = "alert"
		}
		out["rekey"] = map[string]any{
			"health":            rekeyHealth,
			"epoch":             st.LastSnapshot.RekeyEpoch,
			"initiated":         st.LastSnapshot.RekeysInitiated,
			"completed":         st.LastSnapshot.RekeysCompleted,
			"fallbacks":         st.LastSnapshot.RekeyFallbacks,
			"acksRejected":      st.LastSnapshot.RekeyAcksRejected,
			"ackSendFailures":   st.LastSnapshot.RekeyAckSendFailures,
			"initSendFailures":  st.LastSnapshot.RekeyInitSendFailures,
			"initTimeouts":      st.LastSnapshot.RekeyInitTimeouts,
			"lastRekeyAt":       st.LastSnapshot.LastRekeyAt,
			"selectedGatewayID": st.LastSnapshot.SelectedGatewayID,
		}
	}
	if st.LastState != "" {
		out["state"] = st.LastState
	}
	if st.LastClass != "" {
		out["errorClass"] = st.LastClass
	}
	return out
}

func (m *helperManager) subscribeEvents() (int, <-chan runtime.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSubID++
	id := m.nextSubID
	ch := make(chan runtime.Event, 32)
	m.subscribers[id] = ch
	return id, ch
}

func (m *helperManager) unsubscribeEvents(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.subscribers[id]
	if !ok {
		return
	}
	delete(m.subscribers, id)
	close(ch)
}

func (m *helperManager) subscribeDaemonTicks() (int, <-chan BridgeAutopilotDaemonTick) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextDaemonID++
	id := m.nextDaemonID
	ch := make(chan BridgeAutopilotDaemonTick, 32)
	m.daemonSubs[id] = ch
	return id, ch
}

func (m *helperManager) unsubscribeDaemonTicks(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.daemonSubs[id]
	if !ok {
		return
	}
	delete(m.daemonSubs, id)
	close(ch)
}

func (m *helperManager) health() HealthResponse {
	link := m.linkStatus()
	readiness := deriveLinkReadiness(link, time.Now().UTC())
	legacyReady := link.Running && link.ObservedState == runtime.StateEstablished
	return HealthResponse{
		Live:          true,
		Ready:         legacyReady,
		Running:       link.Running,
		State:         link.ObservedState,
		LinkReadiness: readiness,
	}
}

func (m *helperManager) links() []LinkStatusResponse {
	link := m.linkStatus()
	if link.LinkID == "" && !link.Running && link.DeviceID == "" {
		return nil
	}
	return []LinkStatusResponse{link}
}

func (m *helperManager) linkStatus() LinkStatusResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.linkStatusLocked()
}

func (m *helperManager) linkByID(linkID string) (LinkStatusResponse, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	link := m.linkStatusLocked()
	if strings.TrimSpace(linkID) == "" || link.LinkID == "" || link.LinkID != strings.TrimSpace(linkID) {
		return LinkStatusResponse{}, false
	}
	return link, true
}

func (m *helperManager) linkReconnect(linkID string, timeout time.Duration) (LinkStatusResponse, error) {
	linkID = strings.TrimSpace(linkID)
	link, ok := m.linkByID(linkID)
	if !ok {
		return LinkStatusResponse{}, errLinkNotFound
	}
	if !link.Running {
		return link, errRuntimeNotRunning
	}
	if err := m.refresh(timeout); err != nil {
		return m.linkStatus(), err
	}
	if updated, ok := m.linkByID(linkID); ok {
		return updated, nil
	}
	return m.linkStatus(), nil
}

func (m *helperManager) linkDrain(linkID string, timeout time.Duration) (LinkStatusResponse, error) {
	linkID = strings.TrimSpace(linkID)
	link, ok := m.linkByID(linkID)
	if !ok {
		return LinkStatusResponse{}, errLinkNotFound
	}
	if !link.Running {
		return link, nil
	}
	if err := m.stop(timeout); err != nil {
		return m.linkStatus(), err
	}
	if updated, ok := m.linkByID(linkID); ok {
		return updated, nil
	}
	return m.linkStatus(), nil
}

func (m *helperManager) linkResume(linkID string) (LinkStatusResponse, error) {
	linkID = strings.TrimSpace(linkID)
	link, ok := m.linkByID(linkID)
	if !ok {
		return LinkStatusResponse{}, errLinkNotFound
	}
	if link.Running {
		return link, nil
	}
	if !m.hasStoredBootstrap() {
		return link, errNoBootstrapConfigured
	}
	if err := m.startStored(); err != nil {
		return m.linkStatus(), err
	}
	if updated, ok := m.linkByID(linkID); ok {
		return updated, nil
	}
	return m.linkStatus(), nil
}

func (m *helperManager) linkGatewaySelect(linkID, gatewayID string, timeout time.Duration) (LinkStatusResponse, error) {
	linkID = strings.TrimSpace(linkID)
	gatewayID = strings.TrimSpace(gatewayID)
	if gatewayID == "" {
		return LinkStatusResponse{}, errGatewayIDRequired
	}
	link, ok := m.linkByID(linkID)
	if !ok {
		return LinkStatusResponse{}, errLinkNotFound
	}
	running, changed, err := m.applyForcedGateway(gatewayID)
	if err != nil {
		return link, err
	}
	if running && changed {
		if err := m.refresh(timeout); err != nil {
			return m.linkStatus(), err
		}
	}
	if updated, ok := m.linkByID(linkID); ok {
		return updated, nil
	}
	return m.linkStatus(), nil
}

func (m *helperManager) hasStoredBootstrap() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hasBootstrap
}

func (m *helperManager) applyForcedGateway(gatewayID string) (running bool, changed bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasBootstrap {
		return false, false, errNoBootstrapConfigured
	}
	if len(m.bootstrap.Gateways) == 0 {
		return false, false, errGatewayPoolUnavailable
	}
	found := false
	for _, gw := range m.bootstrap.Gateways {
		if strings.TrimSpace(gw.GatewayID) == gatewayID {
			found = true
			break
		}
	}
	if !found {
		return false, false, errGatewayNotFoundInPolicy
	}
	changed = strings.TrimSpace(m.bootstrap.GatewayPolicy.ForceGatewayID) != gatewayID
	m.bootstrap.GatewayPolicy.ForceGatewayID = gatewayID
	if err := m.saveStateLocked(); err != nil {
		return false, false, err
	}
	if changed {
		m.lastEvent.Snapshot.SelectedGatewayID = gatewayID
		m.lastEvent.Snapshot.SelectedGatewayAddr = ""
	}
	return m.running, changed, nil
}

func (m *helperManager) linkStatusLocked() LinkStatusResponse {
	m.expireLeaseLocked(time.Now())

	linkID := strings.TrimSpace(m.linkID)
	if linkID == "" && m.hasBootstrap {
		linkID = helperLinkID(m.deviceID, m.bootstrap)
	}

	out := LinkStatusResponse{
		LinkID:          linkID,
		DeviceID:        m.deviceID,
		ProfileID:       strings.TrimSpace(m.bootstrap.ProfileID),
		SecurityProfile: strings.TrimSpace(m.bootstrap.SecurityProfile),
		Role:            runtime.RoleClient,
		DesiredState:    "down",
		ObservedState:   m.lastEvent.State,
		Health:          deriveLinkHealth(m.running, m.lastEvent.State),
		Running:         m.running,
		TunName:         strings.TrimSpace(m.bootstrap.Tun.Name),
	}
	if m.hasBootstrap {
		out.DesiredState = "up"
	}
	if m.lease != nil {
		out.LeaseOwner = m.lease.Owner
		out.LeaseID = m.lease.ID
	}
	if m.lastError != "" {
		out.LastError = m.lastError
	}
	if m.lastEventAt != nil {
		out.LastEventAt = m.lastEventAt
		out.ErrorClass = m.lastEvent.ErrorClass
		s := m.lastEvent.Snapshot
		out.Snapshot = &s
		if !s.LastTransitionAt.IsZero() {
			out.LastTransitionAt = ptrTime(s.LastTransitionAt)
		}
		if s.SessionID != "" {
			out.SessionID = s.SessionID
		}
		if !s.LastHandshakeAt.IsZero() {
			out.LastHandshakeAt = ptrTime(s.LastHandshakeAt)
		}
		if !s.LastRxAt.IsZero() {
			out.LastRxAt = ptrTime(s.LastRxAt)
		}
		if !s.LastTxAt.IsZero() {
			out.LastTxAt = ptrTime(s.LastTxAt)
		}
		out.RxBytes = s.RxBytes
		out.TxBytes = s.TxBytes
		out.GatewayID = s.SelectedGatewayID
		out.GatewayAddr = s.SelectedGatewayAddr
		if out.ObservedState == "" {
			out.ObservedState = s.State
		}
	}
	if out.GatewayID == "" {
		out.GatewayID = strings.TrimSpace(m.bootstrap.GatewayPolicy.ForceGatewayID)
	}
	out.Readiness = deriveLinkReadiness(out, time.Now().UTC())
	return out
}

func deriveLinkReadiness(link LinkStatusResponse, now time.Time) LinkReadiness {
	out := LinkReadiness{
		ContractVersion:    helperLinkReadinessContractVersion,
		Running:            link.Running,
		Established:        link.ObservedState == runtime.StateEstablished,
		SessionPresent:     strings.TrimSpace(link.SessionID) != "",
		HandshakeObserved:  link.LastHandshakeAt != nil && !link.LastHandshakeAt.IsZero(),
		HandshakeMaxAgeSec: int64(helperLinkReadinessHandshakeMaxAge / time.Second),
	}
	if out.HandshakeObserved {
		age := now.Sub(*link.LastHandshakeAt)
		if age < 0 {
			age = 0
		}
		ageSec := int64(age / time.Second)
		out.LastHandshakeAgeSec = &ageSec
		out.HandshakeFresh = age <= helperLinkReadinessHandshakeMaxAge
	}
	reasons := make([]string, 0, 4)
	if !out.Running {
		reasons = append(reasons, "runtime_not_running")
	}
	if !out.Established {
		reasons = append(reasons, "state_not_established")
	}
	if !out.SessionPresent {
		reasons = append(reasons, "session_missing")
	}
	if !out.HandshakeObserved {
		reasons = append(reasons, "handshake_missing")
	}
	out.Ready = out.Running && out.Established && out.SessionPresent && out.HandshakeObserved
	if len(reasons) > 0 {
		out.Reasons = reasons
	}
	return out
}

func deriveLinkHealth(running bool, state runtime.State) string {
	if !running {
		if state == runtime.StateStopped {
			return "failed"
		}
		return "down"
	}
	switch state {
	case runtime.StateEstablished:
		return "healthy"
	case runtime.StateDialing, runtime.StateHandshaking, runtime.StateReconnecting, runtime.StateAccepted, runtime.StateListening, runtime.StateRekeyPending, runtime.StateRekeyOverlap, runtime.StateRekeyCutover:
		return "degraded"
	case runtime.StateStopped:
		return "failed"
	default:
		return "unknown"
	}
}

func evaluateSecuritySignalsBase(req SecurityEvaluateRequest) SecurityEvaluateResponse {
	req.ServerCountry = strings.ToUpper(strings.TrimSpace(req.ServerCountry))
	req.ClientCountry = strings.ToUpper(strings.TrimSpace(req.ClientCountry))
	if req.RepeatOffenseCount < 0 {
		req.RepeatOffenseCount = 0
	}

	reasons := make([]string, 0, 8)
	riskScore := 0
	if req.GeoIPDetected {
		riskScore += 30
		reasons = append(reasons, "geoip_risk_detected")
	}
	if req.DirectDetected {
		riskScore += 40
		reasons = append(reasons, "direct_client_signal_detected")
	}
	if req.IndirectDetected {
		riskScore += 15
		reasons = append(reasons, "indirect_client_signal_detected")
	}
	if req.HostingRisk {
		riskScore += 35
		reasons = append(reasons, "hosting_asn_risk")
	}
	if req.TorRisk {
		riskScore += 40
		reasons = append(reasons, "tor_exit_risk")
	}
	if req.VPNReputationRisk {
		riskScore += 35
		reasons = append(reasons, "vpn_proxy_reputation_risk")
	}
	if req.RepeatOffenseCount > 0 {
		bump := req.RepeatOffenseCount * 5
		if bump > 20 {
			bump = 20
		}
		riskScore += bump
		reasons = append(reasons, "repeat_offense_history")
	}

	// Baseline decision matrix (GeoIP + direct + indirect).
	decision := "not_detected"
	if req.GeoIPDetected && (req.DirectDetected || req.IndirectDetected) {
		decision = "detected"
	} else if req.GeoIPDetected || (req.DirectDetected && req.IndirectDetected) {
		decision = "additional_check"
	}

	// Strong server-side signals can escalate even without client direct signal.
	if req.HostingRisk || req.TorRisk || req.VPNReputationRisk {
		if req.DirectDetected || req.GeoIPDetected || req.IndirectDetected {
			decision = "detected"
		} else {
			decision = "additional_check"
		}
	}

	// Contradiction rule: server abroad + client in RU is a strong VPN indicator.
	if req.GeoIPDetected && req.ClientCountry == "RU" && req.ServerCountry != "" && req.ServerCountry != "RU" {
		decision = "detected"
		reasons = append(reasons, "geoip_client_country_mismatch_ru")
	}

	// False-positive dampeners.
	if req.CorporateWhitelisted && decision == "detected" && !req.TorRisk {
		decision = "additional_check"
		reasons = append(reasons, "corporate_whitelist_dampener")
	}
	if req.ICloudPrivateRelay && decision == "detected" && !req.DirectDetected {
		decision = "additional_check"
		reasons = append(reasons, "icloud_private_relay_dampener")
	}
	if req.RoamingLikely && decision == "detected" && !req.DirectDetected && !req.TorRisk {
		decision = "additional_check"
		reasons = append(reasons, "roaming_dampener")
	}

	protection := []string{"allow"}
	switch decision {
	case "detected":
		protection = []string{"deny_sensitive_actions", "step_up_auth", "manual_review_queue"}
		if req.TorRisk || req.RepeatOffenseCount >= 2 {
			protection = append(protection, "temporary_block")
		}
	case "additional_check":
		protection = []string{"allow_with_limits", "step_up_auth", "scheduled_recheck"}
	}

	if riskScore > 100 {
		riskScore = 100
	}
	return SecurityEvaluateResponse{
		OK:             true,
		Decision:       decision,
		ProtectionPlan: protection,
		RiskScore:      riskScore,
		Reasons:        reasons,
	}
}

func normalizeTenantID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	return strings.ToLower(v)
}

func sourceQualityScore(source string) int {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "ranr":
		return 100
	case "maxmind", "ip2location":
		return 80
	case "internal":
		return 70
	default:
		return 50
	}
}

func (m *helperManager) addSecurityAuditLocked(entry SecurityAuditEntry) {
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	m.security.reputationAudit = append(m.security.reputationAudit, entry)
	if len(m.security.reputationAudit) > 1000 {
		m.security.reputationAudit = m.security.reputationAudit[len(m.security.reputationAudit)-1000:]
	}
}

func (m *helperManager) getPolicyLocked(tenantID string) SecurityTenantPolicy {
	p, ok := m.security.tenantPolicies[tenantID]
	if ok {
		return p
	}
	if def, ok := m.security.tenantPolicies["default"]; ok {
		def.TenantID = tenantID
		return def
	}
	return SecurityTenantPolicy{
		TenantID:            tenantID,
		Profile:             "balanced",
		Enforce:             true,
		HysteresisThreshold: 2,
		HysteresisWindowSec: 3600,
	}
}

func (m *helperManager) pruneSecurityLocked(now time.Time) {
	for nonce, at := range m.security.usedSignalNonces {
		if now.Sub(at) > 10*time.Minute {
			delete(m.security.usedSignalNonces, nonce)
		}
	}
	for key, rec := range m.security.reputationCache {
		if now.After(rec.ExpiresAt) {
			delete(m.security.reputationCache, key)
		}
	}
	rules := m.security.corporateAllowRules[:0]
	for _, r := range m.security.corporateAllowRules {
		if now.Before(r.ExpiresAt) {
			rules = append(rules, r)
		}
	}
	m.security.corporateAllowRules = rules
}

func (m *helperManager) verifySignalSignatureLocked(req SecurityEvaluateRequest) (string, error) {
	if len(m.security.signalHMACKey) == 0 {
		return "disabled", nil
	}
	sig := strings.TrimSpace(req.SignalSignature)
	nonce := strings.TrimSpace(req.SignalNonce)
	if sig == "" || nonce == "" || req.SignalTimestamp == 0 {
		return "", errors.New("signal signature fields are required")
	}
	ts := time.Unix(req.SignalTimestamp, 0).UTC()
	now := time.Now().UTC()
	if now.Sub(ts) > 5*time.Minute || ts.Sub(now) > 2*time.Minute {
		return "", errors.New("signal timestamp out of allowed window")
	}
	if _, exists := m.security.usedSignalNonces[nonce]; exists {
		return "", errors.New("signal nonce replay detected")
	}
	base := fmt.Sprintf("%s|%s|%s|%t|%t|%t|%d",
		strings.TrimSpace(req.DeviceID),
		normalizeTenantID(req.TenantID),
		nonce,
		req.DirectDetected,
		req.IndirectDetected,
		req.GeoIPDetected,
		req.SignalTimestamp,
	)
	mac := hmac.New(sha256.New, m.security.signalHMACKey)
	_, _ = mac.Write([]byte(base))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(sig)), []byte(strings.ToLower(want))) {
		return "", errors.New("signal signature mismatch")
	}
	m.security.usedSignalNonces[nonce] = now
	return "hmac_sha256", nil
}

func (m *helperManager) matchCorporateAllowLocked(tenantID, asn, ip string) bool {
	asn = strings.TrimSpace(asn)
	parsedIP := net.ParseIP(strings.TrimSpace(ip))
	now := time.Now().UTC()
	for _, r := range m.security.corporateAllowRules {
		rTenant := normalizeTenantID(r.TenantID)
		if rTenant != tenantID && rTenant != "default" {
			continue
		}
		if now.After(r.ExpiresAt) {
			continue
		}
		if asn != "" && strings.EqualFold(strings.TrimSpace(r.ASN), asn) {
			return true
		}
		if parsedIP != nil && strings.TrimSpace(r.CIDR) != "" {
			if _, cidr, err := net.ParseCIDR(strings.TrimSpace(r.CIDR)); err == nil && cidr.Contains(parsedIP) {
				return true
			}
		}
	}
	return false
}

func (m *helperManager) reputationRiskLocked(tenantID, ip string) (bool, []string) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return false, nil
	}
	key := tenantID + "|" + ip
	rec, ok := m.security.reputationCache[key]
	if !ok {
		key = "default|" + ip
		rec, ok = m.security.reputationCache[key]
		if !ok {
			return false, nil
		}
	}
	if time.Now().UTC().After(rec.ExpiresAt) {
		delete(m.security.reputationCache, key)
		return false, nil
	}
	if rec.Confidence >= 60 || rec.SourceScore >= 80 {
		return true, []string{"reputation_cache_hit:" + rec.RiskType + ":" + rec.Source}
	}
	return false, nil
}

func (m *helperManager) recordDecisionLocked(tenantID, subject, decision string, now time.Time) int {
	key := tenantID + "|" + subject
	windowed := m.security.decisionHistory[key][:0]
	for _, item := range m.security.decisionHistory[key] {
		if now.Sub(item.At) <= 24*time.Hour {
			windowed = append(windowed, item)
		}
	}
	windowed = append(windowed, securityDecisionRecord{At: now, Decision: decision})
	m.security.decisionHistory[key] = windowed
	count := 0
	for _, item := range windowed {
		if item.Decision == "detected" {
			count++
		}
	}
	return count
}

func (m *helperManager) evaluateSecurity(req SecurityEvaluateRequest) (SecurityEvaluateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	m.pruneSecurityLocked(now)

	tenantID := normalizeTenantID(req.TenantID)
	req.TenantID = tenantID
	if strings.TrimSpace(req.DeviceID) == "" {
		req.DeviceID = "unknown"
	}
	policy := m.getPolicyLocked(tenantID)
	provenance, err := m.verifySignalSignatureLocked(req)
	if err != nil {
		m.addSecurityAuditLocked(SecurityAuditEntry{
			At:       now,
			TenantID: tenantID,
			DeviceID: req.DeviceID,
			Event:    "signature_rejected",
			Detail:   err.Error(),
		})
		return SecurityEvaluateResponse{}, err
	}

	whitelistedByRule := m.matchCorporateAllowLocked(tenantID, req.ASN, req.ClientIP)
	if whitelistedByRule {
		req.CorporateWhitelisted = true
	}
	reputationRisk, repReasons := m.reputationRiskLocked(tenantID, firstNonEmpty(strings.TrimSpace(req.ReputationIP), strings.TrimSpace(req.ClientIP)))
	if reputationRisk {
		req.VPNReputationRisk = true
	}

	resp := evaluateSecuritySignalsBase(req)
	resp.PolicyProfile = policy.Profile
	resp.Provenance = provenance
	resp.Reasons = append(resp.Reasons, repReasons...)

	// Profile tuning.
	switch policy.Profile {
	case "strict":
		resp.RiskScore += 10
		if resp.Decision == "additional_check" && req.GeoIPDetected && (req.HostingRisk || req.VPNReputationRisk) {
			resp.Decision = "detected"
		}
	case "permissive":
		resp.RiskScore -= 10
		if resp.Decision == "detected" && req.CorporateWhitelisted && !req.TorRisk {
			resp.Decision = "additional_check"
		}
	}
	if resp.RiskScore < 0 {
		resp.RiskScore = 0
	}
	if resp.RiskScore > 100 {
		resp.RiskScore = 100
	}

	// Hysteresis before hard block.
	subject := strings.TrimSpace(req.DeviceID)
	if subject == "" {
		subject = strings.TrimSpace(req.ClientIP)
	}
	if subject == "" {
		subject = "unknown"
	}
	detectedCount := m.recordDecisionLocked(tenantID, subject, resp.Decision, now)
	threshold := policy.HysteresisThreshold
	if threshold <= 0 {
		threshold = 2
	}
	if policy.Enforce && resp.Decision == "detected" && detectedCount >= threshold {
		resp.HardBlock = true
		found := false
		for _, p := range resp.ProtectionPlan {
			if p == "temporary_block" {
				found = true
				break
			}
		}
		if !found {
			resp.ProtectionPlan = append(resp.ProtectionPlan, "temporary_block")
		}
		resp.Reasons = append(resp.Reasons, "hysteresis_threshold_reached")
	}

	m.addSecurityAuditLocked(SecurityAuditEntry{
		At:       now,
		TenantID: tenantID,
		DeviceID: req.DeviceID,
		Event:    "security_evaluate",
		Detail:   fmt.Sprintf("decision=%s risk=%d hardBlock=%t", resp.Decision, resp.RiskScore, resp.HardBlock),
	})
	return resp, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (m *helperManager) upsertSecurityReputation(req SecurityReputationUpsertRequest) (SecurityReputationUpsertResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneSecurityLocked(time.Now().UTC())
	tenantID := normalizeTenantID(req.TenantID)
	ip := strings.TrimSpace(req.IP)
	if ip == "" {
		return SecurityReputationUpsertResponse{}, errors.New("ip is required")
	}
	if net.ParseIP(ip) == nil {
		return SecurityReputationUpsertResponse{}, errors.New("ip must be a valid IPv4/IPv6 address")
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		return SecurityReputationUpsertResponse{}, errors.New("source is required")
	}
	riskType := strings.TrimSpace(req.RiskType)
	if riskType == "" {
		riskType = "vpn"
	}
	conf := req.Confidence
	if conf < 0 {
		conf = 0
	}
	if conf > 100 {
		conf = 100
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if ttl > 7*24*time.Hour {
		ttl = 7 * 24 * time.Hour
	}
	observedAt := time.Now().UTC()
	if req.ObservedAtUnix > 0 {
		observedAt = time.Unix(req.ObservedAtUnix, 0).UTC()
	}
	entry := SecurityReputationEntry{
		TenantID:    tenantID,
		IP:          ip,
		Source:      source,
		RiskType:    riskType,
		Confidence:  conf,
		ObservedAt:  observedAt,
		ExpiresAt:   time.Now().UTC().Add(ttl),
		SourceScore: sourceQualityScore(source),
	}
	key := tenantID + "|" + ip
	m.security.reputationCache[key] = entry
	m.addSecurityAuditLocked(SecurityAuditEntry{
		TenantID: tenantID,
		Event:    "reputation_upsert",
		Detail:   fmt.Sprintf("ip=%s source=%s risk=%s confidence=%d", ip, source, riskType, conf),
	})
	return SecurityReputationUpsertResponse{OK: true, Entry: entry}, nil
}

func (m *helperManager) upsertTenantPolicy(req SecurityTenantPolicy) SecurityTenantPolicyResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	tenantID := normalizeTenantID(req.TenantID)
	p := SecurityTenantPolicy{
		TenantID:            tenantID,
		Profile:             strings.ToLower(strings.TrimSpace(req.Profile)),
		Enforce:             req.Enforce,
		HysteresisThreshold: req.HysteresisThreshold,
		HysteresisWindowSec: req.HysteresisWindowSec,
	}
	if p.Profile == "" {
		p.Profile = "balanced"
	}
	if p.Profile != "strict" && p.Profile != "balanced" && p.Profile != "permissive" {
		p.Profile = "balanced"
	}
	if p.HysteresisThreshold <= 0 {
		p.HysteresisThreshold = 2
	}
	if p.HysteresisWindowSec <= 0 {
		p.HysteresisWindowSec = 3600
	}
	m.security.tenantPolicies[tenantID] = p
	m.addSecurityAuditLocked(SecurityAuditEntry{
		TenantID: tenantID,
		Event:    "policy_upsert",
		Detail:   fmt.Sprintf("profile=%s enforce=%t threshold=%d", p.Profile, p.Enforce, p.HysteresisThreshold),
	})
	return SecurityTenantPolicyResponse{OK: true, Policy: p}
}

func (m *helperManager) getTenantPolicy(tenantID string) SecurityTenantPolicyResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	tid := normalizeTenantID(tenantID)
	return SecurityTenantPolicyResponse{OK: true, Policy: m.getPolicyLocked(tid)}
}

func (m *helperManager) upsertCorporateAllowRule(req CorporateAllowRuleUpsertRequest) (CorporateAllowRuleUpsertResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tenantID := normalizeTenantID(req.TenantID)
	asn := strings.TrimSpace(req.ASN)
	cidrRaw := strings.TrimSpace(req.CIDR)
	if asn == "" && cidrRaw == "" {
		return CorporateAllowRuleUpsertResponse{}, errors.New("asn or cidr is required")
	}
	if cidrRaw != "" {
		if _, _, err := net.ParseCIDR(cidrRaw); err != nil {
			return CorporateAllowRuleUpsertResponse{}, errors.New("invalid cidr")
		}
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if ttl > 30*24*time.Hour {
		ttl = 30 * 24 * time.Hour
	}
	now := time.Now().UTC()
	rule := CorporateAllowRule{
		TenantID:  tenantID,
		ASN:       asn,
		CIDR:      cidrRaw,
		Reason:    strings.TrimSpace(req.Reason),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	m.security.corporateAllowRules = append(m.security.corporateAllowRules, rule)
	m.addSecurityAuditLocked(SecurityAuditEntry{
		TenantID: tenantID,
		Event:    "corporate_allow_upsert",
		Detail:   fmt.Sprintf("asn=%s cidr=%s ttl=%s", asn, cidrRaw, ttl),
	})
	return CorporateAllowRuleUpsertResponse{OK: true, Rule: rule}, nil
}

func (m *helperManager) listSecurityAudit(limit int) []SecurityAuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	total := len(m.security.reputationAudit)
	if total <= limit {
		out := make([]SecurityAuditEntry, total)
		copy(out, m.security.reputationAudit)
		return out
	}
	out := make([]SecurityAuditEntry, limit)
	copy(out, m.security.reputationAudit[total-limit:])
	return out
}

func (m *helperManager) securitySignalIngest(req SecuritySignalIngestRequest) (SecuritySignalIngestResponse, error) {
	evaluate := true
	if req.Evaluate != nil {
		evaluate = *req.Evaluate
	}
	if req.RequireSignature != nil && *req.RequireSignature && strings.TrimSpace(req.Signal.SignalSignature) == "" {
		return SecuritySignalIngestResponse{}, errors.New("signal signature is required by request")
	}
	resp := SecuritySignalIngestResponse{
		OK:         true,
		IngestedAt: time.Now().UTC(),
		TenantID:   normalizeTenantID(req.Signal.TenantID),
		DeviceID:   strings.TrimSpace(req.Signal.DeviceID),
		Evaluated:  evaluate,
	}
	if evaluate {
		out, err := m.evaluateSecurity(req.Signal)
		if err != nil {
			return SecuritySignalIngestResponse{}, err
		}
		resp.Result = &out
	}
	m.mu.Lock()
	m.security.ingestAudit = append(m.security.ingestAudit, resp)
	if len(m.security.ingestAudit) > 500 {
		m.security.ingestAudit = m.security.ingestAudit[len(m.security.ingestAudit)-500:]
	}
	m.mu.Unlock()
	return resp, nil
}

func (m *helperManager) listSecurityIngest(limit int) []SecuritySignalIngestResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	total := len(m.security.ingestAudit)
	if total <= limit {
		out := make([]SecuritySignalIngestResponse, total)
		copy(out, m.security.ingestAudit)
		return out
	}
	out := make([]SecuritySignalIngestResponse, limit)
	copy(out, m.security.ingestAudit[total-limit:])
	return out
}

func (m *helperManager) securityPolicyRollout(req SecurityPolicyRolloutRequest) SecurityPolicyRolloutResponse {
	defaultProfile := strings.ToLower(strings.TrimSpace(req.DefaultProfile))
	if defaultProfile == "" {
		defaultProfile = "balanced"
	}
	if defaultProfile != "strict" && defaultProfile != "balanced" && defaultProfile != "permissive" {
		defaultProfile = "balanced"
	}
	def := m.upsertTenantPolicy(SecurityTenantPolicy{
		TenantID:            "default",
		Profile:             defaultProfile,
		Enforce:             true,
		HysteresisThreshold: 2,
		HysteresisWindowSec: 3600,
	})
	_ = def
	strictTenants := make([]string, 0, len(req.StrictTenants))
	seen := make(map[string]struct{}, len(req.StrictTenants))
	for _, raw := range req.StrictTenants {
		tid := normalizeTenantID(raw)
		if tid == "default" {
			continue
		}
		if _, ok := seen[tid]; ok {
			continue
		}
		seen[tid] = struct{}{}
		strictTenants = append(strictTenants, tid)
		m.upsertTenantPolicy(SecurityTenantPolicy{
			TenantID:            tid,
			Profile:             "strict",
			Enforce:             true,
			HysteresisThreshold: 2,
			HysteresisWindowSec: 3600,
		})
	}
	return SecurityPolicyRolloutResponse{
		OK:             true,
		DefaultProfile: defaultProfile,
		StrictTenants:  strictTenants,
	}
}

func (m *helperManager) waitForState(target runtime.State, timeout time.Duration) (StatusResponse, error) {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	cur := m.status()
	if cur.LastState == target {
		return cur, nil
	}
	subID, ch := m.subscribeEvents()
	defer m.unsubscribeEvents(subID)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case ev := <-ch:
			if ev.State == target {
				return m.status(), nil
			}
		case <-timer.C:
			return m.status(), errors.New("timeout waiting for target state")
		}
	}
}

func (m *helperManager) collectSupportBundle(req CollectSupportBundleRequest) ([]byte, error) {
	m.mu.Lock()
	bootstrap := m.bootstrap
	collector := m.collector
	m.mu.Unlock()

	cfg := req.Support
	if cfg.RuntimeVersion == "" {
		cfg.RuntimeVersion = bootstrap.Support.RuntimeVersion
	}
	if cfg.BuildInfo == "" {
		cfg.BuildInfo = bootstrap.Support.BuildInfo
	}
	if cfg.Ring == "" {
		cfg.Ring = bootstrap.Support.Ring
	}
	if cfg.HostID == "" {
		cfg.HostID = bootstrap.Support.HostID
	}
	if cfg.HostID == "" {
		if h, err := os.Hostname(); err == nil {
			cfg.HostID = h
		}
	}
	if cfg.SigningKeyFile == "" {
		cfg.SigningKeyFile = bootstrap.Support.SigningKeyFile
	}
	if cfg.SigningKeyID == "" {
		cfg.SigningKeyID = bootstrap.Support.SigningKeyID
	}
	var signingKey []byte
	if cfg.SigningKeyFile != "" {
		raw, err := os.ReadFile(cfg.SigningKeyFile)
		if err != nil {
			return nil, err
		}
		signingKey = []byte(strings.TrimSpace(string(raw)))
	}
	return collector.ExportEnvelopeJSONWithConfig(runtime.SupportBundleConfig{
		Role:           runtime.RoleClient,
		RuntimeVersion: cfg.RuntimeVersion,
		BuildInfo:      cfg.BuildInfo,
		Ring:           cfg.Ring,
		HostID:         cfg.HostID,
	}, runtime.SigningOptions{
		Key:   signingKey,
		KeyID: cfg.SigningKeyID,
	})
}

func (m *helperManager) runRuntimeClient(ctx context.Context, pb ProfileBootstrap, onEvent func(runtime.Event)) error {
	clientID, err := parseHex16(pb.ClientID)
	if err != nil {
		return err
	}
	var expectedServerID *[16]byte
	if strings.TrimSpace(pb.ServerID) != "" {
		sid, err := parseHex16(pb.ServerID)
		if err != nil {
			return err
		}
		expectedServerID = &sid
	}
	serverPub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pb.ServerStaticPubB64))
	if err != nil {
		return err
	}
	tunOpts := tun.OpenOptions{
		Name:           pb.Tun.Name,
		MTU:            pb.Tun.MTU,
		SkipUp:         pb.Tun.SkipUp,
		Addresses:      pb.Tun.Addresses,
		Routes:         pb.Tun.Routes,
		ConfigMode:     pb.Tun.ConfigMode,
		CleanupOnClose: pb.Tun.CleanupOnClose,
	}
	if err := tun.Preflight(tunOpts); err != nil {
		return err
	}
	endpoints, err := bootstrapGatewayEndpoints(pb)
	if err != nil {
		return err
	}
	selectorOpts := runtime.GatewaySelectorOptions{
		AutoSelectEnabled:  pb.GatewayPolicy.AutoSelectEnabled,
		ForceGatewayID:     strings.TrimSpace(pb.GatewayPolicy.ForceGatewayID),
		StickyDuration:     pb.GatewayPolicy.StickyDuration,
		FailureCooldownMin: pb.GatewayPolicy.CooldownMin,
		FailureCooldownMax: pb.GatewayPolicy.CooldownMax,
		SwitchHysteresis:   pb.GatewayPolicy.SwitchHysteresis,
	}
	failoverDialer, err := runtime.NewGatewayFailoverDialer(endpoints, func(ctx context.Context, endpoint runtime.GatewayEndpoint) (transport.Stream, error) {
		serverName := strings.TrimSpace(endpoint.ServerName)
		if serverName == "" {
			serverName = pb.ServerName
		}
		if strings.TrimSpace(serverName) == "" {
			serverName = "localhost"
		}
		dialer := &tlsstream.Dialer{
			TLSConfig: tlsstream.ClientConfig(serverName, pb.Insecure),
			Timeout:   pb.ConnectTimeout,
		}
		return dialer.Dial(ctx, endpoint.Addr)
	}, selectorOpts)
	if err != nil {
		return err
	}
	linkID := helperLinkID(m.deviceID, pb)
	rekeyEnabled := helperRekeyEnabled(pb.RekeyPolicy)
	return runtime.RunClient(
		ctx,
		func(_ context.Context) (tun.Device, error) {
			return tun.Open(tunOpts)
		},
		func(ctx context.Context) (transport.Stream, error) {
			stream, err := failoverDialer.Dial(ctx)
			failoverDialer.ReportDialResult(err)
			return stream, err
		},
		func(stream transport.Stream) (*core.Session, error) {
			sess, err := core.ClientHandshakeWithConfig(stream, clientID, serverPub, core.ClientHandshakeOptions{
				Plain:            pb.Plain,
				ExpectedServerID: expectedServerID,
			})
			if err != nil {
				return nil, err
			}
			return sess, nil
		},
		runtime.ClientOptions{
			MaxRetries:          pb.MaxRetries,
			RetryPolicy:         runtime.NewTransportRetryPolicy(),
			LinkID:              linkID,
			RekeyEnabled:        rekeyEnabled,
			RekeyAckRetries:     pb.RekeyPolicy.AckRetries,
			RekeyAckRetryDelay:  pb.RekeyPolicy.AckRetryDelay,
			RekeyInitInterval:   pb.RekeyPolicy.InitInterval,
			RekeyInitAckTimeout: pb.RekeyPolicy.InitAckTimeout,
			RekeyInitRetries:    pb.RekeyPolicy.InitRetries,
			RekeyInitRetryDelay: pb.RekeyPolicy.InitRetryDelay,
			RekeyInitOverlap:    pb.RekeyPolicy.InitOverlap,
			RunEngine: func(runCtx context.Context, dev tun.Device, stream transport.Stream, sess *core.Session, opts engine.Options) error {
				opts.TrafficObserver = func(sample engine.TrafficSample) {
					m.mu.Lock()
					if m.lastEvent.Snapshot.LinkID == "" {
						m.lastEvent.Snapshot.LinkID = linkID
					}
					switch sample.Direction {
					case engine.TrafficDirectionTx:
						m.lastEvent.Snapshot.LastTxAt = sample.At.UTC()
						m.lastEvent.Snapshot.TxBytes += uint64(sample.Bytes)
					case engine.TrafficDirectionRx:
						m.lastEvent.Snapshot.LastRxAt = sample.At.UTC()
						m.lastEvent.Snapshot.RxBytes += uint64(sample.Bytes)
					}
					m.mu.Unlock()
				}
				return engine.Run(runCtx, dev, stream, sess, opts)
			},
			OnEvent: func(e runtime.Event) {
				e.Snapshot.LinkID = linkID
				if e.State == runtime.StateEstablished && e.Snapshot.LastHandshakeAt.IsZero() {
					e.Snapshot.LastHandshakeAt = time.Now().UTC()
				}
				sel := failoverDialer.Snapshot()
				e.Snapshot.SelectedGatewayID = sel.SelectedGatewayID
				e.Snapshot.SelectedGatewayAddr = sel.SelectedGatewayAddr
				e.Snapshot.GatewaySelections = sel.GatewaySelections
				e.Snapshot.GatewaySwitches = sel.GatewaySwitches
				e.Snapshot.GatewayCooldownSkips = sel.GatewayCooldownSkips
				e.Snapshot.GatewayHysteresisKeeps = sel.GatewayHysteresisKeeps
				e.Snapshot.GatewayAutoSelect = sel.GatewayAutoSelect
				onEvent(e)
			},
		},
	)
}

func helperRekeyEnabled(policy RekeyPolicy) bool {
	if policy.Enabled != nil {
		return *policy.Enabled
	}
	return policy.InitInterval > 0
}

func helperLinkID(deviceID string, pb ProfileBootstrap) string {
	parts := []string{"client"}
	if v := strings.TrimSpace(deviceID); v != "" {
		parts = append(parts, v)
	} else if v := strings.TrimSpace(pb.ClientID); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(pb.Tun.Name); v != "" {
		parts = append(parts, v)
	} else {
		parts = append(parts, "default")
	}
	return strings.Join(parts, ":")
}

func bootstrapGatewayEndpoints(pb ProfileBootstrap) ([]runtime.GatewayEndpoint, error) {
	if len(pb.Gateways) == 0 {
		addr := strings.TrimSpace(pb.Addr)
		if addr == "" {
			return nil, errors.New("profileBootstrap.addr is required when gateways are not provided")
		}
		return []runtime.GatewayEndpoint{
			{
				GatewayID:  "legacy",
				Addr:       addr,
				ServerName: strings.TrimSpace(pb.ServerName),
				Health:     "healthy",
			},
		}, nil
	}
	out := make([]runtime.GatewayEndpoint, 0, len(pb.Gateways))
	for i, gw := range pb.Gateways {
		gatewayID := strings.TrimSpace(gw.GatewayID)
		if gatewayID == "" {
			gatewayID = fmt.Sprintf("gw-%d", i+1)
		}
		for _, ep := range gw.Endpoints {
			addr := strings.TrimSpace(ep.Addr)
			if addr == "" {
				continue
			}
			out = append(out, runtime.GatewayEndpoint{
				GatewayID:  gatewayID,
				Region:     strings.TrimSpace(gw.Region),
				Addr:       addr,
				ServerName: strings.TrimSpace(ep.ServerName),
				Health:     strings.TrimSpace(gw.Health),
				Priority:   gw.Hints.Priority,
				LoadScore:  gw.Hints.LoadScore,
				RTTScore:   gw.Hints.RTTScore,
			})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("profileBootstrap.gateways has no valid endpoints")
	}
	return out, nil
}

func main() {
	listenAddr := flag.String("listen", "127.0.0.1:19090", "helper API listen address")
	unixSocket := flag.String("unix-socket", "", "optional unix domain socket path for helper API (preferred for local desktop integration)")
	stateFile := flag.String("state-file", "", "optional path to persisted bootstrap state JSON")
	authTokenFile := flag.String("auth-token-file", "", "optional path to helper API auth token (Bearer or X-Helper-Token)")
	flag.Parse()

	authToken, err := readTokenFile(*authTokenFile)
	if err != nil {
		log.Fatalf("read auth token file: %v", err)
	}
	if strings.TrimSpace(*unixSocket) == "" && strings.TrimSpace(authToken) == "" && !allowTCPHelperNoAuthForTests() {
		log.Fatalf("helper tcp mode requires auth token file (set --auth-token-file) unless ALLOW_HELPER_TCP_NOAUTH_FOR_TESTS=1")
	}
	manager := newHelperManager(*stateFile)
	mux := newHelperMux(manager, authToken)

	srv := &http.Server{
		Addr:    *listenAddr,
		Handler: mux,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		_ = manager.stop(3 * time.Second)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	var (
		ln        net.Listener
		listenOn  string
		listenErr error
	)
	if strings.TrimSpace(*unixSocket) != "" {
		ln, listenOn, listenErr = listenUnixSocket(*unixSocket)
		if listenErr != nil {
			log.Fatalf("helper listen unix socket failed: %v", listenErr)
		}
		defer func() {
			_ = ln.Close()
			_ = os.Remove(listenOn)
		}()
	} else {
		ln, listenErr = net.Listen("tcp", *listenAddr)
		if listenErr != nil {
			log.Fatalf("helper listen tcp failed: %v", listenErr)
		}
		defer ln.Close()
		listenOn = ln.Addr().String()
	}

	if authToken != "" {
		log.Printf("runtime-helper auth enabled")
	}
	log.Printf("runtime-helper listening on %s", listenOn)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("helper server failed: %v", err)
	}
}

func listenUnixSocket(path string) (net.Listener, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, "", errors.New("empty unix socket path")
	}
	clean := filepath.Clean(path)
	if st, err := os.Stat(clean); err == nil {
		if (st.Mode() & os.ModeSocket) == 0 {
			return nil, "", errors.New("unix socket path exists and is not a socket")
		}
		if err := os.Remove(clean); err != nil {
			return nil, "", err
		}
	} else if !os.IsNotExist(err) {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
		return nil, "", err
	}
	ln, err := net.Listen("unix", clean)
	if err != nil {
		return nil, "", err
	}
	if err := os.Chmod(clean, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(clean)
		return nil, "", err
	}
	return ln, clean, nil
}

func newHelperMux(manager *helperManager, authToken string) *http.ServeMux {
	mux := http.NewServeMux()
	protected := requireAuth(authToken)
	leased := requireLease(manager)
	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		return withRequestID(withIdempotency(manager, leased(protected(h))))
	}
	wrapNoLease := func(h http.HandlerFunc) http.HandlerFunc {
		return withRequestID(withIdempotency(manager, protected(h)))
	}
	wrapNoLeaseNoIdem := func(h http.HandlerFunc) http.HandlerFunc {
		return withRequestID(protected(h))
	}

	startHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req StartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		if err := manager.start(req); err != nil {
			writeAPIError(w, r, http.StatusConflict, "runtime_conflict", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	}
	applyBootstrapHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req StartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		if err := manager.applyBootstrap(req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_bootstrap", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
	profileApplyHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req ProfileApplyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		if len(req.Bundle.Profiles) == 0 {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_profile_bundle", "bundle.profiles must contain at least one profile")
			return
		}
		cur, err := manager.applyProfileBundle(req.Bundle)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_profile_bundle", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, ProfileApplyResponse{
			OK:      true,
			Current: cur,
		})
	}
	profileCurrentHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		cur, ok := manager.currentProfileBundle()
		if !ok {
			writeAPIError(w, r, http.StatusNotFound, "profile_not_configured", "no profile bundle is currently configured")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"current": cur,
		})
	}
	validateBootstrapHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req ValidateBootstrapRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		resp, err := manager.validateBootstrap(req)
		if err != nil {
			if resp.Normalized.ClientID == "" {
				writeAPIError(w, r, http.StatusBadRequest, "invalid_bootstrap", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
	leaseAcquireHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req LeaseAcquireRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		out, err := manager.leaseAcquire(req)
		if err != nil {
			writeAPIError(w, r, http.StatusConflict, "lease_conflict", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	leaseRenewHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req LeaseRenewRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		out, err := manager.leaseRenew(req)
		if err != nil {
			writeAPIError(w, r, http.StatusConflict, "lease_renew_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	leaseHeartbeatHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req LeaseHeartbeatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		out, err := manager.leaseHeartbeat(req, r.Header.Get("X-Helper-Lease-ID"))
		if err != nil {
			writeAPIError(w, r, http.StatusConflict, "lease_heartbeat_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	leaseTakeoverHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req LeaseTakeoverRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		out, err := manager.leaseTakeover(req)
		if err != nil {
			writeAPIError(w, r, http.StatusConflict, "lease_takeover_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	leaseReleaseHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req LeaseReleaseRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		out, err := manager.leaseRelease(req)
		if err != nil {
			writeAPIError(w, r, http.StatusConflict, "lease_release_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	leaseStatusHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, manager.leaseStatus())
	}
	startTunnelHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req StartRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		var err error
		if req.ProfileBootstrap.ClientID != "" || req.ProfileBootstrap.ServerStaticPubB64 != "" {
			err = manager.start(req)
		} else {
			err = manager.startStored()
		}
		if err != nil {
			writeAPIError(w, r, http.StatusConflict, "runtime_conflict", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	}
	stopHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req StopRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := manager.stop(req.Timeout); err != nil {
			writeAPIError(w, r, http.StatusGatewayTimeout, "runtime_stop_timeout", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
	refreshHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req StopRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := manager.refresh(req.Timeout); err != nil {
			writeAPIError(w, r, http.StatusConflict, "runtime_refresh_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	}
	statusHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, manager.status())
	}
	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, manager.health())
	}
	statsHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, manager.stats())
	}
	linksHandler := func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/helper/links")
		if path == r.URL.Path {
			path = strings.TrimPrefix(r.URL.Path, "/links")
		}
		path = strings.TrimSpace(path)
		if r.Method == http.MethodGet {
			if path == "" || path == "/" {
				writeJSON(w, http.StatusOK, map[string]any{"links": manager.links()})
				return
			}
			linkID := strings.TrimPrefix(path, "/")
			link, ok := manager.linkByID(linkID)
			if !ok {
				writeAPIError(w, r, http.StatusNotFound, "link_not_found", "link not found")
				return
			}
			writeJSON(w, http.StatusOK, link)
			return
		}
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		if len(parts) != 2 {
			writeAPIError(w, r, http.StatusNotFound, "endpoint_not_found", "endpoint not found")
			return
		}
		linkID := strings.TrimSpace(parts[0])
		action := strings.TrimSpace(parts[1])
		if linkID == "" || action == "" {
			writeAPIError(w, r, http.StatusNotFound, "endpoint_not_found", "endpoint not found")
			return
		}
		const actionTimeout = 5 * time.Second
		switch action {
		case "reconnect":
			link, err := manager.linkReconnect(linkID, actionTimeout)
			if err != nil {
				switch {
				case errors.Is(err, errLinkNotFound):
					writeAPIError(w, r, http.StatusNotFound, "link_not_found", err.Error())
				case errors.Is(err, errRuntimeNotRunning):
					writeAPIError(w, r, http.StatusConflict, "runtime_not_running", err.Error())
				default:
					writeAPIError(w, r, http.StatusConflict, "link_reconnect_failed", err.Error())
				}
				return
			}
			writeJSON(w, http.StatusOK, LinkActionResponse{OK: true, Action: "reconnect", Link: link})
		case "drain":
			link, err := manager.linkDrain(linkID, actionTimeout)
			if err != nil {
				switch {
				case errors.Is(err, errLinkNotFound):
					writeAPIError(w, r, http.StatusNotFound, "link_not_found", err.Error())
				default:
					writeAPIError(w, r, http.StatusConflict, "link_drain_failed", err.Error())
				}
				return
			}
			writeJSON(w, http.StatusOK, LinkActionResponse{OK: true, Action: "drain", Link: link})
		case "resume":
			link, err := manager.linkResume(linkID)
			if err != nil {
				switch {
				case errors.Is(err, errLinkNotFound):
					writeAPIError(w, r, http.StatusNotFound, "link_not_found", err.Error())
				case errors.Is(err, errNoBootstrapConfigured):
					writeAPIError(w, r, http.StatusConflict, "runtime_not_configured", err.Error())
				default:
					writeAPIError(w, r, http.StatusConflict, "link_resume_failed", err.Error())
				}
				return
			}
			writeJSON(w, http.StatusOK, LinkActionResponse{OK: true, Action: "resume", Link: link})
		case "gateway.select":
			var req GatewaySelectRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
				return
			}
			link, err := manager.linkGatewaySelect(linkID, req.GatewayID, actionTimeout)
			if err != nil {
				switch {
				case errors.Is(err, errLinkNotFound):
					writeAPIError(w, r, http.StatusNotFound, "link_not_found", err.Error())
				case errors.Is(err, errGatewayIDRequired):
					writeAPIError(w, r, http.StatusBadRequest, "gateway_id_required", err.Error())
				case errors.Is(err, errNoBootstrapConfigured):
					writeAPIError(w, r, http.StatusConflict, "runtime_not_configured", err.Error())
				case errors.Is(err, errGatewayPoolUnavailable):
					writeAPIError(w, r, http.StatusConflict, "gateway_pool_not_configured", err.Error())
				case errors.Is(err, errGatewayNotFoundInPolicy):
					writeAPIError(w, r, http.StatusConflict, "gateway_not_found", err.Error())
				default:
					writeAPIError(w, r, http.StatusConflict, "gateway_select_failed", err.Error())
				}
				return
			}
			writeJSON(w, http.StatusOK, LinkActionResponse{OK: true, Action: "gateway.select", Link: link})
		default:
			writeAPIError(w, r, http.StatusNotFound, "action_not_found", "action not found")
		}
	}
	securityEvaluateHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req SecurityEvaluateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		out, err := manager.evaluateSecurity(req)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "security_eval_invalid", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	securityReputationUpsertHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req SecurityReputationUpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		out, err := manager.upsertSecurityReputation(req)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "security_reputation_invalid", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	securityPolicyUpsertHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req SecurityTenantPolicy
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, manager.upsertTenantPolicy(req))
	}
	securityPolicyGetHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		tenantID := strings.TrimSpace(r.URL.Query().Get("tenantID"))
		writeJSON(w, http.StatusOK, manager.getTenantPolicy(tenantID))
	}
	securityCorporateAllowUpsertHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req CorporateAllowRuleUpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		out, err := manager.upsertCorporateAllowRule(req)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "security_corporate_allow_invalid", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	securityAuditHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		limit := 100
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, err := strconv.Atoi(raw); err != nil || n <= 0 {
				limit = 100
			} else {
				limit = n
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": manager.listSecurityAudit(limit),
		})
	}
	securitySignalIngestHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req SecuritySignalIngestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		out, err := manager.securitySignalIngest(req)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "security_ingest_invalid", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	securitySignalIngestRecentHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		limit := 100
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": manager.listSecurityIngest(limit),
		})
	}
	securityPolicyRolloutHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req SecurityPolicyRolloutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, manager.securityPolicyRollout(req))
	}
	waitHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		target := runtime.StateEstablished
		if raw := strings.TrimSpace(r.URL.Query().Get("state")); raw != "" {
			target = runtime.State(raw)
		}
		timeout := 20 * time.Second
		if raw := strings.TrimSpace(r.URL.Query().Get("timeout")); raw != "" {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				timeout = d
			}
		}
		st, err := manager.waitForState(target, timeout)
		if err != nil {
			writeJSON(w, http.StatusGatewayTimeout, map[string]any{
				"ok":      false,
				"target":  target,
				"timeout": timeout.String(),
				"status":  st,
				"error":   err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"target": target,
			"status": st,
		})
	}
	eventsHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeAPIError(w, r, http.StatusInternalServerError, "stream_unsupported", "stream unsupported")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		id, ch := manager.subscribeEvents()
		defer manager.unsubscribeEvents(id)
		ctx := r.Context()
		keepAlive := time.NewTicker(15 * time.Second)
		defer keepAlive.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-ch:
				raw, err := json.Marshal(e)
				if err != nil {
					continue
				}
				_, _ = w.Write([]byte("event: runtime\n"))
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(raw)
				_, _ = w.Write([]byte("\n\n"))
				flusher.Flush()
			case <-keepAlive.C:
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flusher.Flush()
			}
		}
	}
	collectHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req CollectSupportBundleRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		raw, err := manager.collectSupportBundle(req)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bundle_export_failed", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}
	bridgeStartupHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req BridgeStartupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		out, err := manager.bridgeStartup(req)
		if err != nil {
			writeAPIError(w, r, http.StatusConflict, "bridge_startup_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	bridgeShutdownHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req BridgeShutdownRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		out, err := manager.bridgeShutdown(req)
		if err != nil {
			writeAPIError(w, r, http.StatusConflict, "bridge_shutdown_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	bridgeReconcileHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req BridgeReconcileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		out, err := manager.bridgeReconcile(req)
		if err != nil {
			writeAPIError(w, r, http.StatusConflict, "bridge_reconcile_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	bridgeAutopilotHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req BridgeAutopilotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		out, err := manager.bridgeAutopilot(req)
		if err != nil {
			writeAPIError(w, r, http.StatusConflict, "bridge_autopilot_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	bridgeAutopilotDaemonHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req BridgeAutopilotDaemonRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		out, err := manager.bridgeAutopilotDaemon(req)
		if err != nil {
			writeAPIError(w, r, http.StatusConflict, "bridge_autopilot_daemon_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
	bridgeAutopilotDaemonStreamHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeAPIError(w, r, http.StatusInternalServerError, "stream_unsupported", "stream unsupported")
			return
		}
		var req BridgeAutopilotDaemonRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, "bad_request", "bad request: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		_, _ = w.Write([]byte("event: begin\n"))
		_, _ = w.Write([]byte("data: {\"ok\":true}\n\n"))
		flusher.Flush()

		emit := func(tick BridgeAutopilotDaemonTick) {
			raw, err := json.Marshal(tick)
			if err != nil {
				return
			}
			_, _ = w.Write([]byte("event: tick\n"))
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(raw)
			_, _ = w.Write([]byte("\n\n"))
			flusher.Flush()
		}
		out, err := manager.bridgeAutopilotDaemonRun(r.Context(), req, emit)
		done := map[string]any{
			"ok":   err == nil,
			"runs": out.Runs,
		}
		if err != nil {
			done["error"] = err.Error()
		}
		rawDone, _ := json.Marshal(done)
		_, _ = w.Write([]byte("event: done\n"))
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(rawDone)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
	}
	bridgeStatusStreamHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeAPIError(w, r, http.StatusInternalServerError, "stream_unsupported", "stream unsupported")
			return
		}
		interval := 5 * time.Second
		if raw := strings.TrimSpace(r.URL.Query().Get("interval")); raw != "" {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				interval = d
			}
		}
		duration := time.Duration(0)
		if raw := strings.TrimSpace(r.URL.Query().Get("duration")); raw != "" {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				duration = d
			}
		}
		ctx := r.Context()
		if duration > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, duration)
			defer cancel()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		_, _ = w.Write([]byte("event: begin\n"))
		_, _ = w.Write([]byte("data: {\"ok\":true}\n\n"))
		flusher.Flush()

		emitSnapshot := func() {
			payload := map[string]any{
				"type":   "snapshot",
				"status": manager.status(),
				"health": manager.health(),
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				return
			}
			_, _ = w.Write([]byte("event: status\n"))
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(raw)
			_, _ = w.Write([]byte("\n\n"))
			flusher.Flush()
		}
		emitSnapshot()

		rid, runtimeCh := manager.subscribeEvents()
		defer manager.unsubscribeEvents(rid)
		did, daemonCh := manager.subscribeDaemonTicks()
		defer manager.unsubscribeDaemonTicks(did)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done := map[string]any{"ok": true}
				if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
					done["ok"] = false
					done["error"] = err.Error()
				}
				rawDone, _ := json.Marshal(done)
				_, _ = w.Write([]byte("event: done\n"))
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(rawDone)
				_, _ = w.Write([]byte("\n\n"))
				flusher.Flush()
				return
			case ev := <-runtimeCh:
				raw, err := json.Marshal(map[string]any{
					"type":  "runtime",
					"event": ev,
				})
				if err != nil {
					continue
				}
				_, _ = w.Write([]byte("event: runtime\n"))
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(raw)
				_, _ = w.Write([]byte("\n\n"))
				flusher.Flush()
			case tick := <-daemonCh:
				raw, err := json.Marshal(map[string]any{
					"type": "daemon",
					"tick": tick,
				})
				if err != nil {
					continue
				}
				_, _ = w.Write([]byte("event: daemon\n"))
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(raw)
				_, _ = w.Write([]byte("\n\n"))
				flusher.Flush()
			case <-ticker.C:
				emitSnapshot()
			}
		}
	}
	linksHealthStreamHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeAPIError(w, r, http.StatusInternalServerError, "stream_unsupported", "stream unsupported")
			return
		}
		interval := 5 * time.Second
		if raw := strings.TrimSpace(r.URL.Query().Get("interval")); raw != "" {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				interval = d
			}
		}
		duration := time.Duration(0)
		if raw := strings.TrimSpace(r.URL.Query().Get("duration")); raw != "" {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				duration = d
			}
		}
		ctx := r.Context()
		if duration > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, duration)
			defer cancel()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		_, _ = w.Write([]byte("event: begin\n"))
		_, _ = w.Write([]byte("data: {\"ok\":true}\n\n"))
		flusher.Flush()

		emitLinks := func() {
			payload := map[string]any{
				"type":  "links",
				"links": manager.links(),
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				return
			}
			_, _ = w.Write([]byte("event: links\n"))
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(raw)
			_, _ = w.Write([]byte("\n\n"))
			flusher.Flush()
		}
		emitLinks()

		rid, runtimeCh := manager.subscribeEvents()
		defer manager.unsubscribeEvents(rid)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done := map[string]any{"ok": true}
				if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
					done["ok"] = false
					done["error"] = err.Error()
				}
				rawDone, _ := json.Marshal(done)
				_, _ = w.Write([]byte("event: done\n"))
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(rawDone)
				_, _ = w.Write([]byte("\n\n"))
				flusher.Flush()
				return
			case ev := <-runtimeCh:
				raw, err := json.Marshal(map[string]any{
					"type":  "runtime",
					"event": ev,
				})
				if err != nil {
					continue
				}
				_, _ = w.Write([]byte("event: runtime\n"))
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(raw)
				_, _ = w.Write([]byte("\n\n"))
				flusher.Flush()
				emitLinks()
			case <-ticker.C:
				emitLinks()
			}
		}
	}

	schemaHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, HelperSchemaResponse{
			APIVersion:              helperAPIVersion,
			GatewayPoolVersion:      helperGatewayPoolVersion,
			SecurityContractVersion: helperSecurityContractVersion,
			ProfileBundleVersion:    helperProfileBundleVersion,
			AuthRequired:            authToken != "",
			AuthSchemes:             []string{"Authorization: Bearer <token>", "X-Helper-Token: <token>"},
			RequestID:               "X-Request-ID",
			LeaseHeader:             "X-Helper-Lease-ID",
			Idempotency:             "Most POST endpoints are idempotent when X-Request-ID is provided; streaming POST endpoints are excluded",
			Bootstrap: HelperBootstrapContract{
				SchemaVersion:             helperBootstrapSchemaVersion,
				RequiredBootstrapFields:   []string{"clientID", "serverStaticPub"},
				OptionalBootstrapFields:   []string{"profileID", "region", "securityProfile", "addr", "serverName", "serverID", "gateways", "gatewayPolicy", "rekeyPolicy", "routing", "dns", "bridge", "tun", "support"},
				GatewayPoolSupported:      true,
				GatewayPolicySupported:    true,
				RekeyPolicySupported:      true,
				ProfileBundleSupported:    true,
				SecurityProfileSupported:  true,
				SupportedSecurityProfiles: []string{"compat", "balanced", "high_risk"},
			},
			Endpoints: []string{
				"POST /v1/helper/bridge.startup",
				"POST /v1/helper/bridge.shutdown",
				"POST /v1/helper/bridge.reconcile",
				"POST /v1/helper/bridge.autopilot",
				"POST /v1/helper/bridge.autopilot.once",
				"POST /v1/helper/bridge.autopilot.daemon",
				"POST /v1/helper/bridge.autopilot.daemon.stream",
				"GET /v1/helper/bridge.status.stream",
				"POST /v1/helper/lease.acquire",
				"POST /v1/helper/lease.renew",
				"POST /v1/helper/lease.heartbeat",
				"POST /v1/helper/lease.takeover",
				"POST /v1/helper/lease.release",
				"GET /v1/helper/lease.status",
				"POST /v1/helper/bootstrap.validate",
				"POST /v1/helper/bootstrap.apply",
				"POST /v1/helper/profile.apply",
				"GET /v1/helper/profile.current",
				"POST /v1/helper/tunnel.start",
				"POST /v1/helper/tunnel.stop",
				"POST /v1/helper/tunnel.refresh",
				"GET /v1/helper/status",
				"GET /v1/helper/health",
				"GET /v1/helper/stats.read",
				"GET /v1/helper/links",
				"GET /v1/helper/links/<linkID>",
				"GET /v1/helper/links/health.stream",
				"POST /v1/helper/links/<linkID>/reconnect",
				"POST /v1/helper/links/<linkID>/drain",
				"POST /v1/helper/links/<linkID>/resume",
				"POST /v1/helper/links/<linkID>/gateway.select",
				"POST /v1/helper/security.evaluate",
				"POST /v1/helper/security.reputation.upsert",
				"POST /v1/helper/security.policy.upsert",
				"GET /v1/helper/security.policy.get",
				"POST /v1/helper/security.policy.rollout",
				"POST /v1/helper/security.corporate-allow.upsert",
				"GET /v1/helper/security.audit",
				"POST /v1/helper/security.signal.ingest",
				"GET /v1/helper/security.signal.ingest.recent",
				"GET /v1/helper/wait",
				"GET /v1/helper/events",
				"POST /v1/helper/diagnostics.export",
			},
			Legacy: []string{
				"POST /bridge.startup",
				"POST /bridge.shutdown",
				"POST /bridge.reconcile",
				"POST /bridge.autopilot",
				"POST /bridge.autopilot.once",
				"POST /bridge.autopilot.daemon",
				"POST /bridge.autopilot.daemon.stream",
				"GET /bridge.status.stream",
				"POST /lease.acquire",
				"POST /lease.renew",
				"POST /lease.heartbeat",
				"POST /lease.takeover",
				"POST /lease.release",
				"GET /lease.status",
				"POST /bootstrap.validate",
				"POST /start",
				"POST /stop",
				"POST /profile.apply",
				"GET /profile.current",
				"GET /status",
				"GET /health",
				"POST /collectSupportBundle",
				"POST /collect-support-bundle",
				"POST /bootstrap.apply",
				"POST /profile.apply",
				"GET /profile.current",
				"POST /tunnel.start",
				"POST /tunnel.stop",
				"POST /tunnel.refresh",
				"GET /stats.read",
				"GET /links",
				"GET /links/<linkID>",
				"GET /links/health.stream",
				"POST /links/<linkID>/reconnect",
				"POST /links/<linkID>/drain",
				"POST /links/<linkID>/resume",
				"POST /links/<linkID>/gateway.select",
				"POST /security.evaluate",
				"POST /security.reputation.upsert",
				"POST /security.policy.upsert",
				"GET /security.policy.get",
				"POST /security.policy.rollout",
				"POST /security.corporate-allow.upsert",
				"GET /security.audit",
				"POST /security.signal.ingest",
				"GET /security.signal.ingest.recent",
				"GET /wait",
				"GET /events",
				"POST /diagnostics.export",
			},
		})
	}

	// Legacy paths.
	mux.HandleFunc("/bridge.startup", wrapNoLease(bridgeStartupHandler))
	mux.HandleFunc("/bridge.shutdown", wrapNoLease(bridgeShutdownHandler))
	mux.HandleFunc("/bridge.reconcile", wrapNoLease(bridgeReconcileHandler))
	mux.HandleFunc("/bridge.autopilot", wrapNoLease(bridgeAutopilotHandler))
	mux.HandleFunc("/bridge.autopilot.once", wrapNoLease(bridgeAutopilotHandler))
	mux.HandleFunc("/bridge.autopilot.daemon", wrapNoLease(bridgeAutopilotDaemonHandler))
	mux.HandleFunc("/bridge.autopilot.daemon.stream", wrapNoLeaseNoIdem(bridgeAutopilotDaemonStreamHandler))
	mux.HandleFunc("/bridge.status.stream", withRequestID(protected(bridgeStatusStreamHandler)))
	mux.HandleFunc("/lease.acquire", wrapNoLease(leaseAcquireHandler))
	mux.HandleFunc("/lease.renew", wrapNoLease(leaseRenewHandler))
	mux.HandleFunc("/lease.heartbeat", wrapNoLease(leaseHeartbeatHandler))
	mux.HandleFunc("/lease.takeover", wrapNoLease(leaseTakeoverHandler))
	mux.HandleFunc("/lease.release", wrapNoLease(leaseReleaseHandler))
	mux.HandleFunc("/lease.status", wrapNoLease(leaseStatusHandler))
	mux.HandleFunc("/start", wrap(startHandler))
	mux.HandleFunc("/stop", wrap(stopHandler))
	mux.HandleFunc("/status", wrap(statusHandler))
	mux.HandleFunc("/health", withRequestID(healthHandler))
	mux.HandleFunc("/collectSupportBundle", wrap(collectHandler))
	mux.HandleFunc("/collect-support-bundle", wrap(collectHandler))
	mux.HandleFunc("/bootstrap.validate", wrapNoLease(validateBootstrapHandler))
	mux.HandleFunc("/bootstrap.apply", wrap(applyBootstrapHandler))
	mux.HandleFunc("/profile.apply", wrap(profileApplyHandler))
	mux.HandleFunc("/profile.current", wrapNoLease(profileCurrentHandler))
	mux.HandleFunc("/tunnel.start", wrap(startTunnelHandler))
	mux.HandleFunc("/tunnel.stop", wrap(stopHandler))
	mux.HandleFunc("/tunnel.refresh", wrap(refreshHandler))
	mux.HandleFunc("/stats.read", wrap(statsHandler))
	mux.HandleFunc("/links", wrap(linksHandler))
	mux.HandleFunc("/links/health.stream", withRequestID(protected(linksHealthStreamHandler)))
	mux.HandleFunc("/links/", wrap(linksHandler))
	mux.HandleFunc("/security.evaluate", wrap(securityEvaluateHandler))
	mux.HandleFunc("/security.reputation.upsert", wrap(securityReputationUpsertHandler))
	mux.HandleFunc("/security.policy.upsert", wrap(securityPolicyUpsertHandler))
	mux.HandleFunc("/security.policy.get", wrapNoLease(securityPolicyGetHandler))
	mux.HandleFunc("/security.policy.rollout", wrap(securityPolicyRolloutHandler))
	mux.HandleFunc("/security.corporate-allow.upsert", wrap(securityCorporateAllowUpsertHandler))
	mux.HandleFunc("/security.audit", wrapNoLease(securityAuditHandler))
	mux.HandleFunc("/security.signal.ingest", wrap(securitySignalIngestHandler))
	mux.HandleFunc("/security.signal.ingest.recent", wrapNoLease(securitySignalIngestRecentHandler))
	mux.HandleFunc("/wait", wrap(waitHandler))
	mux.HandleFunc("/events", wrap(eventsHandler))
	mux.HandleFunc("/diagnostics.export", wrap(collectHandler))

	// Versioned API paths.
	mux.HandleFunc("/v1/helper/schema", withRequestID(schemaHandler))
	mux.HandleFunc("/v1/helper/bridge.startup", wrapNoLease(bridgeStartupHandler))
	mux.HandleFunc("/v1/helper/bridge.shutdown", wrapNoLease(bridgeShutdownHandler))
	mux.HandleFunc("/v1/helper/bridge.reconcile", wrapNoLease(bridgeReconcileHandler))
	mux.HandleFunc("/v1/helper/bridge.autopilot", wrapNoLease(bridgeAutopilotHandler))
	mux.HandleFunc("/v1/helper/bridge.autopilot.once", wrapNoLease(bridgeAutopilotHandler))
	mux.HandleFunc("/v1/helper/bridge.autopilot.daemon", wrapNoLease(bridgeAutopilotDaemonHandler))
	mux.HandleFunc("/v1/helper/bridge.autopilot.daemon.stream", wrapNoLeaseNoIdem(bridgeAutopilotDaemonStreamHandler))
	mux.HandleFunc("/v1/helper/bridge.status.stream", withRequestID(protected(bridgeStatusStreamHandler)))
	mux.HandleFunc("/v1/helper/lease.acquire", wrapNoLease(leaseAcquireHandler))
	mux.HandleFunc("/v1/helper/lease.renew", wrapNoLease(leaseRenewHandler))
	mux.HandleFunc("/v1/helper/lease.heartbeat", wrapNoLease(leaseHeartbeatHandler))
	mux.HandleFunc("/v1/helper/lease.takeover", wrapNoLease(leaseTakeoverHandler))
	mux.HandleFunc("/v1/helper/lease.release", wrapNoLease(leaseReleaseHandler))
	mux.HandleFunc("/v1/helper/lease.status", wrapNoLease(leaseStatusHandler))
	mux.HandleFunc("/v1/helper/start", wrap(startHandler))
	mux.HandleFunc("/v1/helper/stop", wrap(stopHandler))
	mux.HandleFunc("/v1/helper/status", wrap(statusHandler))
	mux.HandleFunc("/v1/helper/health", withRequestID(healthHandler))
	mux.HandleFunc("/v1/helper/collectSupportBundle", wrap(collectHandler))
	mux.HandleFunc("/v1/helper/bootstrap.validate", wrapNoLease(validateBootstrapHandler))
	mux.HandleFunc("/v1/helper/bootstrap.apply", wrap(applyBootstrapHandler))
	mux.HandleFunc("/v1/helper/profile.apply", wrap(profileApplyHandler))
	mux.HandleFunc("/v1/helper/profile.current", wrapNoLease(profileCurrentHandler))
	mux.HandleFunc("/v1/helper/tunnel.start", wrap(startTunnelHandler))
	mux.HandleFunc("/v1/helper/tunnel.stop", wrap(stopHandler))
	mux.HandleFunc("/v1/helper/tunnel.refresh", wrap(refreshHandler))
	mux.HandleFunc("/v1/helper/stats.read", wrap(statsHandler))
	mux.HandleFunc("/v1/helper/links", wrap(linksHandler))
	mux.HandleFunc("/v1/helper/links/health.stream", withRequestID(protected(linksHealthStreamHandler)))
	mux.HandleFunc("/v1/helper/links/", wrap(linksHandler))
	mux.HandleFunc("/v1/helper/security.evaluate", wrap(securityEvaluateHandler))
	mux.HandleFunc("/v1/helper/security.reputation.upsert", wrap(securityReputationUpsertHandler))
	mux.HandleFunc("/v1/helper/security.policy.upsert", wrap(securityPolicyUpsertHandler))
	mux.HandleFunc("/v1/helper/security.policy.get", wrapNoLease(securityPolicyGetHandler))
	mux.HandleFunc("/v1/helper/security.policy.rollout", wrap(securityPolicyRolloutHandler))
	mux.HandleFunc("/v1/helper/security.corporate-allow.upsert", wrap(securityCorporateAllowUpsertHandler))
	mux.HandleFunc("/v1/helper/security.audit", wrapNoLease(securityAuditHandler))
	mux.HandleFunc("/v1/helper/security.signal.ingest", wrap(securitySignalIngestHandler))
	mux.HandleFunc("/v1/helper/security.signal.ingest.recent", wrapNoLease(securitySignalIngestRecentHandler))
	mux.HandleFunc("/v1/helper/wait", wrap(waitHandler))
	mux.HandleFunc("/v1/helper/events", wrap(eventsHandler))
	mux.HandleFunc("/v1/helper/diagnostics.export", wrap(collectHandler))

	return mux
}

type requestIDKey struct{}

func withRequestID(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = generateRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next(w, r.WithContext(ctx))
	}
}

func requestIDFromContext(r *http.Request) string {
	v := r.Context().Value(requestIDKey{})
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func generateRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *captureWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.body.Write(p)
	return w.ResponseWriter.Write(p)
}

func withIdempotency(manager *helperManager, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next(w, r)
			return
		}
		reqID := requestIDFromContext(r)
		if reqID == "" {
			next(w, r)
			return
		}
		key := r.Method + " " + r.URL.Path + " " + reqID
		manager.mu.Lock()
		rec, ok := manager.idempotency[key]
		manager.mu.Unlock()
		if ok {
			if rec.ContentType != "" {
				w.Header().Set("Content-Type", rec.ContentType)
			}
			w.Header().Set("X-Idempotent-Replay", "true")
			w.WriteHeader(rec.Status)
			_, _ = w.Write(rec.Body)
			return
		}
		cw := &captureWriter{ResponseWriter: w}
		next(cw, r)
		if cw.status == 0 {
			cw.status = http.StatusOK
		}
		contentType := cw.Header().Get("Content-Type")
		manager.mu.Lock()
		manager.idempotency[key] = idempotencyRecord{
			Status:      cw.status,
			ContentType: contentType,
			Body:        append([]byte(nil), cw.body.Bytes()...),
		}
		manager.mu.Unlock()
	}
}

func requireLease(manager *helperManager) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				next(w, r)
				return
			}
			leaseID := strings.TrimSpace(r.Header.Get("X-Helper-Lease-ID"))
			if err := manager.checkLease(leaseID); err != nil {
				writeAPIError(w, r, http.StatusLocked, "lease_required", err.Error())
				return
			}
			next(w, r)
		}
	}
}

func requireAuth(token string) func(http.HandlerFunc) http.HandlerFunc {
	token = strings.TrimSpace(token)
	return func(next http.HandlerFunc) http.HandlerFunc {
		if token == "" {
			return next
		}
		return func(w http.ResponseWriter, r *http.Request) {
			if hasValidAuthToken(r, token) {
				next(w, r)
				return
			}
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "unauthorized")
		}
	}
}

func hasValidAuthToken(r *http.Request, token string) bool {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		got := strings.TrimSpace(h[7:])
		if got == token {
			return true
		}
	}
	if strings.TrimSpace(r.Header.Get("X-Helper-Token")) == token {
		return true
	}
	return false
}

func readTokenFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("empty token in %s", path)
	}
	return token, nil
}

func normalizeBootstrap(pb *ProfileBootstrap) error {
	pb.ProfileID = strings.TrimSpace(pb.ProfileID)
	pb.Region = strings.TrimSpace(pb.Region)
	pb.SecurityProfile = normalizeSecurityProfile(pb.SecurityProfile)
	if err := normalizeRoutePolicy(&pb.Routing); err != nil {
		return err
	}
	normalizeDNSPolicy(&pb.DNS)
	applySecurityProfileDefaults(pb)
	if err := validateSecurityProfilePolicy(*pb); err != nil {
		return err
	}

	if strings.TrimSpace(pb.ClientID) == "" || strings.TrimSpace(pb.ServerStaticPubB64) == "" {
		return errors.New("profileBootstrap.clientID and profileBootstrap.serverStaticPub are required")
	}
	hasGateways := len(pb.Gateways) > 0
	if !hasGateways && strings.TrimSpace(pb.Addr) == "" {
		pb.Addr = "127.0.0.1:8443"
	}
	if strings.TrimSpace(pb.ServerName) == "" {
		pb.ServerName = "localhost"
	}
	if pb.ConnectTimeout <= 0 {
		pb.ConnectTimeout = 5 * time.Second
	}
	if (pb.Insecure || pb.Plain) && !allowInsecureTunnelForTests() {
		return errors.New("insecure tunnel modes are disabled in production (plain/insecure); set ALLOW_INSECURE_TUNNEL_FOR_TESTS=1 for test-only usage")
	}
	if (pb.Insecure || pb.Plain) && allowInsecureTunnelForTests() {
		log.Printf("warning: insecure tunnel mode enabled (insecure=%v plain=%v) under ALLOW_INSECURE_TUNNEL_FOR_TESTS", pb.Insecure, pb.Plain)
	}
	pb.GatewayPolicy.ForceGatewayID = strings.TrimSpace(pb.GatewayPolicy.ForceGatewayID)
	if pb.GatewayPolicy.StickyDuration < 0 {
		return errors.New("profileBootstrap.gatewayPolicy.stickyDuration must be >= 0")
	}
	if pb.GatewayPolicy.CooldownMin < 0 || pb.GatewayPolicy.CooldownMax < 0 {
		return errors.New("profileBootstrap.gatewayPolicy cooldown values must be >= 0")
	}
	if pb.GatewayPolicy.CooldownMax > 0 && pb.GatewayPolicy.CooldownMin > 0 && pb.GatewayPolicy.CooldownMax < pb.GatewayPolicy.CooldownMin {
		return errors.New("profileBootstrap.gatewayPolicy.cooldownMax must be >= cooldownMin")
	}
	if pb.GatewayPolicy.SwitchHysteresis < 0 {
		return errors.New("profileBootstrap.gatewayPolicy.switchHysteresis must be >= 0")
	}
	if hasGateways {
		validEndpoints := 0
		forcedGatewayFound := pb.GatewayPolicy.ForceGatewayID == ""
		for i := range pb.Gateways {
			gw := &pb.Gateways[i]
			gw.GatewayID = strings.TrimSpace(gw.GatewayID)
			gw.Region = strings.TrimSpace(gw.Region)
			gw.Health = strings.ToLower(strings.TrimSpace(gw.Health))
			if gw.GatewayID == "" {
				gw.GatewayID = fmt.Sprintf("gw-%d", i+1)
			}
			if gw.GatewayID == pb.GatewayPolicy.ForceGatewayID {
				forcedGatewayFound = true
			}
			if gw.Hints.LoadScore < 0 {
				gw.Hints.LoadScore = 0
			}
			if gw.Hints.LoadScore > 100 {
				gw.Hints.LoadScore = 100
			}
			if gw.Hints.RTTScore < 0 {
				gw.Hints.RTTScore = 0
			}
			if gw.Hints.RTTScore > 100 {
				gw.Hints.RTTScore = 100
			}
			for j := range gw.Endpoints {
				ep := &gw.Endpoints[j]
				ep.Addr = strings.TrimSpace(ep.Addr)
				ep.ServerName = strings.TrimSpace(ep.ServerName)
				if ep.ServerName == "" {
					ep.ServerName = pb.ServerName
				}
				if ep.Addr != "" {
					validEndpoints++
				}
			}
		}
		if validEndpoints == 0 {
			return errors.New("profileBootstrap.gateways must contain at least one non-empty endpoint addr")
		}
		if !forcedGatewayFound {
			return errors.New("profileBootstrap.gatewayPolicy.forceGatewayID is not present in profileBootstrap.gateways")
		}
	}
	if pb.RekeyPolicy.AckRetries < 0 {
		return errors.New("profileBootstrap.rekeyPolicy.ackRetries must be >= 0")
	}
	if pb.RekeyPolicy.InitRetries < 0 {
		return errors.New("profileBootstrap.rekeyPolicy.initRetries must be >= 0")
	}
	if pb.RekeyPolicy.AckRetryDelay < 0 ||
		pb.RekeyPolicy.InitInterval < 0 ||
		pb.RekeyPolicy.InitAckTimeout < 0 ||
		pb.RekeyPolicy.InitRetryDelay < 0 ||
		pb.RekeyPolicy.InitOverlap < 0 {
		return errors.New("profileBootstrap.rekeyPolicy duration values must be >= 0")
	}
	return nil
}

func normalizeRoutePolicy(p *RoutePolicy) error {
	p.Strategy = strings.TrimSpace(p.Strategy)
	p.Source = strings.ToLower(strings.TrimSpace(p.Source))
	p.RulesetRef = strings.TrimSpace(p.RulesetRef)
	p.DefaultAction = strings.TrimSpace(p.DefaultAction)
	p.BGP.Neighbor = strings.TrimSpace(p.BGP.Neighbor)
	p.BGP.ImportPolicy = strings.TrimSpace(p.BGP.ImportPolicy)
	p.BGP.PrefixSetName = strings.TrimSpace(p.BGP.PrefixSetName)
	if p.Source == "" {
		p.Source = "static"
	}
	switch p.Source {
	case "static", "ruleset", "bgp":
	default:
		return fmt.Errorf("profileBootstrap.routing.source %q is unsupported", p.Source)
	}
	if p.BGP.HoldTimeSec < 0 || p.BGP.KeepaliveSec < 0 || p.BGP.MaxPrefixes < 0 {
		return errors.New("profileBootstrap.routing.bgp numeric values must be >= 0")
	}
	if p.Source == "bgp" {
		if !boolDeref(p.BGP.Enabled) {
			v := true
			p.BGP.Enabled = &v
		}
		if p.BGP.Neighbor == "" {
			p.BGP.Neighbor = "45.154.73.71"
		}
		if p.BGP.NeighborAS == 0 {
			p.BGP.NeighborAS = 65432
		}
		if p.BGP.HoldTimeSec == 0 {
			p.BGP.HoldTimeSec = 240
		}
		if p.BGP.KeepaliveSec == 0 {
			p.BGP.KeepaliveSec = 80
		}
		if p.BGP.MaxPrefixes == 0 {
			p.BGP.MaxPrefixes = 50000
		}
		if p.BGP.ImportPolicy == "" {
			p.BGP.ImportPolicy = "prefix-only"
		}
	}
	return nil
}

func normalizeDNSPolicy(p *DNSPolicy) {
	p.Mode = strings.TrimSpace(p.Mode)
	p.TemplateRef = strings.TrimSpace(p.TemplateRef)
}

func normalizeSecurityProfile(profile string) string {
	p := strings.ToLower(strings.TrimSpace(profile))
	if p == "" {
		return "balanced"
	}
	return p
}

func applySecurityProfileDefaults(pb *ProfileBootstrap) {
	switch pb.SecurityProfile {
	case "compat":
		if pb.Bridge.AllowLocalTCPBridge == nil {
			pb.Bridge.AllowLocalTCPBridge = boolPtr(true)
		}
		if pb.Bridge.AllowLocalControlAPI == nil {
			pb.Bridge.AllowLocalControlAPI = boolPtr(true)
		}
	case "balanced":
		if pb.Bridge.AllowLocalTCPBridge == nil {
			pb.Bridge.AllowLocalTCPBridge = boolPtr(false)
		}
		if pb.Bridge.AllowLocalControlAPI == nil {
			pb.Bridge.AllowLocalControlAPI = boolPtr(false)
		}
	case "high_risk":
		if pb.Bridge.AllowLocalTCPBridge == nil {
			pb.Bridge.AllowLocalTCPBridge = boolPtr(false)
		}
		if pb.Bridge.AllowLocalControlAPI == nil {
			pb.Bridge.AllowLocalControlAPI = boolPtr(false)
		}
	}
}

func validateSecurityProfilePolicy(pb ProfileBootstrap) error {
	switch pb.SecurityProfile {
	case "compat", "balanced", "high_risk":
	default:
		return fmt.Errorf("profileBootstrap.securityProfile %q is unsupported", pb.SecurityProfile)
	}
	if pb.SecurityProfile == "high_risk" {
		if boolDeref(pb.Bridge.AllowLocalTCPBridge) {
			return errors.New("profileBootstrap.bridge.allowLocalTCPBridge must be false for high_risk profile")
		}
		if boolDeref(pb.Bridge.AllowLocalControlAPI) {
			return errors.New("profileBootstrap.bridge.allowLocalControlAPI must be false for high_risk profile")
		}
	}
	return nil
}

func boolPtr(v bool) *bool {
	return &v
}

func boolDeref(v *bool) bool {
	return v != nil && *v
}

func allowInsecureTunnelForTests() bool {
	return envTruthy("ALLOW_INSECURE_TUNNEL_FOR_TESTS")
}

func allowTCPHelperNoAuthForTests() bool {
	return envTruthy("ALLOW_HELPER_TCP_NOAUTH_FOR_TESTS")
}

func envTruthy(name string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, apiErrorEnvelope{
		OK: false,
		Error: apiError{
			Code:      code,
			Message:   message,
			RequestID: requestIDFromContext(r),
		},
	})
}

func parseHex16(s string) ([16]byte, error) {
	var out [16]byte
	if len(s) != 32 {
		return out, core.ErrInvalidHandshake
	}
	for i := 0; i < 16; i++ {
		var v byte
		for j := 0; j < 2; j++ {
			c := s[i*2+j]
			switch {
			case c >= '0' && c <= '9':
				v = v<<4 | byte(c-'0')
			case c >= 'a' && c <= 'f':
				v = v<<4 | byte(c-'a'+10)
			case c >= 'A' && c <= 'F':
				v = v<<4 | byte(c-'A'+10)
			default:
				return out, core.ErrInvalidHandshake
			}
		}
		out[i] = v
	}
	return out, nil
}
