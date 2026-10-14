package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// Variables of the files of the audit signing key and of the verify keys
// (ADR 60 decision 18).
const (
	signingKeyEnvVar = "DUSTPAN_AUDIT_SIGNING_KEY_FILE"
	verifyKeysEnvVar = "DUSTPAN_AUDIT_VERIFY_KEYS_FILE"
)

var (
	errCheckpointsNeedKey = errors.New(
		"--audit-checkpoints needs a signing key: set --audit-signing-key-file (" + signingKeyEnvVar + ")",
	)
	errKeyWithoutCheckpoints = errors.New(
		"--audit-signing-key-file is set and --audit-checkpoints is off: set --audit-checkpoints=true",
	)
	errCheckpointsNeedChain = errors.New(
		"--audit-checkpoints needs the audit chain: set --audit-chain=on",
	)
	errVerifyKeysWithoutCheckpoints = errors.New(
		"--audit-verify-keys-file is set and --audit-checkpoints is off: set --audit-checkpoints=true",
	)
)

// checkCheckpointFlags checks that checkpoints come with a signing key and
// the chain on, and that a key comes with checkpoints.
func (f *serveFlags) checkCheckpointFlags() error {
	switch {
	case f.auditCheckpoints && f.auditSigningKeyFile == "":
		return errCheckpointsNeedKey
	case !f.auditCheckpoints && f.auditSigningKeyFile != "":
		return errKeyWithoutCheckpoints
	case f.auditCheckpoints && f.auditChain != string(audit.ChainOn):
		return errCheckpointsNeedChain
	case !f.auditCheckpoints && f.auditVerifyKeysFile != "":
		return errVerifyKeysWithoutCheckpoints
	default:
		return nil
	}
}

// checkpointOptions answers the server option of signed checkpoints, or
// none while they are off.
func (f *serveFlags) checkpointOptions() ([]dustpan.Option, error) {
	if !f.auditCheckpoints {
		return nil, nil
	}

	signer, err := readSigningKeyFile(f.auditSigningKeyFile)
	if err != nil {
		return nil, err
	}

	if f.auditVerifyKeysFile != "" {
		if err := addVerifyKeys(signer, f.auditVerifyKeysFile); err != nil {
			return nil, err
		}
	}

	return []dustpan.Option{dustpan.WithAuditCheckpoints(signer)}, nil
}

// addVerifyKeys reads the file at path and adds each key to signer. The file
// holds one base64 Ed25519 public key on each line. An empty line and a
// line that starts with # are skipped. A public key is not a secret, so the
// file needs no mode check.
func addVerifyKeys(signer *audit.Signer, path string) error {
	raw, err := os.ReadFile(path) //nolint:gosec // The operator names the file through a setting.
	if err != nil {
		return fmt.Errorf("%s: read file: %w", verifyKeysEnvVar, err)
	}

	for n, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		pub, err := audit.ParseVerifyKey(line)
		if err != nil {
			return fmt.Errorf("%s: line %d: %w", verifyKeysEnvVar, n+1, err)
		}

		if err := signer.AddVerifyKey(pub); err != nil {
			return fmt.Errorf("%s: line %d: %w", verifyKeysEnvVar, n+1, err)
		}
	}

	return nil
}

// readSigningKeyFile reads the base64 Ed25519 seed from the file at path,
// with the rules of the key-encryption key file (ADR 23 decision 1): the
// file must not be readable by group or others.
func readSigningKeyFile(path string) (*audit.Signer, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: stat file: %w", signingKeyEnvVar, err)
	}

	if perm := info.Mode().Perm(); perm&keyFilePrivateBits != 0 {
		return nil, fmt.Errorf(
			"%s: %w %q (mode %04o): run chmod 600 on the file",
			signingKeyEnvVar,
			errKeyFilePermissions,
			path,
			perm,
		)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // The operator names the key file through a setting.
	if err != nil {
		return nil, fmt.Errorf("%s: read file: %w", signingKeyEnvVar, err)
	}

	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("%s: not valid base64: %w", signingKeyEnvVar, err)
	}

	signer, err := audit.NewSigner(seed)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", signingKeyEnvVar, err)
	}

	return signer, nil
}
