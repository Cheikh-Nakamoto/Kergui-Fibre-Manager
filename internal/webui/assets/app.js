"use strict";
// Kergui Fibre Manager — dashboard. Vanilla JS over the REST API served by
// `kergui serve`. No build step, no dependencies.

const $ = (id) => document.getElementById(id);
let devices = [];

function toast(msg, ms = 3200) {
  const t = $("toast");
  t.textContent = msg;
  t.classList.remove("hidden");
  clearTimeout(toast._t);
  toast._t = setTimeout(() => t.classList.add("hidden"), ms);
}

async function api(path, opts) {
  const res = await fetch(path, opts);
  let body = null;
  try { body = await res.json(); } catch (_) { /* no body */ }
  return { ok: res.ok, status: res.status, body };
}

// --- Tabs ---

document.querySelectorAll(".tab").forEach((tab) => {
  tab.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"));
    tab.classList.add("active");
    const target = tab.dataset.tab;
    $("tab-devices").classList.toggle("hidden", target !== "devices");
    $("tab-filter").classList.toggle("hidden", target !== "filter");
    $("tab-logs").classList.toggle("hidden", target !== "logs");
    if (target === "filter") {
      renderBlocklist();
      loadFilterState();
    }
    if (target === "logs") {
      unseenErrors = 0;
      updateLogsBadge();
      renderLogs();
    }
  });
});

// --- Journal (server logs) ---

const LEVEL_RANK = { DEBUG: 0, INFO: 1, WARN: 2, ERROR: 3 };
let logs = [];
let lastLogSeq = 0;
let unseenErrors = 0;

function logsTabActive() {
  return !$("tab-logs").classList.contains("hidden");
}

function updateLogsBadge() {
  const b = $("logsBadge");
  b.textContent = unseenErrors;
  b.classList.toggle("hidden", unseenErrors === 0);
}

