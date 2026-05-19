package runtime

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	SupportBundleVersion         = "v1"
	SupportBundleEnvelopeVersion = "v1"
)

var (
	RuntimeVersion = "dev"
	BuildInfo      = "unknown"

	reSecretKeyword = regexp.MustCompile(`(?i)(token|secret|password|private|api[_-]?key|bearer|auth)`)
	reHexLong       = regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`)
	reB64Like       = regexp.MustCompile(`[A-Za-z0-9+/_=-]{40,}`)
)

var ErrBundleIntegrity = errors.New("runtime: support bundle integrity check failed")

type EventRecord struct {
	State      State      `json:"state"`
	ErrorClass ErrorClass `json:"error_class"`
	Cause      string     `json:"cause,omitempty"`
	Snapshot   Snapshot   `json:"snapshot"`
}

type eventLogRecord struct {
	Time       time.Time  `json:"time"`
	State      State      `json:"state"`
	ErrorClass ErrorClass `json:"error_class"`
	Cause      string     `json:"cause,omitempty"`
	Snapshot   Snapshot   `json:"snapshot"`
}

type JSONEventLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewJSONEventLogger(w io.Writer) *JSONEventLogger {
	return &JSONEventLogger{w: w}
}

func (l *JSONEventLogger) OnEvent(e Event) error {
	if l == nil || l.w == nil {
		return nil
	}
	opts := defaultRedactionOptions()
	rec := eventLogRecord{
		Time:       time.Now().UTC(),
		State:      e.State,
		ErrorClass: e.ErrorClass,
		Snapshot:   redactSnapshot(e.Snapshot, opts),
	}
	if e.Cause != nil {
		rec.Cause = redactString(e.Cause.Error(), opts)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

type SupportBundle struct {
	BundleVersion string          `json:"bundle_version"`
	GeneratedAt   time.Time       `json:"generated_at"`
	Runtime       RuntimeMeta     `json:"runtime"`
	Environment   EnvironmentMeta `json:"environment"`
	FinalSnapshot Snapshot        `json:"final_snapshot"`
	Events        []EventRecord   `json:"events"`
	DroppedEvents int             `json:"dropped_events"`
	TotalEvents   int             `json:"total_events"`
}

type SupportBundleEnvelope struct {
	EnvelopeVersion     string        `json:"envelope_version"`
	GeneratedAt         time.Time     `json:"generated_at"`
	Bundle              SupportBundle `json:"bundle"`
	ChecksumSHA256      string        `json:"checksum_sha256"`
	SignatureHMACSHA256 string        `json:"signature_hmac_sha256,omitempty"`
	SignatureKeyID      string        `json:"signature_key_id,omitempty"`
}

type RuntimeMeta struct {
	Version string `json:"version"`
	Build   string `json:"build"`
	Role    Role   `json:"role"`
}

type EnvironmentMeta struct {
	Ring   string `json:"ring,omitempty"`
	HostID string `json:"host_id,omitempty"`
}

type RedactionOptions struct {
	Enabled     bool
	CauseMaxLen int
}

type SupportBundleConfig struct {
	Role           Role
	RuntimeVersion string
	BuildInfo      string
	Ring           string
	HostID         string
	Redaction      RedactionOptions
}

type SigningOptions struct {
	Key   []byte
	KeyID string
}

type SupportBundleCollector struct {
	mu        sync.Mutex
	maxEvents int
	events    []EventRecord
	dropped   int
	total     int
}

func NewSupportBundleCollector(maxEvents int) *SupportBundleCollector {
	if maxEvents <= 0 {
		maxEvents = 1000
	}
	return &SupportBundleCollector{
		maxEvents: maxEvents,
		events:    make([]EventRecord, 0, maxEvents),
	}
}

func (c *SupportBundleCollector) OnEvent(e Event) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total++
	rec := EventRecord{
		State:      e.State,
		ErrorClass: e.ErrorClass,
		Snapshot:   e.Snapshot,
	}
	if e.Cause != nil {
		rec.Cause = e.Cause.Error()
	}
	if len(c.events) < c.maxEvents {
		c.events = append(c.events, rec)
		return
	}
	copy(c.events[0:], c.events[1:])
	c.events[len(c.events)-1] = rec
	c.dropped++
}

func (c *SupportBundleCollector) Build() SupportBundle {
	return c.BuildWithConfig(SupportBundleConfig{})
}

func (c *SupportBundleCollector) BuildWithConfig(cfg SupportBundleConfig) SupportBundle {
	c.mu.Lock()
	defer c.mu.Unlock()

	events := make([]EventRecord, len(c.events))
	copy(events, c.events)

	var final Snapshot
	if len(events) > 0 {
		final = events[len(events)-1].Snapshot
	}

	rv := cfg.RuntimeVersion
	if rv == "" {
		rv = RuntimeVersion
	}
	bi := cfg.BuildInfo
	if bi == "" {
		bi = BuildInfo
	}

	b := SupportBundle{
		BundleVersion: SupportBundleVersion,
		GeneratedAt:   time.Now().UTC(),
		Runtime: RuntimeMeta{
			Version: rv,
			Build:   bi,
			Role:    cfg.Role,
		},
		Environment: EnvironmentMeta{
			Ring:   cfg.Ring,
			HostID: cfg.HostID,
		},
		FinalSnapshot: final,
		Events:        events,
		DroppedEvents: c.dropped,
		TotalEvents:   c.total,
	}
	return applyBundleRedaction(b, normalizeRedactionOptions(cfg.Redaction))
}

func (c *SupportBundleCollector) ExportJSON() ([]byte, error) {
	return c.ExportJSONWithConfig(SupportBundleConfig{})
}

func (c *SupportBundleCollector) ExportJSONWithConfig(cfg SupportBundleConfig) ([]byte, error) {
	b := c.BuildWithConfig(cfg)
	return json.MarshalIndent(b, "", "  ")
}

func (c *SupportBundleCollector) ExportEnvelopeJSONWithConfig(cfg SupportBundleConfig, sign SigningOptions) ([]byte, error) {
	b := c.BuildWithConfig(cfg)
	rawBundle, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(rawBundle)
	env := SupportBundleEnvelope{
		EnvelopeVersion: SupportBundleEnvelopeVersion,
		GeneratedAt:     time.Now().UTC(),
		Bundle:          b,
		ChecksumSHA256:  hex.EncodeToString(sum[:]),
	}
	if len(sign.Key) > 0 {
		mac := hmac.New(sha256.New, sign.Key)
		_, _ = mac.Write(rawBundle)
		env.SignatureHMACSHA256 = hex.EncodeToString(mac.Sum(nil))
		env.SignatureKeyID = sign.KeyID
	}
	return json.MarshalIndent(env, "", "  ")
}

func VerifySupportBundleEnvelope(env SupportBundleEnvelope, sign SigningOptions) error {
	rawBundle, err := json.Marshal(env.Bundle)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(rawBundle)
	if env.ChecksumSHA256 != hex.EncodeToString(sum[:]) {
		return ErrBundleIntegrity
	}
	if len(sign.Key) > 0 {
		if env.SignatureHMACSHA256 == "" {
			return ErrBundleIntegrity
		}
		mac := hmac.New(sha256.New, sign.Key)
		_, _ = mac.Write(rawBundle)
		got := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(got), []byte(env.SignatureHMACSHA256)) {
			return ErrBundleIntegrity
		}
	}
	return nil
}

type RotationOptions struct {
	MaxBytes       int64
	RotateInterval time.Duration
	MaxBackups     int
}

type RotatingFileWriter struct {
	mu sync.Mutex

	path string
	opts RotationOptions

	f        *os.File
	size     int64
	openedAt time.Time
}

func NewRotatingFileWriter(path string, opts RotationOptions) (*RotatingFileWriter, error) {
	if opts.MaxBackups <= 0 {
		opts.MaxBackups = 5
	}
	w := &RotatingFileWriter{
		path: path,
		opts: opts,
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	if w.shouldRotate(now, int64(len(p))) {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

func (w *RotatingFileWriter) shouldRotate(now time.Time, nextWrite int64) bool {
	if w.f == nil {
		return true
	}
	if w.opts.MaxBytes > 0 && w.size+nextWrite > w.opts.MaxBytes {
		return true
	}
	if w.opts.RotateInterval > 0 && !w.openedAt.IsZero() && now.Sub(w.openedAt) >= w.opts.RotateInterval {
		return true
	}
	return false
}

func (w *RotatingFileWriter) rotate() error {
	if w.f != nil {
		if err := w.f.Close(); err != nil {
			return err
		}
		w.f = nil
	}
	if err := w.shiftBackups(); err != nil {
		return err
	}
	if err := os.Rename(w.path, w.backupPath(1)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return w.open()
}

func (w *RotatingFileWriter) shiftBackups() error {
	for i := w.opts.MaxBackups; i >= 1; i-- {
		src := w.backupPath(i)
		if i == w.opts.MaxBackups {
			_ = os.Remove(src)
			continue
		}
		dst := w.backupPath(i + 1)
		if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (w *RotatingFileWriter) backupPath(idx int) string {
	dir := filepath.Dir(w.path)
	base := filepath.Base(w.path)
	return filepath.Join(dir, base+"."+itoa(idx))
}

func (w *RotatingFileWriter) open() error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f = f
	w.size = info.Size()
	w.openedAt = time.Now()
	return nil
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func defaultRedactionOptions() RedactionOptions {
	return RedactionOptions{
		Enabled:     true,
		CauseMaxLen: 256,
	}
}

func normalizeRedactionOptions(in RedactionOptions) RedactionOptions {
	def := defaultRedactionOptions()
	if !in.Enabled && in.CauseMaxLen == 0 {
		return def
	}
	if in.CauseMaxLen <= 0 {
		in.CauseMaxLen = def.CauseMaxLen
	}
	return in
}

func applyBundleRedaction(b SupportBundle, opts RedactionOptions) SupportBundle {
	if !opts.Enabled {
		return b
	}
	b.FinalSnapshot = redactSnapshot(b.FinalSnapshot, opts)
	for i := range b.Events {
		b.Events[i].Cause = redactString(b.Events[i].Cause, opts)
		b.Events[i].Snapshot = redactSnapshot(b.Events[i].Snapshot, opts)
	}
	return b
}

func redactSnapshot(s Snapshot, opts RedactionOptions) Snapshot {
	s.LastError = redactString(s.LastError, opts)
	s.LastRetryReason = redactString(s.LastRetryReason, opts)
	return s
}

func redactString(s string, opts RedactionOptions) string {
	if !opts.Enabled || s == "" {
		return s
	}
	if reSecretKeyword.MatchString(s) {
		return "[redacted]"
	}
	out := reHexLong.ReplaceAllString(s, "[redacted]")
	out = reB64Like.ReplaceAllString(out, "[redacted]")
	out = strings.TrimSpace(out)
	if opts.CauseMaxLen > 0 && len(out) > opts.CauseMaxLen {
		out = out[:opts.CauseMaxLen] + "...(truncated)"
	}
	return out
}
