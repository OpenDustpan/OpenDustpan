package sshsig_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/sshsig"
)

// testMessage returns the message of the signatures that the tests build.
func testMessage() []byte {
	return []byte("opendustpan step-up\nchallenge: stc-1\n")
}

// TestVerifyVectors checks the vectors of the real ssh-keygen with the
// functions of the package.
func TestVerifyVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key, sig, namespace string
		want                sshsig.Result
		wantErr             error
	}{
		{"ed25519", "ed25519-sha512", sshsig.NamespaceStepUp, result(false, false, 0), nil},
		{"ed25519", "ed25519-sha256", sshsig.NamespaceStepUp, result(false, false, 0), nil},
		{"ed25519", "ed25519-key-add", sshsig.NamespaceKeyAdd, result(false, false, 0), nil},
		{"ed25519", "ed25519-git", sshsig.NamespaceStepUp, result(false, false, 0), sshsig.ErrNamespace},
		{"ed25519", "ed25519-key-add", sshsig.NamespaceStepUp, result(false, false, 0), sshsig.ErrNamespace},
		{"ed25519", "ed25519-other", sshsig.NamespaceStepUp, result(false, false, 0), sshsig.ErrKeyMismatch},
		{"sk", "sk-presence", sshsig.NamespaceStepUp, result(true, false, 42), nil},
		{
			"sk",
			"sk-verified",
			sshsig.NamespaceStepUp,
			result(true, true, 7),
			nil,
		},
		{"sk", "sk-no-presence", sshsig.NamespaceStepUp, result(false, false, 0), sshsig.ErrUserPresence},
		{"sk", "sk-verified-no-presence", sshsig.NamespaceStepUp, result(false, false, 0), sshsig.ErrUserPresence},
		{"sk", "sk-flag-0x02", sshsig.NamespaceStepUp, result(false, false, 0), sshsig.ErrFlags},
		{"sk", "sk-flag-0x40", sshsig.NamespaceStepUp, result(false, false, 0), sshsig.ErrFlags},
		{"sk", "sk-flag-0x80", sshsig.NamespaceStepUp, result(false, false, 0), sshsig.ErrFlags},
		{"ecdsa-p384", "ecdsa-p384", sshsig.NamespaceStepUp, result(false, false, 0), sshsig.ErrKeyType},
		{"rsa-3072", "rsa-3072", sshsig.NamespaceStepUp, result(false, false, 0), sshsig.ErrKeyType},
	}

	for _, tc := range tests {
		t.Run(tc.sig+" "+tc.namespace, func(t *testing.T) {
			t.Parallel()

			_, wire := publicKey(t, tc.key)

			got, err := sshsig.Verify(wire, tc.namespace, fixture(t, "message"), fixture(t, tc.sig+".sig"))
			if !errors.Is(err, tc.wantErr) || (err == nil) != (tc.wantErr == nil) {
				t.Fatalf("Verify error = %v, want %v", err, tc.wantErr)
			}

			if got != tc.want {
				t.Fatalf("Verify result = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestParseBlob sends hostile blobs to the parser, as if the armor check
// had failed. Each blob is otherwise a valid signature by its key.
func TestParseBlob(t *testing.T) {
	t.Parallel()

	msg := testMessage()

	ed := newKey(t, sshsig.TypeEd25519)
	sk := newKey(t, sshsig.TypeSKEd25519)
	long := strings.Repeat("a", sshsig.MaxNamespaceSize+1)

	tests := []struct {
		name string
		key  testKey
		edit func(s *spec)
		want error
	}{
		{"a valid ed25519 signature", ed, func(*spec) {}, nil},
		{"a valid sk signature", sk, func(*spec) {}, nil},
		{"a wrong magic", ed, func(s *spec) { s.magic = "SSHSIH" }, sshsig.ErrMalformed},
		{"a lowercase magic", ed, func(s *spec) { s.magic = "sshsig" }, sshsig.ErrMalformed},
		{"a short magic", ed, func(s *spec) { s.magic = "SSH" }, sshsig.ErrMalformed},
		{"the version 0", ed, func(s *spec) { s.version = 0 }, sshsig.ErrVersion},
		{"the version 2", ed, func(s *spec) { s.version = 2 }, sshsig.ErrVersion},
		{"the version 0xffffffff", ed, func(s *spec) { s.version = 0xffffffff }, sshsig.ErrVersion},
		{"an empty namespace", ed, func(s *spec) { s.namespace = "" }, sshsig.ErrMalformed},
		{
			"a namespace above the limit",
			ed,
			func(s *spec) { s.namespace, s.signNamespace = long, long },
			sshsig.ErrMalformed,
		},
		{"a namespace with a space", ed, func(s *spec) { s.namespace = "step up" }, sshsig.ErrMalformed},
		{"a namespace with LF", ed, func(s *spec) { s.namespace = "step-up\n@opendustpan.org" }, sshsig.ErrMalformed},
		{
			"a namespace with a NUL",
			ed,
			func(s *spec) { s.namespace = "step-up@opendustpan.org\x00" },
			sshsig.ErrMalformed,
		},
		{"a reserved field that is not empty", ed, func(s *spec) { s.reserved = []byte("x") }, sshsig.ErrMalformed},
		{"the hash sha1", ed, func(s *spec) { s.hash, s.signHash = "sha1", "sha1" }, sshsig.ErrHashAlgorithm},
		{"the hash SHA256", ed, func(s *spec) { s.hash = "SHA256" }, sshsig.ErrHashAlgorithm},
		{"the hash sha384", ed, func(s *spec) { s.hash = "sha384" }, sshsig.ErrHashAlgorithm},
		{"an empty hash", ed, func(s *spec) { s.hash = "" }, sshsig.ErrHashAlgorithm},
		{"a hash above the limit", ed, func(s *spec) { s.hash = strings.Repeat("s", 17) }, sshsig.ErrMalformed},
		{"bytes after the blob", ed, func(s *spec) { s.tail = []byte{0} }, sshsig.ErrMalformed},
		{"a field after the blob", ed, func(s *spec) { s.tail = appendString(nil, []byte("x")) }, sshsig.ErrMalformed},
		{"bytes after the ed25519 signature", ed, func(s *spec) { s.innerTail = []byte{1} }, sshsig.ErrMalformed},
		{"flags after the ed25519 signature", ed, func(s *spec) {
			s.innerTail = []byte{1, 0, 0, 0, 1}
		}, sshsig.ErrMalformed},
		{"bytes after the sk counter", sk, func(s *spec) { s.innerTail = []byte{0} }, sshsig.ErrMalformed},
		{"an sk signature without the counter", sk, func(s *spec) {
			s.inner = appendString(appendString(nil, []byte(sshsig.TypeSKEd25519)), make([]byte, 64))
			s.inner = append(s.inner, sshsig.FlagUserPresent)
		}, sshsig.ErrMalformed},
		{"an sk signature with a truncated counter", sk, func(s *spec) {
			s.inner = appendString(appendString(nil, []byte(sshsig.TypeSKEd25519)), make([]byte, 64))
			s.inner = append(s.inner, sshsig.FlagUserPresent, 0, 0, 1)
		}, sshsig.ErrMalformed},
		{"an sk signature without the flags", sk, func(s *spec) {
			s.inner = appendString(appendString(nil, []byte(sshsig.TypeSKEd25519)), make([]byte, 64))
		}, sshsig.ErrMalformed},
		{"a signature of 63 bytes", ed, func(s *spec) { s.sigBlob = make([]byte, 63) }, sshsig.ErrMalformed},
		{"a signature of 65 bytes", ed, func(s *spec) { s.sigBlob = make([]byte, 65) }, sshsig.ErrMalformed},
		{"an empty signature", ed, func(s *spec) { s.sigBlob = []byte{} }, sshsig.ErrMalformed},
		{"a signature type outside the allowlist", ed, func(s *spec) { s.sigType = "rsa-sha2-512" }, sshsig.ErrKeyType},
		{"a webauthn signature type", sk, func(s *spec) {
			s.sigType = "webauthn-sk-ecdsa-sha2-nistp256@openssh.com"
		}, sshsig.ErrKeyType},
		{"an empty signature field", ed, func(s *spec) { s.inner = []byte{} }, sshsig.ErrMalformed},
		{"a signature field length beyond the end", ed, func(s *spec) {
			s.inner = binary.BigEndian.AppendUint32(nil, 1000)
		}, sshsig.ErrMalformed},
		{"a signature field length of 0xffffffff", ed, func(s *spec) {
			s.inner = binary.BigEndian.AppendUint32(nil, 0xffffffff)
		}, sshsig.ErrMalformed},
		{"a public key above the limit", ed, func(s *spec) { s.publicKey = make([]byte, 513) }, sshsig.ErrMalformed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := tc.key.valid(msg)
			tc.edit(&s)

			_, err := sshsig.ParseBlob(tc.key.blob(s))
			if !errors.Is(err, tc.want) || (err == nil) != (tc.want == nil) {
				t.Fatalf("ParseBlob error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestParseBlobTruncated checks that each prefix of a valid blob is
// malformed, and that a length field that points past the end does not
// read past it.
func TestParseBlobTruncated(t *testing.T) {
	t.Parallel()

	msg := testMessage()

	for _, keyType := range []string{sshsig.TypeEd25519, sshsig.TypeSKEd25519} {
		k := newKey(t, keyType)
		blob := k.blob(k.valid(msg))

		for n := range blob {
			if _, err := sshsig.ParseBlob(blob[:n]); !errors.Is(err, sshsig.ErrMalformed) {
				t.Fatalf("%s: ParseBlob of %d of %d bytes: error = %v, want ErrMalformed", keyType, n, len(blob), err)
			}
		}
	}

	if _, err := sshsig.ParseBlob(make([]byte, sshsig.MaxArmoredSize+1)); !errors.Is(err, sshsig.ErrMalformed) {
		t.Fatalf("ParseBlob above the limit: error = %v, want ErrMalformed", err)
	}
}

// TestParseArmor sends hostile armor to the armor check.
func TestParseArmor(t *testing.T) {
	t.Parallel()

	msg := testMessage()

	k := newKey(t, sshsig.TypeEd25519)
	good := string(k.armored(k.valid(msg)))
	body := strings.TrimSuffix(strings.TrimPrefix(good, "-----BEGIN SSH SIGNATURE-----\n"),
		"-----END SSH SIGNATURE-----\n")
	oneLine := strings.ReplaceAll(body, "\n", "")

	tests := []struct {
		name string
		text string
		want error
	}{
		{"the armor of ssh-keygen", good, nil},
		{"no LF after the end line", strings.TrimSuffix(good, "\n"), sshsig.ErrMalformed},
		{"lines of 76 characters", wrap("-----BEGIN SSH SIGNATURE-----", oneLine, 76), nil},
		{"lines of 77 characters", wrap("-----BEGIN SSH SIGNATURE-----", oneLine, 77), sshsig.ErrMalformed},
		{"two LF after the end line", good + "\n", sshsig.ErrMalformed},
		{"CRLF line ends", strings.ReplaceAll(good, "\n", "\r\n"), sshsig.ErrMalformed},
		{"text before the begin line", "x\n" + good, sshsig.ErrMalformed},
		{"text after the end line", good + "x", sshsig.ErrMalformed},
		{"no end line", strings.TrimSuffix(good, "-----END SSH SIGNATURE-----\n"), sshsig.ErrMalformed},
		{"no begin line", strings.TrimPrefix(good, "-----BEGIN SSH SIGNATURE-----\n"), sshsig.ErrMalformed},
		{"another begin line", strings.Replace(good, "SSH SIGNATURE", "PGP SIGNATURE", 1), sshsig.ErrMalformed},
		{"a header line", strings.Replace(good, "-----\n", "-----\nComment: x\n", 1), sshsig.ErrMalformed},
		{"an empty line", strings.Replace(good, "-----\n", "-----\n\n", 1), sshsig.ErrMalformed},
		{"no body", "-----BEGIN SSH SIGNATURE-----\n-----END SSH SIGNATURE-----\n", sshsig.ErrMalformed},
		{"a space in the body", strings.Replace(good, "-----\n", "-----\n ", 1), sshsig.ErrMalformed},
		{"a URL-safe character", strings.Replace(good, "-----\n", "-----\n-", 1), sshsig.ErrMalformed},
		{
			"a body that is not base64",
			"-----BEGIN SSH SIGNATURE-----\nU1NIU0lH=\n-----END SSH SIGNATURE-----\n",
			sshsig.ErrMalformed,
		},
		{
			"padding bits that are not zero",
			"-----BEGIN SSH SIGNATURE-----\nU1NIU0lHAB==\n-----END SSH SIGNATURE-----\n",
			sshsig.ErrMalformed,
		},
		{"an armor above the size limit", good + strings.Repeat("A", sshsig.MaxArmoredSize), sshsig.ErrMalformed},
		{"an empty input", "", sshsig.ErrMalformed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := sshsig.Parse([]byte(tc.text))
			if !errors.Is(err, tc.want) || (err == nil) != (tc.want == nil) {
				t.Fatalf("Parse error = %v, want %v", err, tc.want)
			}
		})
	}
}

// wrap returns an armor with the base64 body in lines of size characters.
func wrap(begin, body string, size int) string {
	var b strings.Builder

	b.WriteString(begin + "\n")

	for len(body) > size {
		b.WriteString(body[:size] + "\n")
		body = body[size:]
	}

	b.WriteString(body + "\n-----END SSH SIGNATURE-----\n")

	return b.String()
}

// TestParsePublicKey sends hostile keys to the key parser.
func TestParsePublicKey(t *testing.T) {
	t.Parallel()

	ed := newKey(t, sshsig.TypeEd25519).pub
	sk := newKey(t, sshsig.TypeSKEd25519).pub
	key32 := []byte(ed.Key)

	build := func(fields ...[]byte) []byte {
		var b []byte
		for _, f := range fields {
			b = appendString(b, f)
		}

		return b
	}
	ssh := []byte(sshsig.TypeEd25519)
	skType := []byte(sshsig.TypeSKEd25519)

	tests := []struct {
		name string
		wire []byte
		want error
	}{
		{"an ed25519 key", ed.Marshal(), nil},
		{"an sk key", sk.Marshal(), nil},
		{"an sk key with the application ssh:git", build(skType, key32, []byte("ssh:git")), nil},
		{"an sk key with a web application", build(skType, key32, []byte("https://example.com")), sshsig.ErrMalformed},
		{
			"an sk key with the application example.com",
			build(skType, key32, []byte("example.com")),
			sshsig.ErrMalformed,
		},
		{"an sk key with an empty application", build(skType, key32, nil), sshsig.ErrMalformed},
		{"an sk key with LF in the application", build(skType, key32, []byte("ssh:\n")), sshsig.ErrMalformed},
		{"an sk key with an application above the limit", build(skType, key32,
			[]byte("ssh:"+strings.Repeat("a", sshsig.MaxApplicationSize))), sshsig.ErrMalformed},
		{"an sk key without the application", build(skType, key32), sshsig.ErrMalformed},
		{"an ed25519 key with an application", build(ssh, key32, []byte("ssh:")), sshsig.ErrMalformed},
		{"a key of 31 bytes", build(ssh, key32[:31]), sshsig.ErrMalformed},
		{"a key of 33 bytes", build(ssh, append(bytes.Clone(key32), 0)), sshsig.ErrMalformed},
		{"bytes after the key", append(ed.Marshal(), 0), sshsig.ErrMalformed},
		{"an RSA key", build([]byte("ssh-rsa"), []byte{1, 0, 1}, make([]byte, 384)), sshsig.ErrKeyType},
		{"an ECDSA key", build([]byte("ecdsa-sha2-nistp256"), []byte("nistp256"), make([]byte, 65)), sshsig.ErrKeyType},
		{"a certificate", build([]byte("ssh-ed25519-cert-v01@openssh.com"), key32), sshsig.ErrKeyType},
		{"a type above the limit", build([]byte(strings.Repeat("t", 65)), key32), sshsig.ErrMalformed},
		{"a truncated type", ed.Marshal()[:6], sshsig.ErrMalformed},
		{"no bytes", nil, sshsig.ErrMalformed},
		{"a key above the size limit", make([]byte, 513), sshsig.ErrMalformed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			k, err := sshsig.ParsePublicKey(tc.wire)
			if !errors.Is(err, tc.want) || (err == nil) != (tc.want == nil) {
				t.Fatalf("ParsePublicKey error = %v, want %v", err, tc.want)
			}

			if err == nil && !bytes.Equal(k.Marshal(), tc.wire) {
				t.Fatal("Marshal does not give the parsed bytes")
			}
		})
	}
}

// TestVerifyBlob checks signatures that parse but do not verify.
func TestVerifyBlob(t *testing.T) {
	t.Parallel()

	msg := testMessage()

	ed := newKey(t, sshsig.TypeEd25519)
	sk := newKey(t, sshsig.TypeSKEd25519)
	other := newKey(t, sshsig.TypeEd25519)

	tests := []struct {
		name      string
		key       testKey
		edit      func(s *spec)
		namespace string
		message   []byte
		want      error
		// verifyWith is the key of the call to Verify, when it is not key.
		verifyWith *testKey
	}{
		{"a valid ed25519 signature", ed, func(*spec) {}, sshsig.NamespaceStepUp, msg, nil, nil},
		{
			"a valid sha256 signature", ed, func(s *spec) { s.hash, s.signHash = "sha256", "sha256" },
			sshsig.NamespaceStepUp, msg, nil, nil,
		},
		{"a valid sk signature", sk, func(*spec) {}, sshsig.NamespaceStepUp, msg, nil, nil},
		{"another message", ed, func(*spec) {}, sshsig.NamespaceStepUp, []byte("other"), sshsig.ErrVerify, nil},
		{"an empty message", ed, func(*spec) {}, sshsig.NamespaceStepUp, nil, sshsig.ErrVerify, nil},
		{"another namespace", ed, func(*spec) {}, sshsig.NamespaceKeyAdd, msg, sshsig.ErrNamespace, nil},
		{"an empty expected namespace", ed, func(*spec) {}, "", msg, sshsig.ErrNamespace, nil},
		{
			"a namespace that the signature does not cover", ed, func(s *spec) { s.signNamespace = "git" },
			sshsig.NamespaceStepUp, msg, sshsig.ErrVerify, nil,
		},
		{
			"a hash that the signature does not cover", ed, func(s *spec) { s.hash = "sha256" },
			sshsig.NamespaceStepUp, msg, sshsig.ErrVerify, nil,
		},
		{
			"an embedded key of another signer", ed, func(s *spec) { s.publicKey = other.pub.Marshal() },
			sshsig.NamespaceStepUp, msg, sshsig.ErrKeyMismatch, nil,
		},
		{
			"a signature by another key", other, func(s *spec) { s.publicKey = ed.pub.Marshal() },
			sshsig.NamespaceStepUp, msg, sshsig.ErrVerify, &ed,
		},
		{
			"an sk signature type for an ed25519 key", ed, func(s *spec) { s.sigType = sshsig.TypeSKEd25519 },
			sshsig.NamespaceStepUp, msg, sshsig.ErrKeyType, nil,
		},
		{
			"an ed25519 signature type for an sk key", sk, func(s *spec) { s.sigType = sshsig.TypeEd25519 },
			sshsig.NamespaceStepUp, msg, sshsig.ErrKeyType, nil,
		},
		{
			"an sk signature without user presence", sk, func(s *spec) { s.flags, s.signFlags = 0, 0 },
			sshsig.NamespaceStepUp, msg, sshsig.ErrUserPresence, nil,
		},
		{
			"an sk signature with only user verification", sk, func(s *spec) { s.flags, s.signFlags = 0x04, 0x04 },
			sshsig.NamespaceStepUp, msg, sshsig.ErrUserPresence, nil,
		},
		{
			"an sk signature with the flag 0x02", sk, func(s *spec) { s.flags, s.signFlags = 0x03, 0x03 },
			sshsig.NamespaceStepUp, msg, sshsig.ErrFlags, nil,
		},
		{
			"an sk signature with the flag 0x08", sk, func(s *spec) { s.flags, s.signFlags = 0x09, 0x09 },
			sshsig.NamespaceStepUp, msg, sshsig.ErrFlags, nil,
		},
		{
			"an sk signature with the attested data flag", sk, func(s *spec) { s.flags, s.signFlags = 0x41, 0x41 },
			sshsig.NamespaceStepUp, msg, sshsig.ErrFlags, nil,
		},
		{
			"an sk signature with the extension data flag", sk, func(s *spec) { s.flags, s.signFlags = 0x81, 0x81 },
			sshsig.NamespaceStepUp, msg, sshsig.ErrFlags, nil,
		},
		{
			"an sk signature whose flags the key did not sign", sk, func(s *spec) { s.flags = 0x05 },
			sshsig.NamespaceStepUp, msg, sshsig.ErrVerify, nil,
		},
		{"an sk counter that the key did not sign", sk, func(s *spec) {
			s.inner = sk.innerSig(*s)
			s.inner[len(s.inner)-1]++
		}, sshsig.NamespaceStepUp, msg, sshsig.ErrVerify, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := tc.key.valid(msg)
			tc.edit(&s)

			sig, err := sshsig.Parse(tc.key.armored(s))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}

			key := tc.key.pub
			if tc.verifyWith != nil {
				key = tc.verifyWith.pub
			}

			res, err := sig.Verify(key, tc.namespace, tc.message)
			if !errors.Is(err, tc.want) || (err == nil) != (tc.want == nil) {
				t.Fatalf("Verify error = %v, want %v", err, tc.want)
			}

			if err != nil && res != result(false, false, 0) {
				t.Fatalf("Verify returned the result %+v with an error", res)
			}
		})
	}
}

// TestVerifyFields sends signatures and keys that a caller builds to
// Verify, as if the parser had failed. Each check of Verify stops its value
// alone.
func TestVerifyFields(t *testing.T) {
	t.Parallel()

	msg := testMessage()

	ed := newKey(t, sshsig.TypeEd25519)
	sk := newKey(t, sshsig.TypeSKEd25519)

	parsed := func(k testKey) sshsig.Signature {
		sig, err := sshsig.Parse(k.armored(k.valid(msg)))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}

		return sig
	}

	tests := []struct {
		name string
		key  testKey
		edit func(s *sshsig.Signature, k *sshsig.PublicKey)
		want error
	}{
		{"a valid ed25519 signature", ed, func(*sshsig.Signature, *sshsig.PublicKey) {}, nil},
		{"a valid sk signature", sk, func(*sshsig.Signature, *sshsig.PublicKey) {}, nil},
		{
			"the hash md5", ed, func(s *sshsig.Signature, _ *sshsig.PublicKey) { s.HashAlgorithm = "md5" },
			sshsig.ErrHashAlgorithm,
		},
		{
			"an empty hash", ed, func(s *sshsig.Signature, _ *sshsig.PublicKey) { s.HashAlgorithm = "" },
			sshsig.ErrHashAlgorithm,
		},
		{
			"an empty namespace in the signature",
			ed,
			func(s *sshsig.Signature, _ *sshsig.PublicKey) { s.Namespace = "" },
			sshsig.ErrNamespace,
		},
		{"a signature type outside the allowlist", ed, func(s *sshsig.Signature, _ *sshsig.PublicKey) {
			s.Type = "ssh-rsa"
		}, sshsig.ErrKeyType},
		{
			"a short signature", ed, func(s *sshsig.Signature, _ *sshsig.PublicKey) { s.Blob = s.Blob[:10] },
			sshsig.ErrMalformed,
		},
		{"flags on an ed25519 signature", ed, func(s *sshsig.Signature, _ *sshsig.PublicKey) {
			s.Flags = sshsig.FlagUserPresent
		}, sshsig.ErrFlags},
		{
			"a counter on an ed25519 signature", ed, func(s *sshsig.Signature, _ *sshsig.PublicKey) { s.Counter = 1 },
			sshsig.ErrFlags,
		},
		{
			"no embedded key", ed, func(s *sshsig.Signature, _ *sshsig.PublicKey) { s.PublicKey = nil },
			sshsig.ErrKeyMismatch,
		},
		{"a key type outside the allowlist", ed, func(_ *sshsig.Signature, k *sshsig.PublicKey) {
			k.Type = "ssh-rsa"
		}, sshsig.ErrKeyType},
		{
			"a short key", ed, func(_ *sshsig.Signature, k *sshsig.PublicKey) { k.Key = k.Key[:31] },
			sshsig.ErrMalformed,
		},
		{"an application on an ed25519 key", ed, func(_ *sshsig.Signature, k *sshsig.PublicKey) {
			k.Application = "ssh:"
		}, sshsig.ErrMalformed},
		{"a web application on an sk key", sk, func(_ *sshsig.Signature, k *sshsig.PublicKey) {
			k.Application = "https://example.com"
		}, sshsig.ErrMalformed},
		{
			"sk flags outside the allowlist", sk, func(s *sshsig.Signature, _ *sshsig.PublicKey) { s.Flags = 0xff },
			sshsig.ErrFlags,
		},
		{
			"sk flags without user presence", sk, func(s *sshsig.Signature, _ *sshsig.PublicKey) { s.Flags = 0 },
			sshsig.ErrUserPresence,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sig := parsed(tc.key)
			key := tc.key.pub
			key.Key = bytes.Clone(key.Key)
			tc.edit(&sig, &key)

			_, err := sig.Verify(key, sshsig.NamespaceStepUp, msg)
			if !errors.Is(err, tc.want) || (err == nil) != (tc.want == nil) {
				t.Fatalf("Verify error = %v, want %v", err, tc.want)
			}
		})
	}
}
