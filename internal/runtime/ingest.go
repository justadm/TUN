package runtime

import "errors"

type KeyStatus string

const (
	KeyStatusActive   KeyStatus = "active"
	KeyStatusPrevious KeyStatus = "previous"
	KeyStatusRetired  KeyStatus = "retired"
)

var (
	ErrMissingSignature    = errors.New("runtime: missing support bundle signature")
	ErrMissingSignatureKey = errors.New("runtime: missing support bundle signature key id")
	ErrUnknownSigningKeyID = errors.New("runtime: unknown support bundle signing key id")
	ErrRetiredSigningKey   = errors.New("runtime: support bundle signed with retired key")
)

type VerificationPolicy struct {
	RequireSignature bool
}

type VerificationKey struct {
	Key    []byte
	Status KeyStatus
}

type VerificationKeyring struct {
	Keys map[string]VerificationKey
}

func VerifyEnvelopeForIngest(env SupportBundleEnvelope, keyring VerificationKeyring, policy VerificationPolicy) error {
	if err := VerifySupportBundleEnvelope(env, SigningOptions{}); err != nil {
		return err
	}
	if env.SignatureHMACSHA256 == "" {
		if policy.RequireSignature {
			return ErrMissingSignature
		}
		return nil
	}
	if env.SignatureKeyID == "" {
		return ErrMissingSignatureKey
	}
	k, ok := keyring.Keys[env.SignatureKeyID]
	if !ok {
		return ErrUnknownSigningKeyID
	}
	if k.Status == "" {
		k.Status = KeyStatusActive
	}
	if k.Status == KeyStatusRetired {
		return ErrRetiredSigningKey
	}
	if len(k.Key) == 0 {
		return ErrUnknownSigningKeyID
	}
	return VerifySupportBundleEnvelope(env, SigningOptions{Key: k.Key})
}
