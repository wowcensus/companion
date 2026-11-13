export interface Env {
  UPLOADS: R2Bucket;
  DB: D1Database;
  IP_LIMITER: RateLimit;
  DEVICE_LIMITER: RateLimit;
  ADMIN_TOKEN: string;
  INGEST_TOKEN: string;
  IP_HASH_SALT: string;
  REGISTRATIONS_PER_IP_PER_DAY: string;
  MAX_ZIP_BYTES: string;
  MAX_LUA_BYTES: string;
}

export function json(body: unknown, status = 200, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json", "cache-control": "no-store", ...headers },
  });
}

export const fail = (status: number, error: string, headers?: Record<string, string>) => json({ error }, status, headers);

export async function sha256Hex(s: string): Promise<string> {
  const d = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(s));
  return [...new Uint8Array(d)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export async function secretEquals(a: string, b: string): Promise<boolean> {
  if (!a || !b) return false;
  const [ha, hb] = await Promise.all([
    crypto.subtle.digest("SHA-256", new TextEncoder().encode(a)),
    crypto.subtle.digest("SHA-256", new TextEncoder().encode(b)),
  ]);
  return crypto.subtle.timingSafeEqual(ha, hb);
}

export function bearer(req: Request): string | null {
  const m = /^Bearer ([A-Za-z0-9._~+/=-]{16,512})$/.exec(req.headers.get("authorization") ?? "");
  return m ? m[1] : null;
}

export function randomToken(bytes = 32): string {
  const b = crypto.getRandomValues(new Uint8Array(bytes));
  return btoa(String.fromCharCode(...b)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export const now = () => new Date().toISOString();
export const HEX64 = /^[0-9a-f]{64}$/;