function fmtTime(iso) {
  const d = new Date(iso);
  return d.toLocaleTimeString("fr-FR", { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0");
}

function renderLogs() {
  const min = $("logLevel").value;
  const q = $("logSearch").value.trim().toLowerCase();
  const view = $("logView");
  const visible = logs.filter((e) =>
    (min === "all" || LEVEL_RANK[e.level] >= LEVEL_RANK[min]) &&
    (!q || e.msg.toLowerCase().includes(q)));
  view.replaceChildren(...visible.map((e) => {
    const row = document.createElement("div");
    row.className = "log-row lvl-" + e.level.toLowerCase();
    const t = document.createElement("span");
    t.className = "log-time";
    t.textContent = fmtTime(e.time);
    const l = document.createElement("span");
    l.className = "log-level";
    l.textContent = e.level;
    const m = document.createElement("span");
    m.className = "log-msg";
    m.textContent = e.msg;
    row.append(t, l, m);
    return row;
  }));
  $("logEmpty").classList.toggle("hidden", visible.length > 0);
  if ($("logFollow").checked) view.scrollTop = view.scrollHeight;
}

async function pollLogs() {
  const { ok, body } = await api("/api/logs?since=" + lastLogSeq);
  if (ok && Array.isArray(body) && body.length) {
    logs = logs.concat(body).slice(-2000);
    lastLogSeq = body[body.length - 1].seq;
    if (!logsTabActive()) {
      unseenErrors += body.filter((e) => e.level === "ERROR").length;
      updateLogsBadge();
    } else {
      renderLogs();
    }
  }
}

["logLevel", "logSearch"].forEach((id) => $(id).addEventListener("input", renderLogs));
pollLogs();
setInterval(pollLogs, 2000);

// --- Health ---

async function loadHealth() {
  const { ok, body } = await api("/api/health");
  if (ok && body) {
    $("routerLine").textContent =
      `${body.router || "?"} · adapter ${body.adapter || "auto"} · v${body.version || "?"}`;
  }
}

// --- Devices ---

async function loadDevices() {
  const { ok, status, body } = await api("/api/devices");
  if (!ok) {
    if (status === 401) {
      toast("Connexion requise — ouvrez Réglages et enregistrez vos identifiants.");
      $("settings").classList.remove("hidden");
    } else {
      toast(`Erreur (${status}): ${(body && body.error) || "inconnue"}`);
    }
    devices = [];
  } else {
    devices = Array.isArray(body) ? body : [];
  }
  render();
  renderBlocklist();
}

function counts() {
  return {
    total: devices.length,
    connected: devices.filter((d) => d.Connected).length,
    blocked: devices.filter((d) => d.Blocked).length,
    newly: devices.filter((d) => d.NewlySeen).length,
  };
}

function displayName(d) { return d.CustomName || d.Hostname || d.MAC; }

function filterSort() {
  const q = $("search").value.trim().toLowerCase();
  const f = $("filter").value;
  const s = $("sort").value;
  let out = devices.filter((d) => {
    if (f === "connected" && !d.Connected) return false;
    if (f === "blocked" && !d.Blocked) return false;
    if (f === "new" && !d.NewlySeen) return false;
    if (!q) return true;
    return [displayName(d), d.IP, d.MAC, d.Vendor, d.SSID]
      .filter(Boolean).join(" ").toLowerCase().includes(q);
  });
  const cmp = {
    name: (a, b) => displayName(a).localeCompare(displayName(b)),
    ip: (a, b) => (a.IP || "").localeCompare(b.IP || "", undefined, { numeric: true }),
    mac: (a, b) => (a.MAC || "").localeCompare(b.MAC || ""),
    status: (a, b) => Number(b.Connected) - Number(a.Connected),
  }[s];
  return out.sort(cmp);
}

function statusPill(d) {
  if (d.Blocked) return `<span class="pill bad">Bloqué</span>`;
  return d.Connected
    ? `<span class="pill ok">Connecté</span>`
    : `<span class="pill off">Hors ligne</span>`;
}

function render() {
  const c = counts();
  $("cTotal").textContent = c.total;
  $("cConnected").textContent = c.connected;
  $("cBlocked").textContent = c.blocked;
  $("cNew").textContent = c.newly;

  const list = $("list");
  const rows = filterSort();
  $("empty").classList.toggle("hidden", rows.length > 0);
  list.innerHTML = "";
  for (const d of rows) {
    const el = document.createElement("article");
    el.className = "device" + (d.NewlySeen ? " newly" : "");
    const act = d.Blocked
      ? `<button class="btn sm" data-act="unblock" data-mac="${d.MAC}">Débloquer</button>`
      : `<button class="btn sm ghost" data-act="block" data-mac="${d.MAC}">Bloquer</button>`;
    el.innerHTML = `
      <div class="device-top">
        <span class="dname">${esc(displayName(d))}</span>
        ${statusPill(d)}
        ${d.NewlySeen ? `<span class="pill new">Nouveau</span>` : ""}
        <span class="spacer"></span>
        <span class="muted">${esc(d.Vendor || "")}</span>
      </div>
      <div class="meta">
        <span>IP: <code>${esc(d.IP || "—")}</code></span>
        <span>MAC: <code>${esc(d.MAC)}</code></span>
        ${d.SSID ? `<span>SSID: ${esc(d.SSID)}</span>` : ""}
      </div>
      <div class="row-actions">
        ${act}
        <button class="btn sm ghost" data-act="rename" data-mac="${d.MAC}">Renommer</button>
        <button class="btn sm ghost" data-act="details" data-mac="${d.MAC}">Détails</button>
      </div>
      <div class="details hidden" id="det-${cssid(d.MAC)}">
        <div>Première vue: ${fmt(d.FirstSeen)}</div>
        <div>Dernière vue: ${fmt(d.LastSeen)}</div>
        <div>Nom routeur: ${esc(d.Hostname || "—")}</div>
        ${d.Notes ? `<div>Notes: ${esc(d.Notes)}</div>` : ""}
      </div>`;
    list.appendChild(el);
  }
}

// --- Blocklist (filter tab) ---

function renderBlocklist() {
  const blocked = devices.filter((d) => d.Blocked);
  const container = $("blocklist");
  const emptyMsg = $("blocklistEmpty");

  if (blocked.length === 0) {
    container.innerHTML = "";
    emptyMsg.classList.remove("hidden");
    return;
  }
  emptyMsg.classList.add("hidden");
  container.innerHTML = "";
  for (const d of blocked) {
    const el = document.createElement("div");
    el.className = "block-item";
    el.innerHTML = `
      <span class="bi-dot"></span>
      <div class="bi-info">
        <div class="bi-name">${esc(displayName(d))}</div>
        <div class="bi-mac">${esc(d.MAC)}</div>
      </div>
      <button class="btn sm" data-act="unblock" data-mac="${d.MAC}">Débloquer</button>`;
    container.appendChild(el);
  }
}

// --- Actions ---

async function onAction(e) {
  const btn = e.target.closest("button[data-act]");
  if (!btn) return;
  const mac = btn.dataset.mac;
  const act = btn.dataset.act;
  if (act === "details") {
    $(`det-${cssid(mac)}`).classList.toggle("hidden");
    return;
  }
  if (act === "rename") {
    const cur = devices.find((d) => d.MAC === mac);
    const name = prompt("Nom pour cet appareil :", cur ? displayName(cur) : "");
    if (name == null) return;
    const r = await api(`/api/devices/${mac}`, {
      method: "PATCH", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ custom_name: name }),
    });
    if (r.ok) { toast("Renommé."); loadDevices(); }
    else toast(`Échec du renommage (${r.status}): ${(r.body && r.body.error) || ""}`);
    return;
  }
  // block / unblock
  btn.disabled = true;
  const r = await api(`/api/devices/${mac}/${act}`, { method: "POST" });
  btn.disabled = false;
  if (r.ok) { toast(act === "block" ? "Appareil bloqué." : "Appareil débloqué."); loadDevices(); }
  else if (r.status === 501) toast("Écriture pas encore disponible pour ce routeur. Le protocole d'écriture doit d'abord être validé.");
  else if (r.status === 401) { toast("Connexion requise — ouvrez Réglages."); $("settings").classList.remove("hidden"); }
  else toast(`Échec (${r.status}): ${(r.body && r.body.error) || ""}`);
}

