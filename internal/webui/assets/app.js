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

async function loadHealth() {
  const { ok, body } = await api("/api/health");
  if (ok && body) {
    $("routerLine").textContent =
      `${body.router || "?"} · adapter ${body.adapter || "auto"} · v${body.version || "?"}`;
  }
}

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
  if (d.Blocked) return `<span class="pill bad">🔴 Bloqué</span>`;
  return d.Connected
    ? `<span class="pill ok">🟢 Connecté</span>`
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
  const r = await api(`/api/devices/${mac}/${act}`, { method: "POST" });
  if (r.ok) { toast(act === "block" ? "Appareil bloqué." : "Appareil débloqué."); loadDevices(); }
  else if (r.status === 501) toast("Blocage/déblocage pas encore disponible : le protocole d'écriture doit d'abord être validé sur un vrai routeur.");
  else if (r.status === 401) { toast("Connexion requise — ouvrez Réglages."); $("settings").classList.remove("hidden"); }
  else toast(`Échec (${r.status}): ${(r.body && r.body.error) || ""}`);
}

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

function esc(s) { return String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }
function cssid(s) { return String(s).replace(/[^a-z0-9]/gi, "_"); }
function fmt(iso) {
  if (!iso || iso.startsWith("0001")) return "—";
  const d = new Date(iso);
  return isNaN(d) ? "—" : d.toLocaleString();
}

$("refreshBtn").addEventListener("click", loadDevices);
$("settingsBtn").addEventListener("click", () => $("settings").classList.toggle("hidden"));
$("search").addEventListener("input", render);
$("filter").addEventListener("change", render);
$("sort").addEventListener("change", render);
$("list").addEventListener("click", onAction);
$("testBtn").addEventListener("click", () => doLogin(true));
$("loginForm").addEventListener("submit", (e) => { e.preventDefault(); doLogin(false); });

loadHealth();
loadDevices();
