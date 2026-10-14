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

// signingKeyEnvVar names the file that holds the audit signing key (ADR 60
// decision 18).
const signingKeyEnvVar = "DUSTPAN_AUDIT_SIGNING_KEY_FILE"

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

	return []dustpan.Option{dustpan.WithAuditCheckpoints(signer)}, nil
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
