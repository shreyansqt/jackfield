/**
 * The hub's own records in HUB_KV.
 *
 * Every key carries a prefix, so one namespace holds four record families
 * without collision:
 *
 *   cred:<connection>        the encrypted credential for one connection
 *   device:<tokenHash>       one issued device token, looked up by its hash
 *   deviceindex:<deviceId>   maps a device id to its token hash, for listing
 *   devauth:<deviceCode>     an in-flight device-authorization request
 *   usercode:<userCode>      maps the short user code to its device code
 *   approval:<ticket>        a fresh browser approval that permits one write
 *
 * KV is eventually consistent. That is acceptable for every record here:
 * a credential read that briefly returns a previous value is harmless, and the
 * device flow polls until the record appears. Where a record must not be
 * replayed (the write-approval ticket), the hub deletes it on use and the
 * delete is what carries the meaning, not the read.
 */
import {
  decryptSecret,
  encryptSecret,
  hashToken,
  randomToken,
  randomUserCode,
} from "./crypto.js";

/** How long an unapproved device-authorization request stays valid. */
export const DEVICE_CODE_TTL_SECONDS = 900; // 15 minutes

/** How long a write approval stays usable after the human approves it. */
export const APPROVAL_TTL_SECONDS = 300; // 5 minutes

/** The polling interval the hub asks device clients to respect. */
export const DEVICE_POLL_INTERVAL_SECONDS = 5;

/** A credential for one upstream connection, as stored. */
export interface CredentialRecord {
  /** The connection name, for example "slack-smarta" or "google-personal". */
  connection: string;
  /** The AES-GCM ciphertext. Never the plaintext. */
  ciphertext: string;
  /** Who this credential acts as, for the status panel. Not a secret. */
  identity: string;
  /** Milliseconds since the epoch when this credential was last written. */
  updatedAt: number;
  /** The device or browser session that wrote it, for the audit trail. */
  updatedBy: string;
}

/** One issued device token. The token itself is never stored. */
export interface DeviceRecord {
  /** A stable id for this device, used by `jf devices revoke`. */
  deviceId: string;
  /** The human-chosen name, for example "macbook" or "grumpyorange". */
  name: string;
  /** Milliseconds since the epoch when the token was issued. */
  createdAt: number;
  /** Milliseconds since the epoch of the last successful use, or null. */
  lastUsedAt: number | null;
}

/** An in-flight device-authorization request, before the human approves it. */
export interface DeviceAuthRecord {
  deviceCode: string;
  userCode: string;
  /** The device name the client asked for. */
  requestedName: string;
  /** Milliseconds since the epoch when this request expires. */
  expiresAt: number;
  /** Set once a human approves the request in a browser. */
  approved: boolean;
  /**
   * The device token, held here only between approval and the client's next
   * poll. The hub deletes the whole record as soon as the client collects it,
   * so the plaintext token lives in KV for one polling interval at most.
   */
  issuedToken?: string;
}

/** A fresh browser approval that permits exactly one credential write. */
export interface ApprovalRecord {
  /** The connection this approval covers. An approval is not transferable. */
  connection: string;
  /** The Access or sign-in identity that approved the write. */
  approvedBy: string;
  /** Milliseconds since the epoch when this approval expires. */
  expiresAt: number;
}

/* ------------------------------------------------------------------ */
/* Credentials                                                         */
/* ------------------------------------------------------------------ */

const credKey = (connection: string) => `cred:${connection}`;

/**
 * Writes a credential. The caller must already have checked that a fresh
 * approval exists: this function does not enforce the policy, it only stores.
 */
export async function putCredential(
  kv: KVNamespace,
  masterKey: string,
  input: { connection: string; secret: string; identity: string; updatedBy: string },
): Promise<CredentialRecord> {
  const record: CredentialRecord = {
    connection: input.connection,
    ciphertext: await encryptSecret(masterKey, input.connection, input.secret),
    identity: input.identity,
    updatedAt: Date.now(),
    updatedBy: input.updatedBy,
  };
  await kv.put(credKey(input.connection), JSON.stringify(record));
  return record;
}

/** Reads a credential record without decrypting it. */
export async function getCredentialRecord(
  kv: KVNamespace,
  connection: string,
): Promise<CredentialRecord | null> {
  return kv.get<CredentialRecord>(credKey(connection), "json");
}

