import { cloudflareTest } from "@cloudflare/vitest-plugin";
import { defineConfig } from "vitest/config";

/**
 * The tests run inside the real Workers runtime, not a Node mock, so the KV
 * and Web Crypto behaviour under test is the behaviour that ships.
 *
 * The bindings below override wrangler.jsonc for tests: the KV namespace ids
 * in that file are placeholders for a real account, and Miniflare supplies
 * local namespaces instead.
 */
export default defineConfig({
  plugins: [
    cloudflareTest({
      wrangler: { configPath: "./wrangler.jsonc" },
      miniflare: {
        kvNamespaces: ["OAUTH_KV", "HUB_KV"],
        bindings: {
          HUB_ORIGIN: "https://hub.test",
          ACCESS_ENABLED: "false",
          ACCESS_TEAM_DOMAIN: "",
          ACCESS_AUD: "",
          // A fixed 32-byte key, base64. Test data only; never a real key.
          CRED_MASTER_KEY: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
          DEV_SIGNIN_TOKEN: "test-dev-signin-token",
        },
      },
    }),
  ],
});
