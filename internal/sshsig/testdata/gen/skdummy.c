/*
 * skdummy is a software FIDO middleware for ssh-keygen (SSH_SK_PROVIDER).
 * It exists only to make the sk-ssh-ed25519@openssh.com test vectors of
 * package sshsig with the real ssh-keygen. It is not a security key: the key
 * handle holds the private key in clear text.
 *
 * The environment variables SKDUMMY_FLAGS and SKDUMMY_COUNTER set the flags
 * and the counter of a signature, so that a vector can hold flags that a
 * real authenticator sets, or flags that the verifier must refuse.
 *
 * The structures follow sk-api.h of OpenSSH (API version 0x000a0000).
 */
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <openssl/evp.h>
#include <openssl/sha.h>

#define SSH_SK_ED25519 0x01
#define SSH_SK_VERSION_MAJOR 0x000a0000
#define SSH_SK_ERR_GENERAL -1
#define SSH_SK_ERR_UNSUPPORTED -2

struct sk_enroll_response {
	uint8_t flags;
	uint8_t *public_key;
	size_t public_key_len;
	uint8_t *key_handle;
	size_t key_handle_len;
	uint8_t *signature;
	size_t signature_len;
	uint8_t *attestation_cert;
	size_t attestation_cert_len;
	uint8_t *authdata;
	size_t authdata_len;
};

struct sk_sign_response {
	uint8_t flags;
	uint32_t counter;
	uint8_t *sig_r;
	size_t sig_r_len;
	uint8_t *sig_s;
	size_t sig_s_len;
};

struct sk_resident_key;
struct sk_option;

uint32_t sk_api_version(void) { return SSH_SK_VERSION_MAJOR; }

int sk_enroll(uint32_t alg, const uint8_t *challenge, size_t challenge_len,
    const char *application, uint8_t flags, const char *pin,
    struct sk_option **options, struct sk_enroll_response **resp)
{
	EVP_PKEY *pkey = NULL;
	struct sk_enroll_response *r;
	size_t len;

	(void)challenge; (void)challenge_len; (void)application; (void)pin;
	(void)options;
	if (alg != SSH_SK_ED25519)
		return SSH_SK_ERR_UNSUPPORTED;
	if ((r = calloc(1, sizeof(*r))) == NULL)
		return SSH_SK_ERR_GENERAL;
	if ((pkey = EVP_PKEY_Q_keygen(NULL, NULL, "ED25519")) == NULL)
		return SSH_SK_ERR_GENERAL;
	r->flags = flags;
	r->public_key_len = len = 32;
	r->public_key = malloc(len);
	r->key_handle_len = 32;
	r->key_handle = malloc(32);
	if (EVP_PKEY_get_raw_public_key(pkey, r->public_key, &len) != 1)
		return SSH_SK_ERR_GENERAL;
	len = 32;
	if (EVP_PKEY_get_raw_private_key(pkey, r->key_handle, &len) != 1)
		return SSH_SK_ERR_GENERAL;
	EVP_PKEY_free(pkey);
	*resp = r;
	return 0;
}

int sk_sign(uint32_t alg, const uint8_t *data, size_t data_len,
    const char *application, const uint8_t *key_handle, size_t key_handle_len,
    uint8_t flags, const char *pin, struct sk_option **options,
    struct sk_sign_response **resp)
{
	struct sk_sign_response *r;
	uint8_t tbs[32 + 1 + 4 + 32];
	uint32_t counter = 1;
	EVP_PKEY *pkey;
	EVP_MD_CTX *ctx;
	const char *v;
	size_t siglen = 64;

	(void)pin; (void)options;
	if (alg != SSH_SK_ED25519 || key_handle_len != 32)
		return SSH_SK_ERR_UNSUPPORTED;
	if ((r = calloc(1, sizeof(*r))) == NULL)
		return SSH_SK_ERR_GENERAL;
	/* A real authenticator sets user presence when the key asks for it. */
	r->flags = flags & 0x01;
	if ((v = getenv("SKDUMMY_FLAGS")) != NULL)
		r->flags = (uint8_t)strtoul(v, NULL, 0);
	if ((v = getenv("SKDUMMY_COUNTER")) != NULL)
		counter = (uint32_t)strtoul(v, NULL, 0);
	r->counter = counter;

	SHA256((const uint8_t *)application, strlen(application), tbs);
	tbs[32] = r->flags;
	tbs[33] = counter >> 24;
	tbs[34] = counter >> 16;
	tbs[35] = counter >> 8;
	tbs[36] = counter;
	SHA256(data, data_len, tbs + 37);

	pkey = EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519, NULL,
	    key_handle, key_handle_len);
	ctx = EVP_MD_CTX_new();
	r->sig_r = malloc(64);
	r->sig_r_len = 64;
	if (pkey == NULL || ctx == NULL ||
	    EVP_DigestSignInit(ctx, NULL, NULL, NULL, pkey) != 1 ||
	    EVP_DigestSign(ctx, r->sig_r, &siglen, tbs, sizeof(tbs)) != 1)
		return SSH_SK_ERR_GENERAL;
	EVP_MD_CTX_free(ctx);
	EVP_PKEY_free(pkey);
	*resp = r;
	return 0;
}

int sk_load_resident_keys(const char *pin, struct sk_option **options,
    struct sk_resident_key ***rks, size_t *nrks)
{
	(void)pin; (void)options; (void)rks; (void)nrks;
	return SSH_SK_ERR_UNSUPPORTED;
}