/** Reads and decrypts a credential. Returns null when it does not exist. */
export async function getCredentialSecret(
  kv: KVNamespace,
  masterKey: string,
  connection: string,
): Promise<{ record: CredentialRecord; secret: string } | null> {
  const record = await getCredentialRecord(kv, connection);
  if (!record) return null;
  const secret = await decryptSecret(masterKey, connection, record.ciphertext);
  return { record, secret };
}

/** Lists every stored credential, without any secret value. */
export async function listCredentials(kv: KVNamespace): Promise<CredentialRecord[]> {
  const listing = await kv.list({ prefix: "cred:" });
  const records = await Promise.all(
    listing.keys.map((key) => kv.get<CredentialRecord>(key.name, "json")),
  );
  return records.filter((record): record is CredentialRecord => record !== null);
}

/* ------------------------------------------------------------------ */
/* Device tokens                                                       */
/* ------------------------------------------------------------------ */

const deviceKey = (tokenHash: string) => `device:${tokenHash}`;
const deviceIndexKey = (deviceId: string) => `deviceindex:${deviceId}`;

/**
 * Issues a device token and stores its record under the token's hash.
 * The plaintext token is returned to the caller once and never stored.
 */
export async function issueDeviceToken(
  kv: KVNamespace,
  name: string,
): Promise<{ token: string; record: DeviceRecord }> {
  const token = `jfd_${randomToken(32)}`;
  const tokenHash = await hashToken(token);
  const record: DeviceRecord = {
    deviceId: randomToken(8),
    name,
    createdAt: Date.now(),
    lastUsedAt: null,
  };
  await kv.put(deviceKey(tokenHash), JSON.stringify(record));
  await kv.put(deviceIndexKey(record.deviceId), tokenHash);
  return { token, record };
}

/**
 * Looks up the device behind a bearer token.
 * Returns null when the token is unknown or was revoked.
 *
 * Both keys must agree before a token counts as valid. The index key is the
 * authority on whether a device still exists, and `revokeDevice` deletes it
 * first. Checking only the `device:` record would honour a token whose record
 * a concurrent background write recreated after revocation.
 */
export async function lookupDeviceByToken(
  kv: KVNamespace,
  token: string,
): Promise<DeviceRecord | null> {
  const tokenHash = await hashToken(token);
  const record = await kv.get<DeviceRecord>(deviceKey(tokenHash), "json");
  if (!record) return null;
  const indexed = await kv.get(deviceIndexKey(record.deviceId), "text");
  if (indexed !== tokenHash) return null;
  return record;
}

/**
 * Records that a device token was used. The caller passes this to
 * `ctx.waitUntil`, because the read must not wait for the write.
 *
 * The re-read below is not redundant. This runs in the background, after the
 * response was already sent, so the device may have been revoked in between.
 * Writing the record back unconditionally would recreate a revoked device and
 * silently restore its access. The index key is the authority: `revokeDevice`
 * deletes it first, so its absence means "revoked, do not resurrect".
 */
export async function touchDevice(kv: KVNamespace, token: string): Promise<void> {
  const tokenHash = await hashToken(token);
  const record = await kv.get<DeviceRecord>(deviceKey(tokenHash), "json");
  if (!record) return;
  const stillPresent = await kv.get(deviceIndexKey(record.deviceId), "text");
  if (stillPresent !== tokenHash) return;
  record.lastUsedAt = Date.now();
  await kv.put(deviceKey(tokenHash), JSON.stringify(record));
}

/**
 * Lists every issued device token, for `jf devices`.
 *
 * The listing is driven from the index keys, not from the `device:` records,
 * for the same reason `lookupDeviceByToken` checks the index: a revoked device
 * must not reappear in the list if a background write recreated its record.
 */
export async function listDevices(kv: KVNamespace): Promise<DeviceRecord[]> {
  const listing = await kv.list({ prefix: "deviceindex:" });
  const records = await Promise.all(
    listing.keys.map(async (key) => {
      const tokenHash = await kv.get(key.name, "text");
      if (!tokenHash) return null;
      return kv.get<DeviceRecord>(deviceKey(tokenHash), "json");
    }),
  );
  return records.filter((record): record is DeviceRecord => record !== null);
}

/**
 * Revokes one device token by its device id.
 * Returns true when a device was found and removed.
 */
export async function revokeDevice(kv: KVNamespace, deviceId: string): Promise<boolean> {
  const tokenHash = await kv.get(deviceIndexKey(deviceId), "text");
  if (!tokenHash) return false;
  await kv.delete(deviceKey(tokenHash));
  await kv.delete(deviceIndexKey(deviceId));
  return true;
}

