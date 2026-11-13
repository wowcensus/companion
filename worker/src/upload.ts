import { authenticateDevice } from "./auth";
import { HEX64, fail, json, now, type Env, type IngestJob } from "./util";

type ReceiptRow = {
  id: string;
  device_id: string;
  source_id: string;
  sha256: string;
  zip_sha256: string;
  object_key: string;
  zip_bytes: number;
  lua_bytes: number;
  edition: string;
  app_version: string;
  status: "stored" | "queued" | "imported" | "failed";
  detail: string | null;
  created_at: string;
  queued_at: string | null;
  finished_at: string | null;
};

const H = {
  source: "x-wowcensus-source-id",
  sha: "x-wowcensus-sha256",
  zipSha: "x-wowcensus-zip-sha256",
  luaBytes: "x-wowcensus-lua-bytes",
  edition: "x-wowcensus-edition",
  version: "x-wowcensus-app-version",
};

export async function handleUpload(req: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
  const device = await authenticateDevice(req, env, ctx);
  if (device instanceof Response) return device;

  const h = (k: string) => (req.headers.get(k) ?? "").trim();
  const sourceId = h(H.source).toLowerCase();
  const sha256 = h(H.sha).toLowerCase();
  const zipSha256 = h(H.zipSha).toLowerCase();
  const edition = h(H.edition);
  const appVersion = h(H.version);
  const luaBytes = Number(h(H.luaBytes));
  const zipBytes = Number(req.headers.get("content-length"));

  if (!HEX64.test(sourceId) || !HEX64.test(sha256) || !HEX64.test(zipSha256)) return fail(400, "Source ID and hashes must be 64 hex characters");
  if (!/^[a-z0-9_]{1,40}$/.test(edition)) return fail(400, "Invalid edition");
  if (!/^[\w.+-]{0,40}$/.test(appVersion)) return fail(400, "Invalid app version");
  if (!Number.isSafeInteger(luaBytes) || luaBytes <= 0) return fail(400, "Invalid Lua size");
  if (!Number.isSafeInteger(zipBytes) || zipBytes <= 0) return fail(411, "Content-Length is required");
  if ((req.headers.get("content-type") ?? "") !== "application/zip") return fail(415, "Body must be application/zip");
  if (zipBytes > Number(env.MAX_ZIP_BYTES)) return fail(413, `Bundle exceeds ${env.MAX_ZIP_BYTES} bytes`);
  if (luaBytes > Number(env.MAX_LUA_BYTES)) return fail(413, `SavedVariables exceed ${env.MAX_LUA_BYTES} bytes`);
  if (zipBytes > luaBytes + 64 * 1024) return fail(400, "Bundle size is inconsistent with file size");

  const existing = await env.DB.prepare("SELECT * FROM receipts WHERE device_id = ? AND source_id = ? AND sha256 = ?")
    .bind(device.id, sourceId, sha256)
    .first<ReceiptRow>();
  if (existing) {
    ctx.waitUntil(req.body?.cancel() ?? Promise.resolve());
    const row = existing.status === "stored" ? await enqueue(env, existing) : existing;
    return json({ status: row.status, receiptId: row.id, duplicate: true });
  }

  const objectKey = `incoming/${device.id}/${sourceId}/${sha256}.zip`;
  let stored: R2Object | null;
  try {
    stored = await env.UPLOADS.put(objectKey, req.body, {
      sha256: zipSha256,
      httpMetadata: { contentType: "application/zip" },
      customMetadata: { device: device.id, sourceId, sha256, edition },
    });
  } catch (err) {
    return fail(422, "Upload rejected by storage: " + String((err as Error).message ?? err).slice(0, 200));
  }
  if (!stored || stored.size !== zipBytes) {
    if (stored) ctx.waitUntil(env.UPLOADS.delete(objectKey));
    return fail(422, "Stored size does not match Content-Length");
  }

  const row: ReceiptRow = {
    id: crypto.randomUUID(),
    device_id: device.id,
    source_id: sourceId,
    sha256,
    zip_sha256: zipSha256,
    object_key: objectKey,
    zip_bytes: zipBytes,
    lua_bytes: luaBytes,
    edition,
    app_version: appVersion,
    status: "stored",
    detail: null,
    created_at: now(),
    queued_at: null,
    finished_at: null,
  };
  const ins = await env.DB.prepare(
    `INSERT INTO receipts (id, device_id, source_id, sha256, zip_sha256, object_key, zip_bytes, lua_bytes, edition, app_version, status, created_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'stored', ?) ON CONFLICT (device_id, source_id, sha256) DO NOTHING`,
  )
    .bind(row.id, row.device_id, row.source_id, row.sha256, row.zip_sha256, row.object_key, row.zip_bytes, row.lua_bytes, row.edition, row.app_version, row.created_at)
    .run();
  if (!ins.meta.changes) {
    const winner = await env.DB.prepare("SELECT * FROM receipts WHERE device_id = ? AND source_id = ? AND sha256 = ?")
      .bind(device.id, sourceId, sha256)
      .first<ReceiptRow>();
    if (winner) return json({ status: winner.status, receiptId: winner.id, duplicate: true });
  }

  const queued = await enqueue(env, row);
  return json({ status: queued.status, receiptId: queued.id, duplicate: false }, 201);
}

async function enqueue(env: Env, row: ReceiptRow): Promise<ReceiptRow> {
  const queuedAt = now();
  const job: IngestJob = {
    receiptId: row.id,
    deviceId: row.device_id,
    sourceId: row.source_id,
    sha256: row.sha256,
    zipSha256: row.zip_sha256,
    objectKey: row.object_key,
    zipBytes: row.zip_bytes,
    luaBytes: row.lua_bytes,
    edition: row.edition,
    appVersion: row.app_version,
    queuedAt,
    ingestKey: `${row.device_id}:${row.source_id}:${row.sha256}`,
  };
  await env.INGEST.send(job, { contentType: "json" });
  await env.DB.prepare("UPDATE receipts SET status = 'queued', queued_at = ? WHERE id = ? AND status = 'stored'").bind(queuedAt, row.id).run();
  return { ...row, status: "queued", queued_at: queuedAt };
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
