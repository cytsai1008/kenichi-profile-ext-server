#!/usr/bin/env node
/**
 * gen-keypair.mjs
 *
 * Generates an Ed25519 keypair for signing gallery sync requests.
 *
 * Usage:
 *   node scripts/gen-keypair.mjs
 *
 * Output (copy each value to the appropriate config):
 *   GALLERY_SIGNING_KEY   → set in the build / sync environment (CI secret)
 *   GALLERY_ED25519_PUBLIC_KEY → set on the Go server (GALLERY_ED25519_PUBLIC_KEY env var)
 */

const { subtle } = globalThis.crypto;

const keypair = await subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"]);

// Export private key as PKCS8, then extract the 32-byte seed.
const pkcs8 = Buffer.from(await subtle.exportKey("pkcs8", keypair.privateKey));
// The seed is the last 32 bytes of the PKCS8 DER structure.
const seed = pkcs8.slice(-32);

// Export public key as SPKI, then extract the 32-byte key.
const spki = Buffer.from(await subtle.exportKey("spki", keypair.publicKey));
const pub = spki.slice(-32);

// The signing key accepted by gallery-explicit-sync.mjs is the 32-byte seed,
// or optionally the 64-byte seed+pub concatenation.
const signingKey = Buffer.concat([seed, pub]);

console.log("# Copy these values to their respective config locations.\n");
console.log(`GALLERY_SIGNING_KEY=${signingKey.toString("base64")}`);
console.log(`GALLERY_ED25519_PUBLIC_KEY=${pub.toString("base64")}`);