/* ------------------------------------------------------------------ */
/* The device-authorization flow (RFC 8628)                            */
/* ------------------------------------------------------------------ */

const devAuthKey = (deviceCode: string) => `devauth:${deviceCode}`;
const userCodeKey = (userCode: string) => `usercode:${userCode}`;

/** Starts a device-authorization request and returns both codes. */
export async function startDeviceAuth(
  kv: KVNamespace,
  requestedName: string,
): Promise<DeviceAuthRecord> {
  const record: DeviceAuthRecord = {
    deviceCode: randomToken(32),
    userCode: randomUserCode(),
    requestedName,
    expiresAt: Date.now() + DEVICE_CODE_TTL_SECONDS * 1000,
    approved: false,
  };
  // KV expires both keys on its own, so an abandoned request cleans itself up.
  await kv.put(devAuthKey(record.deviceCode), JSON.stringify(record), {
    expirationTtl: DEVICE_CODE_TTL_SECONDS,
  });
  await kv.put(userCodeKey(record.userCode), record.deviceCode, {
    expirationTtl: DEVICE_CODE_TTL_SECONDS,
  });
  return record;
}

/** Finds a device-authorization request by its device code. */
export async function getDeviceAuth(
  kv: KVNamespace,
  deviceCode: string,
): Promise<DeviceAuthRecord | null> {
  return kv.get<DeviceAuthRecord>(devAuthKey(deviceCode), "json");
}

/** Finds a device-authorization request by the short code the human types. */
export async function getDeviceAuthByUserCode(
  kv: KVNamespace,
  userCode: string,
): Promise<DeviceAuthRecord | null> {
  const deviceCode = await kv.get(userCodeKey(userCode.toUpperCase()), "text");
  if (!deviceCode) return null;
  return getDeviceAuth(kv, deviceCode);
}

/**
 * Approves a device-authorization request and mints its token.
 * The token waits in the record until the client's next poll collects it.
 */
export async function approveDeviceAuth(
  kv: KVNamespace,
  record: DeviceAuthRecord,
  name: string,
): Promise<DeviceRecord> {
  const { token, record: device } = await issueDeviceToken(kv, name);
  const approved: DeviceAuthRecord = {
    ...record,
    approved: true,
    requestedName: name,
    issuedToken: token,
  };
  await kv.put(devAuthKey(record.deviceCode), JSON.stringify(approved), {
    expirationTtl: DEVICE_CODE_TTL_SECONDS,
  });
  return device;
}

/**
 * Removes a device-authorization request once its token was collected.
 * This is what stops one approval from yielding two tokens.
 */
export async function consumeDeviceAuth(
  kv: KVNamespace,
  record: DeviceAuthRecord,
): Promise<void> {
  await kv.delete(devAuthKey(record.deviceCode));
  await kv.delete(userCodeKey(record.userCode));
}

/* ------------------------------------------------------------------ */
/* Write approvals                                                     */
/* ------------------------------------------------------------------ */

const approvalKey = (ticket: string) => `approval:${ticket}`;

/**
 * Records that a human approved a write to one connection, in a browser, now.
 * The returned ticket is the proof the write path demands.
 */
export async function createApproval(
  kv: KVNamespace,
  connection: string,
  approvedBy: string,
): Promise<string> {
  const ticket = randomToken(32);
  const record: ApprovalRecord = {
    connection,
    approvedBy,
    expiresAt: Date.now() + APPROVAL_TTL_SECONDS * 1000,
  };
  await kv.put(approvalKey(ticket), JSON.stringify(record), {
    expirationTtl: APPROVAL_TTL_SECONDS,
  });
  return ticket;
}

/**
 * Spends an approval ticket. The ticket is deleted whether or not it matched,
 * so a ticket can never authorise two writes and a wrong guess cannot be
 * retried against a different connection.
 *
 * Returns the record when the ticket was valid and covered `connection`.
 */
export async function consumeApproval(
  kv: KVNamespace,
  ticket: string,
  connection: string,
): Promise<ApprovalRecord | null> {
  const record = await kv.get<ApprovalRecord>(approvalKey(ticket), "json");
  if (!record) return null;
  await kv.delete(approvalKey(ticket));
  if (record.connection !== connection) return null;
  if (record.expiresAt < Date.now()) return null;
  return record;
}
