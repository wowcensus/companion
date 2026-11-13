import { createDevice, registerDevice, requireSecret } from "./auth";
import { handleReceipt, handleUpload } from "./upload";
import { fail, json, now, type Env } from "./util";

export default {
  async fetch(req: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(req.url);
    const path = url.pathname.replace(/\/+$/, "") || "/";
    try {
      if (path === "/v1/health" && req.method === "GET") return json({ ok: true });
      if (path === "/v1/devices" && req.method === "POST") return registerDevice(req, env);
      if (path === "/v1/uploads") return req.method === "PUT" ? handleUpload(req, env, ctx) : fail(405, "Use PUT");

      let m = /^\/v1\/uploads\/([0-9a-f-]{36})$/.exec(path);
      if (m && req.method === "GET") return handleReceipt(req, env, ctx, m[1]);

      if (path === "/v1/ingest/ack" && req.method === "POST") return ingestAck(req, env);

      if (path === "/v1/admin/devices" && req.method === "POST") return enrol(req, env);
      m = /^\/v1\/admin\/devices\/(dev_[0-9a-f]{20})$/.exec(path);
      if (m && req.method === "DELETE") return revoke(req, env, m[1]);

      return fail(404, "Not found");
    } catch (err) {
      console.error(err);
      return fail(500, "Internal error");
    }
  },
} satisfies ExportedHandler<Env>;

async function ingestAck(req: Request, env: Env): Promise<Response> {
  const denied = await requireSecret(req, env.INGEST_TOKEN);
  if (denied) return denied;
  const body = (await req.json().catch(() => null)) as { receiptId?: string; status?: string; detail?: string } | null;
  if (!body?.receiptId || !/^[0-9a-f-]{36}$/.test(body.receiptId) || (body.status !== "imported" && body.status !== "failed")) {
    return fail(400, 'Body must be {"receiptId": uuid, "status": "imported"|"failed", "detail"?: string}');
  }
  const res = await env.DB.prepare("UPDATE receipts SET status = ?, detail = ?, finished_at = ? WHERE id = ? AND status IN ('queued', 'failed', 'imported')")
    .bind(body.status, (body.detail ?? "").slice(0, 500) || null, now(), body.receiptId)
    .run();
  return res.meta.changes ? json({ ok: true }) : fail(404, "Unknown or not-yet-queued receipt");
}

async function enrol(req: Request, env: Env): Promise<Response> {
  const denied = await requireSecret(req, env.ADMIN_TOKEN);
  if (denied) return denied;
  const body = (await req.json().catch(() => ({}))) as { label?: string };
  return json(await createDevice(env, (body.label ?? "").slice(0, 100) || null, null), 201);
}

async function revoke(req: Request, env: Env, deviceId: string): Promise<Response> {
  const denied = await requireSecret(req, env.ADMIN_TOKEN);
  if (denied) return denied;
  const res = await env.DB.prepare("UPDATE devices SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL").bind(now(), deviceId).run();
  return res.meta.changes ? json({ ok: true }) : fail(404, "Unknown or already revoked device");
}
