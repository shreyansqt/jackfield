/** Small helpers for building responses. Kept separate to keep routes short. */

/** Returns a JSON response. */
export function json(body: unknown, status = 200, headers: HeadersInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

/**
 * Returns an OAuth-style error response.
 * The device flow depends on these exact `error` values, so the shape follows
 * RFC 6749 section 5.2 and RFC 8628 section 3.5.
 */
export function oauthError(error: string, description: string, status = 400): Response {
  return json({ error, error_description: description }, status);
}

/** Returns an HTML page. */
export function html(body: string, status = 200, headers: HeadersInit = {}): Response {
  return new Response(body, {
    status,
    headers: { "Content-Type": "text/html; charset=utf-8", ...headers },
  });
}

/**
 * Returns a hidden form field that carries the development sign-in token, or
 * an empty string when there is nothing to carry.
 *
 * A browser holds the token in the URL. A form submission does not inherit the
 * query string, so without this field the POST arrives with no identity and
 * the approval is refused. Re-embedding it keeps the development flow usable.
 *
 * This puts the shared development secret into the HTML of a page that the
 * same secret already unlocked, so it reveals nothing to a reader who could
 * not already see it. It returns an empty string once Access is on, so the
 * field never appears in a real deployment. DESIGN.md section 6 records this.
 */
export function devTokenField(token: string | null): string {
  if (!token) return "";
  return `\n  <input type="hidden" name="dev_token" value="${escapeHtml(token)}">`;
}

/** Escapes text for safe inclusion in HTML. */
export function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

/**
 * Wraps page content in the hub's minimal chrome.
 * The hub serves a handful of pages and none of them needs a framework, so the
 * styling stays inline and small.
 */
export function page(title: string, body: string): string {
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${escapeHtml(title)}</title>
<style>
  body { font-family: ui-sans-serif, system-ui, sans-serif; max-width: 34rem;
         margin: 3rem auto; padding: 0 1.25rem; line-height: 1.5; }
  code, .code { font-family: ui-monospace, monospace; }
  .code { display: inline-block; font-size: 1.5rem; letter-spacing: 0.15em;
          padding: 0.5rem 0.75rem; border: 1px solid #ccc; border-radius: 6px; }
  button { font: inherit; padding: 0.5rem 1rem; border-radius: 6px;
           border: 1px solid #888; background: #f6f6f6; cursor: pointer; }
  input[type=text] { font: inherit; padding: 0.5rem; width: 100%;
                     box-sizing: border-box; }
  label { display: block; margin: 0.75rem 0 0.25rem; }
  .warn { color: #8a5300; }
</style>
</head>
<body>
${body}
</body>
</html>`;
}
