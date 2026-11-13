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
const editions = {retail: "Retail", classic_era: "Classic Era", anniversary: "TBC Anniversary", classic: "MoP Classic", forever: "Forever", classic_beta: "Classic Beta", ptr: "PTR", xptr: "PTR", beta: "Beta"};
const editionName = (e) => editions[e] || String(e || "").replace(/_/g, " ");

document.querySelectorAll(".tabs button").forEach((b) => b.addEventListener("click", () => {
  document.querySelectorAll(".tabs button").forEach((x) => x.classList.toggle("active", x === b));
  document.querySelectorAll(".panel").forEach((p) => p.classList.toggle("active", p.id === "tab-" + b.dataset.tab));
}));

let installing = false;

function uploadCell(f, autoUpload) {
  const r = f.lastReceipt;
  if (f.uploaded && r?.status === "imported") return `<span class="pill ok">On wowcensus.io</span>`;
  if (f.uploaded && r?.status === "failed") return `<span class="pill warn" title="The server could not use this save">Not used</span>`;
  if (f.uploaded) return `<span class="pill ok" title="Waiting to be added to wowcensus.io">Uploaded ${when(r?.at)}</span>`;
  if (f.nextRetry) return `<span class="pill err" title="${esc(f.lastError)}">Retrying ${when(f.nextRetry)}</span>`;
  if (f.lastError) return `<span class="pill warn" title="${esc(f.lastError)}">${esc(f.lastError)}</span>`;
  if (!autoUpload) return `<span class="pill">Uploads off</span>`;
  return `<span class="pill">Waiting…</span>`;
}

function renderAddon(a) {
  const installs = a?.installs || [];
  const latest = a?.latest || "";
  const list = $("#addon-list");
  const btn = $("#btn-addon");
  if (!installs.length) {
    $("#addon-summary").textContent = "No World of Warcraft installation found. If WoW is installed somewhere unusual, add its folder in Settings.";
    list.innerHTML = "";
    btn.hidden = true;
    return;
  }
  list.innerHTML = installs.map((i) => {
    const state = !i.installed
      ? `<span class="pill">Not installed</span>`
      : latest && i.installed !== latest
        ? `<span class="pill warn">Version ${esc(i.installed)}, update available</span>`
        : `<span class="pill ok">Installed ${esc(i.installed)}</span>`;
    return `<li><span>${esc(editionName(i.edition))}</span>${state}</li>`;
  }).join("");
  const missing = installs.filter((i) => !i.installed).length;
  const outdated = latest ? installs.filter((i) => i.installed && i.installed !== latest).length : 0;
  if (missing || outdated) {
    $("#addon-summary").textContent = missing
      ? `Not installed for ${missing} of your ${installs.length} game version${installs.length === 1 ? "" : "s"}.`
      : "An update is available.";
    if (!installing) btn.textContent = missing ? "Install add-on" : "Update add-on";
    btn.hidden = false;
  } else {
    $("#addon-summary").textContent = latest ? `Up to date (version ${latest}) in all your game versions.` : "Installed in all your game versions.";
    btn.hidden = true;
  }
  if (!installing) btn.disabled = false;
  if (a?.error && !latest && !$("#addon-msg").textContent) $("#addon-msg").textContent = a.error;
}

let updating = false;

function renderUpdate(u) {
  const banner = $("#update-banner");
  if (!u?.available) {
    banner.hidden = true;
    return;
  }
  banner.hidden = false;
  if (updating || u.installing) {
    $("#update-text").textContent = `Downloading version ${u.version}… the app will restart by itself.`;
    $("#btn-update").disabled = true;
    return;
  }
  $("#update-text").textContent = u.error
    ? `Version ${u.version} is available, but the last try failed: ${u.error}`
    : `Version ${u.version} is available (you have ${u.current}). It takes a few seconds and the app restarts by itself.`;
  $("#btn-update").disabled = false;
  $("#btn-update").textContent = u.error ? "Try again" : "Update now";
}

function renderStatus(s) {
  const files = s.files || [];
  const busy = s.busy === "Uploading" ? " · uploading…" : "";
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
  $("#files").innerHTML = files.map((f) => `<tr title="${esc(f.path)}">
      <td>${esc(editionName(f.edition))}</td><td>${esc(f.account)}</td>
      <td>${mb(f.bytes)}</td><td>${when(f.modTime)}</td><td>${uploadCell(f, s.autoUpload)}</td></tr>`).join("");
  renderAddon(s.addon);
  renderUpdate(s.update);
  const log = s.log || [];
  $("#log").innerHTML = log.slice().reverse().map((l) =>
    `<li class="${esc(l.level)}"><time>${when(l.at)}</time> ${esc(l.message)}</li>`).join("");
}

Events.On("status", (ev) => renderStatus(ev.data));
CompanionService.GetStatus().then(renderStatus);

$("#btn-addon").onclick = async () => {
  const btn = $("#btn-addon");
  installing = true;
  btn.disabled = true;
  btn.textContent = "Installing…";
  $("#addon-msg").textContent = "";
  try {
    $("#addon-msg").textContent = (await CompanionService.InstallAddon()) + " Restart World of Warcraft (or type /reload) to load it.";
  } catch (err) {
    $("#addon-msg").textContent = String(err?.message || err);
  } finally {
    installing = false;
    btn.disabled = false;
    CompanionService.GetStatus().then(renderStatus);
  }
};

$("#btn-update").onclick = async () => {
  updating = true;
  $("#btn-update").disabled = true;
  $("#update-text").textContent = "Downloading the update… the app will restart by itself.";
  try {
    await CompanionService.InstallUpdate();
  } catch (err) {
    updating = false;
    CompanionService.GetStatus().then(renderStatus);
  }
};

const form = $("#settings");

function fillSettings(v) {
  form.elements.autoUpload.checked = !!v.autoUpload;
  form.elements.startAtLogin.checked = !!v.startAtLogin;
  form.elements.wowRoots.value = (v.wowRoots || []).join("\n");
}

function readSettings() {
  return {
    autoUpload: form.elements.autoUpload.checked,
    startAtLogin: form.elements.startAtLogin.checked,
    wowRoots: form.elements.wowRoots.value.split(/\r?\n/).map((s) => s.trim()).filter(Boolean),
  };
}

function msg(text, isErr) {
  const m = $("#save-msg");
  m.textContent = text;
  m.className = isErr ? "error" : "muted";
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  try {
    fillSettings(await CompanionService.SaveSettings(readSettings()));
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
  } catch (err) {
    alert(String(err?.message || err));
  }
};

$("#btn-detect").onclick = async () => {
  const roots = form.elements.wowRoots.value.split(/\r?\n/).map((s) => s.trim()).filter(Boolean);
  const found = await CompanionService.DetectInstalls(roots);
  $("#detected").innerHTML = found.length
    ? found.map((f) => `<li><b>${esc(editionName(f.edition))}</b> · ${esc(f.account)}<br><small>${esc(f.path)}</small></li>`).join("")
    : "<li class='muted'>No WoWCensus saves found in those folders yet.</li>";
};

CompanionService.GetSettings().then(fillSettings);
