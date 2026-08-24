/**
 * Cryptography for the hub.
 *
 * Two separate jobs live here.
 *
 * 1. Credential encryption. Credentials are encrypted with AES-GCM before they
 *    reach KV. The key comes from the CRED_MASTER_KEY secret, which is a
 *    deploy-time Worker secret. KV therefore never holds a readable secret.
 *
 * 2. Token hashing. Device tokens are shown to the caller once. The hub stores
 *    only the SHA-256 hash. A reader of the KV namespace cannot recover a
 *    usable token.
 */

const AES_GCM_IV_BYTES = 12;

/** Encodes bytes as base64url, which is safe in a URL and in a KV key. */
export function toBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** Decodes base64url back to bytes. */
export function fromBase64Url(text: string): Uint8Array {
  const padded = text.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded.padEnd(Math.ceil(padded.length / 4) * 4, "="));
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/**
 * Returns `byteLength` cryptographically random bytes as a base64url string.
 * Every secret value the hub generates comes from here. Math.random is never
 * used, because its output is predictable.
 */
export function randomToken(byteLength = 32): string {
  const bytes = new Uint8Array(byteLength);
  crypto.getRandomValues(bytes);
  return toBase64Url(bytes);
}

/**
 * Generates a short user code for the device flow, in the RFC 8628 style.
 * The alphabet omits the characters that people read wrong: 0, O, 1, I, L, U.
 * The result looks like "BCDF-GHJK".
 */
export function randomUserCode(): string {
  const alphabet = "BCDFGHJKMNPQRSTVWXYZ23456789";
  const bytes = new Uint8Array(8);
  crypto.getRandomValues(bytes);
  let code = "";
  for (let i = 0; i < 8; i++) {
    // `bytes[i]` is defined because the loop stays inside the array length.
    code += alphabet[bytes[i]! % alphabet.length];
    if (i === 3) code += "-";
  }
  return code;
}

/** Returns the SHA-256 hash of `value` as base64url. */
export async function hashToken(value: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return toBase64Url(new Uint8Array(digest));
}

/**
 * Compares two strings without leaking their content through timing.
 * Both values are hashed first, so the comparison always runs over 32 bytes
 * even when the inputs have different lengths. `timingSafeEqual` throws when
 * the two buffers differ in length, and hashing removes that case.
 */
export async function timingSafeEqualStrings(a: string, b: string): Promise<boolean> {
  const encoder = new TextEncoder();
  const [digestA, digestB] = await Promise.all([
    crypto.subtle.digest("SHA-256", encoder.encode(a)),
    crypto.subtle.digest("SHA-256", encoder.encode(b)),
  ]);
  return crypto.subtle.timingSafeEqual(digestA, digestB);
}

/** Imports the master key from its base64 secret. */
async function importMasterKey(masterKeyBase64: string): Promise<CryptoKey> {
  const raw = fromBase64Url(masterKeyBase64);
  if (raw.byteLength !== 32) {
    throw new Error("CRED_MASTER_KEY must decode to exactly 32 bytes");
  }
  return crypto.subtle.importKey("raw", raw, { name: "AES-GCM" }, false, [
    "encrypt",
    "decrypt",
  ]);
}

/**
 * Encrypts `plaintext` with AES-GCM.
 *
 * `connection` is bound in as additional authenticated data. The ciphertext of
 * one connection therefore cannot be moved to another connection's KV key: the
 * decryption fails instead of returning the wrong secret quietly.
 *
 * The returned string is "iv.ciphertext", both base64url.
 */
export async function encryptSecret(
  masterKeyBase64: string,
  connection: string,
  plaintext: string,
): Promise<string> {
  const key = await importMasterKey(masterKeyBase64);
  const iv = new Uint8Array(AES_GCM_IV_BYTES);
  crypto.getRandomValues(iv);
  const ciphertext = await crypto.subtle.encrypt(
    { name: "AES-GCM", iv, additionalData: new TextEncoder().encode(connection) },
    key,
    new TextEncoder().encode(plaintext),
  );
  return `${toBase64Url(iv)}.${toBase64Url(new Uint8Array(ciphertext))}`;
}

/**
 * Decrypts a value produced by `encryptSecret`.
 * Throws when the master key is wrong, the data was changed, or `connection`
 * does not match the one used at encryption time.
 */
export async function decryptSecret(
  masterKeyBase64: string,
  connection: string,
  stored: string,
): Promise<string> {
  const separator = stored.indexOf(".");
  if (separator < 0) throw new Error("stored credential is malformed");
  const iv = fromBase64Url(stored.slice(0, separator));
  const ciphertext = fromBase64Url(stored.slice(separator + 1));
  const key = await importMasterKey(masterKeyBase64);
  const plaintext = await crypto.subtle.decrypt(
    { name: "AES-GCM", iv, additionalData: new TextEncoder().encode(connection) },
    key,
    ciphertext,
  );
  return new TextDecoder().decode(plaintext);
}
