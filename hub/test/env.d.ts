/**
 * Types for the `cloudflare:test` module used by the Workers test runner.
 *
 * `ProvidedEnv` tells the runner what `env` holds inside a test. It is the
 * hub's own Env, so a test that reads a binding gets the same types the Worker
 * does and a typo fails the typecheck rather than the test run.
 */
declare module "cloudflare:test" {
  import type { Env } from "../src/env.js";

  export const env: Env;
  export function createExecutionContext(): ExecutionContext;
  export function waitOnExecutionContext(ctx: ExecutionContext): Promise<void>;
}
