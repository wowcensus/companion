import { bearer, fail, json, now, randomToken, secretEquals, sha256Hex, type Env } from "./util";

export type Device = { id: string };

export async function authenticateDevice(req: Request, env: Env, ctx: ExecutionContext): Promise<Device | Response> {
  const ip = req.headers.get("cf-connecting-ip") ?? "unknown";
  if (!(await env.IP_LIMITER.limit({ key: ip })).success) return fail(429, "Too many requests", { "retry-after": "60" });

  const token = bearer(req);
  if (!token) return fail(401, "Missing device token");
  const row = await env.DB.prepare("SELECT id, revoked_at FROM devices WHERE token_sha256 = ?")
    .bind(await sha256Hex(token))
    .first<{ id: string; revoked_at: string | null }>();
  if (!row) return fail(401, "Unknown device token");
  if (row.revoked_at) return fail(403, "This device has been revoked");

  if (!(await env.DEVICE_LIMITER.limit({ key: row.id })).success) {
    return fail(429, "Upload rate limit reached", { "retry-after": "60" });
  }
  ctx.waitUntil(env.DB.prepare("UPDATE devices SET last_seen_at = ? WHERE id = ?").bind(now(), row.id).run());
  return { id: row.id };
}

export async function requireSecret(req: Request, secret: string | undefined): Promise<Response | null> {
  const token = bearer(req);
  if (!secret || !token || !(await secretEquals(token, secret))) return fail(401, "Unauthorized");
  return null;
}

export async function createDevice(env: Env, label: string | null, regIpHash: string | null, clientId: string | null = null): Promise<{ deviceId: string; token: string }> {
  const token = randomToken();
  const deviceId = "dev_" + (await sha256Hex("device:" + crypto.randomUUID())).slice(0, 20);
  await env.DB.prepare("INSERT INTO devices (id, token_sha256, label, created_at, reg_ip_hash, client_id) VALUES (?, ?, ?, ?, ?, ?)")
    .bind(deviceId, await sha256Hex(token), label, now(), regIpHash, clientId)
    .run();
  return { deviceId, token };
}

export async function registerDevice(req: Request, env: Env): Promise<Response> {
  const ip = req.headers.get("cf-connecting-ip") ?? "unknown";
  if (!(await env.IP_LIMITER.limit({ key: ip })).success) return fail(429, "Too many requests", { "retry-after": "60" });
  const ipHash = await sha256Hex(env.IP_HASH_SALT + ":" + ip);
  const since = new Date(Date.now() - 24 * 3600 * 1000).toISOString();
  const recent = await env.DB.prepare("SELECT count(*) AS n FROM devices WHERE reg_ip_hash = ? AND created_at > ?")
    .bind(ipHash, since)
    .first<{ n: number }>();
  if ((recent?.n ?? 0) >= Number(env.REGISTRATIONS_PER_IP_PER_DAY)) {
    return fail(429, "Too many registrations from this network today", { "retry-after": "3600" });
  }
  const body = (await req.json().catch(() => ({}))) as { appVersion?: unknown; os?: unknown; clientId?: unknown };
  const clientId = typeof body.clientId === "string" && /^[0-9a-f]{32}$/.test(body.clientId) ? body.clientId : null;
  const clean = (v: unknown) => (typeof v === "string" ? v.replace(/[^\w.+-]/g, "").slice(0, 40) : "");
  const label = ["companion", clean(body.appVersion), clean(body.os)].filter(Boolean).join(" ");
  return json(await createDevice(env, label, ipHash, clientId), 201);
}
