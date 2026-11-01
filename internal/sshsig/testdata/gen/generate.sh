#!/bin/sh
# generate.sh makes the test vectors of package sshsig with the real
# ssh-keygen. Run it from this directory:
#
#	sh generate.sh
#
# It needs ssh-keygen (OpenSSH 8.2 or later), a C compiler and the libcrypto
# headers of OpenSSL 3. It makes each key in a temporary directory and
# deletes the directory, so that no private key goes into the repository.
# The vectors were made with OpenSSH_9.6p1 and OpenSSL 3.0.
set -eu

out=$(cd .. && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cc -shared -fPIC -Wall -o "$tmp/skdummy.so" skdummy.c -lcrypto
SSH_SK_PROVIDER="$tmp/skdummy.so"
export SSH_SK_PROVIDER

stepup=step-up@opendustpan.org
keyadd=step-up-key-add@opendustpan.org

printf 'opendustpan step-up\norigin: https://dustpan.example.com\nchallenge: stc-0123456789abcdef\n' >"$out/message"
cp "$out/message" "$tmp/message"

# keygen TYPE NAME [OPTIONS...] makes a key with no passphrase and no comment.
keygen() {
	t=$1 n=$2
	shift 2
	ssh-keygen -q -t "$t" -N '' -C '' -f "$tmp/$n" "$@"
	cp "$tmp/$n.pub" "$out/$n.pub"
}

# sign KEY NAMESPACE VECTOR [OPTIONS...] signs the message.
sign() {
	k=$1 ns=$2 v=$3
	shift 3
	rm -f "$tmp/message.sig"
	ssh-keygen -q -Y sign -f "$tmp/$k" -n "$ns" "$@" "$tmp/message" 2>/dev/null
	cp "$tmp/message.sig" "$out/$v.sig"
}

# check KEY NAMESPACE VECTOR checks a vector with ssh-keygen -Y verify.
check() {
	printf 'p %s\n' "$(cut -d' ' -f1,2 "$out/$1.pub")" >"$tmp/allowed"
	ssh-keygen -Y verify -f "$tmp/allowed" -I p -n "$2" -s "$out/$3.sig" <"$out/message"
}

keygen ed25519 ed25519
keygen ed25519 ed25519-other
keygen ed25519-sk sk
keygen ecdsa ecdsa-p384 -b 384
keygen rsa rsa-2048 -b 2048
keygen rsa rsa-3072 -b 3072

sign ed25519 "$stepup" ed25519-sha512
sign ed25519 "$stepup" ed25519-sha256 -O hashalg=sha256
sign ed25519 "$keyadd" ed25519-key-add
sign ed25519 git ed25519-git
sign ed25519-other "$stepup" ed25519-other
sign ecdsa-p384 "$stepup" ecdsa-p384
sign rsa-2048 "$stepup" rsa-2048
sign rsa-3072 "$stepup" rsa-3072

SKDUMMY_COUNTER=42 sign sk "$stepup" sk-presence
SKDUMMY_COUNTER=7 SKDUMMY_FLAGS=0x05 sign sk "$stepup" sk-verified
SKDUMMY_COUNTER=0 sign sk "$stepup" sk-counter-zero
SKDUMMY_COUNTER=0xffffffff sign sk "$stepup" sk-counter-max
SKDUMMY_COUNTER=43 sign sk "$keyadd" sk-key-add
SKDUMMY_COUNTER=44 SKDUMMY_FLAGS=0x00 sign sk "$stepup" sk-no-presence
SKDUMMY_COUNTER=45 SKDUMMY_FLAGS=0x04 sign sk "$stepup" sk-verified-no-presence
SKDUMMY_COUNTER=46 SKDUMMY_FLAGS=0x03 sign sk "$stepup" sk-flag-0x02
SKDUMMY_COUNTER=47 SKDUMMY_FLAGS=0x41 sign sk "$stepup" sk-flag-0x40
SKDUMMY_COUNTER=48 SKDUMMY_FLAGS=0x81 sign sk "$stepup" sk-flag-0x80

# The real ssh-keygen accepts each vector that package sshsig accepts.
check ed25519 "$stepup" ed25519-sha512
check ed25519 "$stepup" ed25519-sha256
check ed25519 "$keyadd" ed25519-key-add
check sk "$stepup" sk-presence
check sk "$stepup" sk-verified
check sk "$stepup" sk-counter-zero
check sk "$stepup" sk-counter-max
check sk "$keyadd" sk-key-add

# ssh-keygen -Y verify also accepts sk-no-presence: an allowed signers file
# has no option that requires user presence. Package sshsig refuses it.
check sk "$stepup" sk-no-presence
