import { authenticateDevice } from "./auth";
import { HEX64, fail, json, now, type Env } from "./util";

type ReceiptRow = {
  id: string;
  status: "stored" | "queued" | "imported" | "failed";
};

const H = {
  source: "x-wowcensus-source-id",
  sha: "x-wowcensus-sha256",
  zipSha: "x-wowcensus-zip-sha256",
  luaBytes: "x-wowcensus-lua-bytes",
  edition: "x-wowcensus-edition",
  version: "x-wowcensus-app-version",
  client: "x-wowcensus-client-id",
};

const CLIENT_ID = /^[0-9a-f]{32}$/;

export async function handleUpload(req: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
  const device = await authenticateDevice(req, env, ctx);
  if (device instanceof Response) return device;

  const h = (k: string) => (req.headers.get(k) ?? "").trim();
  const sourceId = h(H.source).toLowerCase();
  const sha256 = h(H.sha).toLowerCase();
  const zipSha256 = h(H.zipSha).toLowerCase();
  const clientId = h(H.client).toLowerCase() || null;
  const edition = h(H.edition);
  const appVersion = h(H.version);
  const luaBytes = Number(h(H.luaBytes));
  const zipBytes = Number(req.headers.get("content-length"));

  if (!HEX64.test(sourceId) || !HEX64.test(sha256) || !HEX64.test(zipSha256)) return fail(400, "Source ID and hashes must be 64 hex characters");
  if (clientId !== null && !CLIENT_ID.test(clientId)) return fail(400, "Invalid client ID");
  if (!/^[a-z0-9_]{1,40}$/.test(edition)) return fail(400, "Invalid edition");
  if (!/^[\w.+-]{0,40}$/.test(appVersion)) return fail(400, "Invalid app version");
  if (!Number.isSafeInteger(luaBytes) || luaBytes <= 0) return fail(400, "Invalid Lua size");
  if (!Number.isSafeInteger(zipBytes) || zipBytes <= 0) return fail(411, "Content-Length is required");
  if ((req.headers.get("content-type") ?? "") !== "application/zip") return fail(415, "Body must be application/zip");
  if (zipBytes > Number(env.MAX_ZIP_BYTES)) return fail(413, `Bundle exceeds ${env.MAX_ZIP_BYTES} bytes`);
  if (luaBytes > Number(env.MAX_LUA_BYTES)) return fail(413, `SavedVariables exceed ${env.MAX_LUA_BYTES} bytes`);
  if (zipBytes > luaBytes + 64 * 1024) return fail(400, "Bundle size is inconsistent with file size");

  const existing = await env.DB.prepare("SELECT id, status FROM receipts WHERE device_id = ? AND source_id = ? AND sha256 = ?")
    .bind(device.id, sourceId, sha256)
    .first<ReceiptRow>();
  if (existing) {
    ctx.waitUntil(req.body?.cancel() ?? Promise.resolve());
    if (existing.status === "stored") {
      await env.DB.prepare("UPDATE receipts SET status = 'queued', queued_at = ? WHERE id = ?").bind(now(), existing.id).run();
      existing.status = "queued";
    }
    return json({ status: existing.status, receiptId: existing.id, duplicate: true });
  }

  const owner = clientId ?? device.id;
  const objectKey = `incoming/${owner}/${owner}-${sourceId.slice(0, 16)}-${sha256.slice(0, 16)}.zip`;
  let stored: R2Object | null;
  try {
    stored = await env.UPLOADS.put(objectKey, req.body, {
      sha256: zipSha256,
      httpMetadata: { contentType: "application/zip" },
      customMetadata: { device: device.id, client: clientId ?? "", sourceId, sha256, edition },
    });
  } catch (err) {
    return fail(422, "Upload rejected by storage: " + String((err as Error).message ?? err).slice(0, 200));
  }
  if (!stored || stored.size !== zipBytes) {
    if (stored) ctx.waitUntil(env.UPLOADS.delete(objectKey));
    return fail(422, "Stored size does not match Content-Length");
  }

  const id = crypto.randomUUID();
  const at = now();
  const ins = await env.DB.prepare(
    `INSERT INTO receipts (id, device_id, client_id, source_id, sha256, zip_sha256, object_key, zip_bytes, lua_bytes, edition, app_version, status, created_at, queued_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?, ?) ON CONFLICT (device_id, source_id, sha256) DO NOTHING`,
  )
    .bind(id, device.id, clientId, sourceId, sha256, zipSha256, objectKey, zipBytes, luaBytes, edition, appVersion, at, at)
    .run();
  if (!ins.meta.changes) {
    const winner = await env.DB.prepare("SELECT id, status FROM receipts WHERE device_id = ? AND source_id = ? AND sha256 = ?")
      .bind(device.id, sourceId, sha256)
      .first<ReceiptRow>();
    if (winner) return json({ status: winner.status, receiptId: winner.id, duplicate: true });
  }
  return json({ status: "queued", receiptId: id, duplicate: false }, 201);
}

export async function handleReceipt(req: Request, env: Env, ctx: ExecutionContext, receiptId: string): Promise<Response> {
  const device = await authenticateDevice(req, env, ctx);
  if (device instanceof Response) return device;
  const row = await env.DB.prepare(
    "SELECT id, status, detail, created_at, queued_at, finished_at FROM receipts WHERE id = ? AND device_id = ?",
  )
    .bind(receiptId, device.id)
    .first();
  return row ? json(row) : fail(404, "Unknown receipt");
}

export async function listPending(env: Env, limit: number): Promise<Response> {
  const rows = await env.DB.prepare(
    `SELECT id, client_id, device_id, source_id, sha256, zip_sha256, object_key, zip_bytes, lua_bytes, edition, app_version, created_at
     FROM receipts WHERE status IN ('queued', 'stored') ORDER BY created_at LIMIT ?`,
  )
    .bind(Math.min(Math.max(limit, 1), 200))
    .all();
  return json({ receipts: rows.results });
}

export async function getObject(env: Env, receiptId: string): Promise<Response> {
  const row = await env.DB.prepare("SELECT object_key FROM receipts WHERE id = ?").bind(receiptId).first<{ object_key: string }>();
  if (!row) return fail(404, "Unknown receipt");
  const obj = await env.UPLOADS.get(row.object_key);
  if (!obj) return fail(404, "Object missing from storage");
  return new Response(obj.body, {
    headers: { "content-type": "application/zip", "content-length": String(obj.size), "cache-control": "no-store" },
  });
}
