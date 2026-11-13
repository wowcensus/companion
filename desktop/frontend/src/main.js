import {Events} from "@wailsio/runtime";
import {CompanionService} from "../bindings/wowcensuscompanion";

const $ = (sel) => document.querySelector(sel);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({"&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"}[c]));
const mb = (n) => n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : (n / 1024).toFixed(0) + " KB";
const when = (t) => {
  if (!t) return "—";
  const d = t instanceof Date ? t : new Date(t);
  if (isNaN(d.getTime()) || d.getFullYear() < 2000) return "—";
  return d.toLocaleDateString() + " " + d.toLocaleTimeString([], {hour: "2-digit", minute: "2-digit"});
};
const editions = {retail: "Retail", classic_era: "Classic Era", anniversary: "TBC Anniversary", classic: "MoP Classic", forever: "Forever", ptr: "PTR", beta: "Beta", xptr: "PTR"};
const editionName = (e) => editions[e] || String(e || "").replace(/_/g, " ");
const buttonLabels = {"#btn-scan": "Scan", "#btn-upload": "Upload now", "#btn-backup": "Back up now"};
let pressed = null;

function press(sel, label, call) {
  const b = $(sel);
  pressed = sel;
  b.disabled = true;
  b.textContent = label;
  call().catch((err) => {
    b.disabled = false;
    b.textContent = buttonLabels[sel];
    alert(String(err?.message || err));
  });
}

document.querySelectorAll(".tabs button").forEach((b) => b.addEventListener("click", () => {
  document.querySelectorAll(".tabs button").forEach((x) => x.classList.toggle("active", x === b));
  document.querySelectorAll(".panel").forEach((p) => p.classList.toggle("active", p.id === "tab-" + b.dataset.tab));
  if (b.dataset.tab === "maintenance") loadReport();
}));

function renderStatus(s) {
  const files = s.files || [];
  const busy = s.busy ? ` · ${s.busy}…` : "";
  $("#summary").textContent =
    `${s.running ? "Watching for new saves" : "Stopped"} · uploads ${s.autoUpload ? "on" : "off"} · last checked ${when(s.lastScan)}${busy}`;
  $("#consent-banner").hidden = !!s.autoUpload;
  const scanned = when(s.lastScan) !== "—";
  const versions = new Set(files.map((f) => f.edition)).size;
  $("#found").textContent = !scanned
    ? "Looking for your WoWCensus saves…"
    : files.length
      ? `Found ${files.length} WoWCensus save${files.length === 1 ? "" : "s"} in ${versions} game version${versions === 1 ? "" : "s"}.`
      : "No WoWCensus saves found yet.";
  $("#found").className = "found " + (files.length ? "ok" : scanned ? "warn" : "");
  $("#files-table").hidden = files.length === 0;
  $("#no-files").hidden = files.length > 0 || !scanned;
  if (!s.busy && pressed) {
    $(pressed).disabled = false;
    $(pressed).textContent = buttonLabels[pressed];
    pressed = null;
  }
  $("#files").innerHTML = files.map((f) => {
    let up;
    if (f.uploaded) up = `<span class="pill ok">Queued ${when(f.lastReceipt?.at)}</span>`;
    else if (f.nextRetry) up = `<span class="pill err" title="${esc(f.lastError)}">Retry ${when(f.nextRetry)}</span>`;
    else if (f.lastError) up = `<span class="pill warn" title="${esc(f.lastError)}">${esc(f.lastError)}</span>`;
    else up = `<span class="pill">Not uploaded</span>`;
    return `<tr title="${esc(f.path)}">
      <td>${esc(editionName(f.edition))}</td><td>${esc(f.account)}</td>
      <td class="${f.overThreshold ? "big" : ""}">${mb(f.bytes)}</td>
      <td>${when(f.modTime)}</td><td>${up}</td></tr>`;
  }).join("");
  const log = s.log || [];
  $("#log").innerHTML = log.slice().reverse().map((l) =>
    `<li class="${esc(l.level)}"><time>${when(l.at)}</time> ${esc(l.message)}</li>`).join("");
}

Events.On("status", (ev) => renderStatus(ev.data));
CompanionService.GetStatus().then(renderStatus);

