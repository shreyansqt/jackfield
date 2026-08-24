/**
 * The hub's bindings and configuration.
 *
 * `wrangler types` regenerates a fuller version of this from wrangler.jsonc.
 * This file stays hand-written for now because the project has no generated
 * `worker-configuration.d.ts` checked in, and the binding set is small enough
 * to read at a glance. Run `npm run cf-typegen` after you change bindings and
 * reconcile any difference.
 */
export interface Env {
  /** The OAuth provider library's own storage. The library owns every key. */
  OAUTH_KV: KVNamespace;

  /** Everything the hub writes itself. See src/storage.ts for the key shapes. */
  HUB_KV: KVNamespace;

  /** The hub's public origin, for example "https://hub.example.com". */
  HUB_ORIGIN: string;

  /** "true" once Cloudflare Access sits in front of the browser endpoints. */
  ACCESS_ENABLED: string;

  /** The Access team domain, for example "myteam.cloudflareaccess.com". */
  ACCESS_TEAM_DOMAIN: string;

  /** The Access application audience tag. */
  ACCESS_AUD: string;

  /** Base64 of 32 random bytes. Encrypts every stored credential. */
  CRED_MASTER_KEY: string;

  /** Development stand-in for an Access identity. Unused once Access is on. */
  DEV_SIGNIN_TOKEN?: string;
}
