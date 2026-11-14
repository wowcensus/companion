import {Events} from "@wailsio/runtime";
import {CompanionService} from "../bindings/wowcensuscompanion";
import {
  t,
  setLanguage,
  getLanguage,
  getStoredLanguage,
  getEditionName,
  formatMissingAddon,
  formatFoundSaves,
} from "./i18n.js";

const $ = (sel) => document.querySelector(sel);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({"&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"}[c]));
const mb = (n) => n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : (n / 1024).toFixed(0) + " KB";

const localeMap = {
  ko: "ko-KR",
  es: "es-ES",
  de: "de-DE",
  fr: "fr-FR",
  it: "it-IT",
  en: "en-US",
};

const when = (tVal) => {
  if (!tVal) return "—";
  const d = tVal instanceof Date ? tVal : new Date(tVal);
  if (isNaN(d.getTime()) || d.getFullYear() < 2000) return "—";
  const loc = localeMap[getLanguage()] || undefined;
  return d.toLocaleDateString(loc) + " " + d.toLocaleTimeString(loc, {hour: "2-digit", minute: "2-digit"});
};

document.querySelectorAll(".tabs button").forEach((b) => b.addEventListener("click", () => {
  document.querySelectorAll(".tabs button").forEach((x) => x.classList.toggle("active", x === b));
  document.querySelectorAll(".panel").forEach((p) => p.classList.toggle("active", p.id === "tab-" + b.dataset.tab));
}));

let installing = false;
let lastStatus = null;

function uploadCell(f, autoUpload) {
  const r = f.lastReceipt;
  if (f.uploaded && r?.status === "imported") return `<span class="pill ok">${t("pill.onSite")}</span>`;
  if (f.uploaded && r?.status === "failed") return `<span class="pill warn" title="${t("pill.notUsedTitle")}">${t("pill.notUsed")}</span>`;
  if (f.uploaded) return `<span class="pill ok" title="${t("pill.uploadedTitle")}">${t("pill.uploaded", { when: when(r?.at) })}</span>`;
  if (f.nextRetry) return `<span class="pill err" title="${esc(f.lastError)}">${t("pill.retrying", { when: when(f.nextRetry) })}</span>`;
  if (f.lastError) return `<span class="pill warn" title="${esc(f.lastError)}">${esc(f.lastError)}</span>`;
  if (!autoUpload) return `<span class="pill">${t("pill.uploadsOff")}</span>`;
  return `<span class="pill">${t("pill.waiting")}</span>`;
}

function renderAddon(a) {
  const installs = a?.installs || [];
  const latest = a?.latest || "";
  const list = $("#addon-list");
  const btn = $("#btn-addon");
  if (!installs.length) {
    $("#addon-summary").textContent = t("addon.notFound");
    list.innerHTML = "";
    btn.hidden = true;
    return;
  }
  list.innerHTML = installs.map((i) => {
    const state = !i.installed
      ? `<span class="pill">${t("addon.notInstalled")}</span>`
      : latest && i.installed !== latest
        ? `<span class="pill warn">${t("addon.updateAvailable", { version: esc(i.installed) })}</span>`
        : `<span class="pill ok">${t("addon.installed", { version: esc(i.installed) })}</span>`;
    return `<li><span>${esc(getEditionName(i.edition))}</span>${state}</li>`;
  }).join("");
  const missing = installs.filter((i) => !i.installed).length;
  const outdated = latest ? installs.filter((i) => i.installed && i.installed !== latest).length : 0;
  if (missing || outdated) {
    $("#addon-summary").textContent = missing
      ? formatMissingAddon(missing, installs.length)
      : t("addon.hasUpdate");
    if (!installing) btn.textContent = missing ? t("addon.install") : t("addon.update");
    btn.hidden = false;
  } else {
    $("#addon-summary").textContent = latest
      ? t("addon.upToDate", { version: latest })
      : t("addon.allInstalled");
    btn.hidden = true;
  }
  if (!installing) btn.disabled = false;
  if (a?.error && !latest && !$("#addon-msg").textContent) $("#addon-msg").textContent = a.error;
}

let updating = false;
let updateReady = false;

function renderUpdate(u) {
  const banner = $("#update-banner");
  updateReady = !!u?.ready;
  if (!u?.available) {
    banner.hidden = true;
    return;
  }
  banner.hidden = false;
  if (updating || u.installing) {
    $("#update-text").textContent = t("update.installing", { version: u.version });
    $("#btn-update").disabled = true;
    return;
  }
  if (u.downloading) {
    $("#update-text").textContent = t("update.downloading", { version: u.version });
    $("#btn-update").disabled = true;
    return;
  }
  if (u.ready) {
    $("#update-text").textContent = u.error
      ? t("update.readyFailed", { version: u.version, error: u.error })
      : t("update.ready", { version: u.version });
    $("#btn-update").disabled = false;
    $("#btn-update").textContent = t("update.restartBtn");
    return;
  }
  $("#update-text").textContent = u.error
    ? t("update.availableFailed", { version: u.version, error: u.error })
    : t("update.available", { version: u.version, current: u.current });
  $("#btn-update").disabled = false;
  $("#btn-update").textContent = t("update.retryBtn");
}