// --- Add arbitrary MAC ---

$("addMacBtn").addEventListener("click", async () => {
  const macInput = $("addMacInput");
  const nameInput = $("addMacName");
  const mac = macInput.value.trim();
  const name = nameInput.value.trim();

  if (!mac) { toast("Entrez une adresse MAC."); return; }
  // Basic MAC validation
  if (!/^([0-9a-fA-F]{2}[:\-]){5}[0-9a-fA-F]{2}$/.test(mac)) {
    toast("Format MAC invalide. Ex: AA:BB:CC:DD:EE:FF");
    return;
  }

  // If a name is provided, rename the device first
  if (name) {
    await api(`/api/devices/${mac}`, {
      method: "PATCH", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ custom_name: name }),
    });
  }

  // Block the device
  const r = await api(`/api/devices/${mac}/block`, { method: "POST" });
  if (r.ok) {
    toast(`${name || mac} ajouté à la liste de filtrage.`);
    macInput.value = "";
    nameInput.value = "";
    loadDevices();
  } else if (r.status === 501) {
    toast("Écriture pas encore disponible pour ce routeur.");
  } else {
    toast(`Échec (${r.status}): ${(r.body && r.body.error) || ""}`);
  }
});

// --- Filter toggle ---

function setFilterUI(enabled) {
  $("filterToggle").checked = enabled;
  $("filterToggleLabel").textContent = enabled ? "Activé" : "Désactivé";
  $("filterOffWarning").classList.toggle("hidden", enabled);
}

async function loadFilterState() {
  const r = await api("/api/filter");
  if (r.ok && r.body) setFilterUI(r.body.enabled);
}

$("filterToggle").addEventListener("change", async () => {
  const toggle = $("filterToggle");
  const enabled = toggle.checked;
  toggle.disabled = true;
  $("filterToggleLabel").textContent = enabled ? "Activation…" : "Désactivation…";
  const r = await api("/api/filter/toggle", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ enabled }),
  });
  toggle.disabled = false;
  if (r.ok) {
    setFilterUI(enabled);
    toast(enabled ? "Filtrage MAC activé : les appareils de la liste sont coupés." : "Filtrage MAC désactivé.");
  } else {
    setFilterUI(!enabled);
    toast(r.status === 501 ? "Activation du filtrage indisponible pour ce routeur."
      : `Échec (${r.status}): ${(r.body && r.body.error) || ""}`);
  }
});

// --- Login ---

async function doLogin(test) {
  const r = await api("/api/login", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: $("username").value, password: $("password").value, test }),
  });
  if (r.ok) {
    toast(test ? "Connexion OK." : "Identifiants enregistrés.");
    $("password").value = "";
    if (!test) { $("settings").classList.add("hidden"); loadDevices(); }
  } else {
    toast(`Échec (${r.status}): ${(r.body && r.body.error) || "vérifiez les identifiants"}`);
  }
}

// --- Helpers ---

function esc(s) { return String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }
function cssid(s) { return String(s).replace(/[^a-z0-9]/gi, "_"); }
function fmt(iso) {
  if (!iso || iso.startsWith("0001")) return "—";
  const d = new Date(iso);
  return isNaN(d) ? "—" : d.toLocaleString();
}

// --- Event wiring ---

$("refreshBtn").addEventListener("click", loadDevices);
$("settingsBtn").addEventListener("click", () => $("settings").classList.toggle("hidden"));
$("search").addEventListener("input", render);
$("filter").addEventListener("change", render);
$("sort").addEventListener("change", render);
$("list").addEventListener("click", onAction);
$("blocklist").addEventListener("click", onAction);
$("testBtn").addEventListener("click", () => doLogin(true));
$("loginForm").addEventListener("submit", (e) => { e.preventDefault(); doLogin(false); });

// Allow Enter key in MAC input
$("addMacInput").addEventListener("keydown", (e) => {
  if (e.key === "Enter") { e.preventDefault(); $("addMacBtn").click(); }
});
$("addMacName").addEventListener("keydown", (e) => {
  if (e.key === "Enter") { e.preventDefault(); $("addMacBtn").click(); }
});

loadHealth();
loadDevices();