$("#btn-scan").onclick = () => press("#btn-scan", "Scanning…", CompanionService.ScanNow);
$("#btn-upload").onclick = () => press("#btn-upload", "Uploading…", CompanionService.UploadNow);
$("#btn-backup").onclick = () => press("#btn-backup", "Backing up…", CompanionService.BackupNow);

const form = $("#settings");
const numFields = ["sizeThresholdMB", "backupKeep", "backupCapMB", "pollSeconds"];
const boolFields = ["autoUpload", "includeSystemInfo", "startAtLogin", "autoBackup"];

function fillSettings(v) {
  boolFields.forEach((k) => form.elements[k].checked = !!v[k]);
  numFields.forEach((k) => form.elements[k].value = v[k] ?? "");
  form.elements.serverUrl.value = v.serverUrl || "";
  form.elements.backupDir.value = v.backupDir || "";
  form.elements.wowRoots.value = (v.wowRoots || []).join("\n");
  $("#device-hint").textContent = v.registered
    ? `This computer is registered (${v.deviceId}).`
    : "This computer registers itself automatically with the first upload.";
}

function readSettings() {
  const cfg = {
    serverUrl: form.elements.serverUrl.value.trim(),
    backupDir: form.elements.backupDir.value.trim(),
    wowRoots: form.elements.wowRoots.value.split(/\r?\n/).map((s) => s.trim()).filter(Boolean),
  };
  boolFields.forEach((k) => cfg[k] = form.elements[k].checked);
  numFields.forEach((k) => cfg[k] = parseInt(form.elements[k].value, 10) || 0);
  return cfg;
}

function msg(text, isErr) {
  const m = $("#save-msg");
  m.textContent = text;
  m.className = isErr ? "error" : "muted";
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  const cfg = readSettings();
  if (cfg.autoUpload && !confirm("Turn on automatic uploads? Your WoWCensus.lua will be sent to " + (cfg.serverUrl || "the server") + " each time it changes.")) {
    return;
  }
  try {
    fillSettings(await CompanionService.SaveSettings(cfg));
    msg("Saved.");
  } catch (err) {
    msg(String(err?.message || err), true);
  }
});

$("#btn-enable").onclick = async () => {
  try {
    const cfg = await CompanionService.GetSettings();
    cfg.autoUpload = true;
    fillSettings(await CompanionService.SaveSettings(cfg));
    CompanionService.UploadNow();
  } catch (err) {
    alert(String(err?.message || err));
  }
};

$("#btn-detect").onclick = async () => {
  const roots = form.elements.wowRoots.value.split(/\r?\n/).map((s) => s.trim()).filter(Boolean);
  const found = await CompanionService.DetectInstalls(roots);
  $("#detected").innerHTML = found.length
    ? found.map((f) => `<li><b>${esc(editionName(f.edition))}</b> · ${esc(f.account)}<br><small>${esc(f.path)}</small></li>`).join("")
    : "<li class='muted'>Nothing found. Check the folder and that WoWCensus has saved at least once (/reload).</li>";
};

CompanionService.GetSettings().then(fillSettings);

async function loadReport() {
  const r = await CompanionService.MaintenanceReport();
  const rows = (r.files || []).map((f) =>
    `<tr><td>${esc(editionName(f.edition))}</td><td>${esc(f.account)}</td><td class="${f.overThreshold ? "big" : ""}">${mb(f.bytes)}</td>
     <td>${f.overThreshold ? "Above " + r.thresholdMB + " MB" : "OK"}</td></tr>`).join("");
  $("#report").innerHTML = `
    <table class="files"><thead><tr><th>Edition</th><th>Account</th><th>Size</th><th>Check</th></tr></thead><tbody>${rows}</tbody></table>
    <p><b>Backups:</b> ${r.backupCount} file(s), ${mb(r.backupBytes)} of ${r.backupCapMB} MB cap, ${r.backupKeep} generation(s) per file<br>
    <small>${esc(r.backupDir)}</small></p>
    <ul class="notes">${(r.notes || []).map((n) => `<li>${esc(n)}</li>`).join("")}</ul>`;
}
$("#btn-report").onclick = loadReport;