function renderStatus(s) {
  if (!s) return;
  lastStatus = s;
  const files = s.files || [];
  const busy = s.busy === "Uploading" ? t("status.uploading") : "";
  const watchingText = s.running ? t("status.watching") : t("status.stopped");
  const uploadsText = s.autoUpload ? t("status.uploadsOn") : t("status.uploadsOff");
  const lastCheckedText = t("status.lastChecked", { when: when(s.lastScan) });
  $("#summary").textContent = `${watchingText} · ${uploadsText} · ${lastCheckedText}${busy}`;
  $("#consent-banner").hidden = !!s.autoUpload;
  const scanned = when(s.lastScan) !== "—";
  const versions = new Set(files.map((f) => f.edition)).size;
  $("#found").textContent = !scanned
    ? t("saves.looking")
    : files.length
      ? formatFoundSaves(files.length, versions)
      : t("saves.noFiles");
  $("#found").className = "found " + (files.length ? "ok" : scanned ? "warn" : "");
  $("#files-table").hidden = files.length === 0;
  $("#no-files").hidden = files.length > 0 || !scanned;
  $("#files").innerHTML = files.map((f) => `<tr title="${esc(f.path)}">
      <td>${esc(getEditionName(f.edition))}</td><td>${esc(f.account)}</td>
      <td>${mb(f.bytes)}</td><td>${when(f.modTime)}</td><td>${uploadCell(f, s.autoUpload)}</td></tr>`).join("");
  renderAddon(s.addon);
  renderUpdate(s.update);
  const log = s.log || [];
  $("#log").innerHTML = log.slice().reverse().map((l) =>
    `<li class="${esc(l.level)}"><time>${when(l.at)}</time> ${esc(l.message)}</li>`).join("");
}

// Initialize language from stored setting or system default
setLanguage(getStoredLanguage() || "auto");

Events.On("status", (ev) => renderStatus(ev.data));
CompanionService.GetStatus().then(renderStatus);

$("#btn-addon").onclick = async () => {
  const btn = $("#btn-addon");
  installing = true;
  btn.disabled = true;
  btn.textContent = t("addon.installing");
  $("#addon-msg").textContent = "";
  try {
    const res = await CompanionService.InstallAddon();
    $("#addon-msg").textContent = `${res} ${t("addon.restartHint")}`;
  } catch (err) {
    $("#addon-msg").textContent = String(err?.message || err);
  } finally {
    installing = false;
    btn.disabled = false;
    CompanionService.GetStatus().then(renderStatus);
  }
};

$("#btn-update").onclick = async () => {
  if (!updateReady) {
    $("#btn-update").disabled = true;
    try {
      await CompanionService.PrepareUpdate();
    } catch (err) {
      CompanionService.GetStatus().then(renderStatus);
    }
    return;
  }
  updating = true;
  $("#btn-update").disabled = true;
  $("#update-text").textContent = t("update.installingProgress");
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
  const lang = v.language || getStoredLanguage() || "auto";
  form.elements.language.value = lang;
  setLanguage(lang);
  if (lastStatus) {
    renderStatus(lastStatus);
  }
}

function readSettings() {
  return {
    autoUpload: form.elements.autoUpload.checked,
    startAtLogin: form.elements.startAtLogin.checked,
    wowRoots: form.elements.wowRoots.value.split(/\r?\n/).map((s) => s.trim()).filter(Boolean),
    language: form.elements.language.value,
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
    const saved = await CompanionService.SaveSettings(readSettings());
    fillSettings(saved);
    msg(t("settings.saved"));
  } catch (err) {
    msg(String(err?.message || err), true);
  }
});

$("#language-select").addEventListener("change", async (e) => {
  setLanguage(e.target.value);
  if (lastStatus) {
    renderStatus(lastStatus);
  }
  try {
    const cur = await CompanionService.GetSettings();
    cur.language = e.target.value;
    await CompanionService.SaveSettings(cur);
  } catch {}
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
    ? found.map((f) => `<li><b>${esc(getEditionName(f.edition))}</b> · ${esc(f.account)}<br><small>${esc(f.path)}</small></li>`).join("")
    : `<li class='muted'>${t("settings.noSavesInFolders")}</li>`;
};

CompanionService.GetSettings().then(fillSettings);
