"use strict";
// Kergui Fibre Manager — dashboard. Vanilla JS over the REST API served by
// `kergui serve`. No build step, no dependencies.

const $ = (id) => document.getElementById(id);
let devices = [];
let wifiBlocked = new Set();
let usageByMac = {};

function fmtBytes(n) {
  if (n >= 1e9) return (n / 1e9).toFixed(2).replace(".", ",") + " Go";
  if (n >= 1e6) return (n / 1e6).toFixed(1).replace(".", ",") + " Mo";
  return Math.round(n / 1e3) + " Ko";
}
let knownNames = {};
try { knownNames = JSON.parse(localStorage.getItem("kergui.names") || "{}"); } catch (_) { /* private mode */ }

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
    if (location.hash !== "#" + target) history.replaceState(null, "", "#" + target);
    $("tab-devices").classList.toggle("hidden", target !== "devices");
    $("tab-filter").classList.toggle("hidden", target !== "filter");
    $("tab-logs").classList.toggle("hidden", target !== "logs");
    $("tab-history").classList.toggle("hidden", target !== "history");
    if (target === "history") loadHistory();
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
    for (const d of devices) knownNames[d.MAC] = displayName(d);
    try { localStorage.setItem("kergui.names", JSON.stringify(knownNames)); } catch (_) { /* ignore */ }
  }
  const u = await api("/api/usage");
  usageByMac = {};
  if (u.ok && Array.isArray(u.body)) for (const e of u.body) usageByMac[e.mac] = e.bytes;
  const w = await api("/api/wifi/blocked");
  wifiBlocked = new Set(w.ok && Array.isArray(w.body) ? w.body : []);
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
  if (wifiBlocked.has(d.MAC)) return `<span class="pill bad">Wi-Fi coupé</span>`;
  if (d.Blocked) return `<span class="pill bad">Internet coupé</span>`;
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
    const act = (d.Blocked
      ? `<button class="btn sm" data-act="unblock" data-mac="${d.MAC}">Rétablir Internet</button>`
      : `<button class="btn sm ghost" data-act="block" data-mac="${d.MAC}">Couper Internet</button>`)
      + (wifiBlocked.has(d.MAC)
        ? `<button class="btn sm" data-act="wifi-unblock" data-mac="${d.MAC}">Rétablir le Wi-Fi</button>`
        : `<button class="btn sm danger" data-act="wifi-block" data-mac="${d.MAC}">Couper le Wi-Fi</button>`);
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
        <span>Conso du mois : <strong>${usageByMac[d.MAC] ? fmtBytes(usageByMac[d.MAC]) : "—"}</strong></span>
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
  const byMac = new Map();
  for (const d of devices) if (d.Blocked) byMac.set(d.MAC, { mac: d.MAC, name: displayName(d), net: true, wifi: false });
  for (const mac of wifiBlocked) {
    const e = byMac.get(mac) || { mac, name: knownNames[mac] || mac, net: false };
    e.wifi = true;
    byMac.set(mac, e);
  }
  const container = $("blocklist");
  container.innerHTML = "";
  $("blocklistEmpty").classList.toggle("hidden", byMac.size > 0);
  for (const e of byMac.values()) {
    const what = [e.net && "Internet coupé", e.wifi && "Wi-Fi coupé"].filter(Boolean).join(" · ");
    const el = document.createElement("div");
    el.className = "block-item";
    el.innerHTML = `
      <span class="bi-dot"></span>
      <div class="bi-info">
        <div class="bi-name">${esc(e.name)}</div>
        <div class="bi-mac">${esc(e.mac)} — ${what}</div>
      </div>
      ${e.net ? `<button class="btn sm" data-act="unblock" data-mac="${e.mac}">Rétablir Internet</button>` : ""}
      ${e.wifi ? `<button class="btn sm" data-act="wifi-unblock" data-mac="${e.mac}">Rétablir le Wi-Fi</button>` : ""}`;
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
  // block / unblock / wifi-block / wifi-unblock
  if (act === "wifi-block" &&
      !confirm(`Couper le Wi-Fi de ${knownNames[mac] || mac} ?\n\nL'appareil sera refusé par la box. Le Wi-Fi peut se réinitialiser quelques secondes pour tous les appareils.`)) return;
  const label = btn.textContent;
  btn.disabled = true;
  btn.textContent = "…";
  const r = await api(`/api/devices/${mac}/${act}`, { method: "POST" });
  btn.disabled = false;
  btn.textContent = label;
  const done = {
    "block": "Internet coupé pour cet appareil.",
    "unblock": "Internet rétabli.",
    "wifi-block": "Wi-Fi coupé : l'appareil est refusé par la box.",
    "wifi-unblock": "Wi-Fi rétabli : l'appareil peut se reconnecter.",
  };
  if (r.ok) { toast(done[act]); loadDevices(); }
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

// --- Historique & paiements ---

const hist = { page: 1, pages: 1, items: [] };
const STATUS_FR = { pending: "en attente", paid: "payé", cancelled: "annulé", failed: "échoué", refunded: "remboursé" };
const fcfa = (n) => Number(n || 0).toLocaleString("fr-FR") + " F";

function statusPillHTML(st) {
  const cls = { paid: "ok", pending: "new", cancelled: "off", failed: "bad", refunded: "off" }[st] || "off";
  return `<span class="pill ${cls}">${esc(STATUS_FR[st] || st)}</span>`;
}

// The stored "connected" flag is the state at the last refresh; only trust it if
// that refresh is recent.
function isOnline(it) {
  return it.connected && Date.now() - new Date(it.last_seen).getTime() < 10 * 60 * 1000;
}

async function loadHistory() {
  const q = encodeURIComponent($("histSearch").value.trim());
  const size = $("histSize").value;
  const r = await api(`/api/history?page=${hist.page}&size=${size}&q=${q}`);
  if (!r.ok) { toast(`Historique indisponible (${r.status}): ${(r.body && r.body.error) || ""}`); return; }
  const b = r.body;
  Object.assign(hist, { page: b.page, pages: b.pages, items: b.items });
  $("histPeriod").textContent = b.period;
  if (document.activeElement !== $("pricePerGB")) $("pricePerGB").value = b.settings.price_per_gb;
  if (document.activeElement !== $("minAmount")) $("minAmount").value = b.settings.min_amount;
  const ps = $("payState");
  ps.textContent = b.payments_enabled ? "NabooPay actif" : "NabooPay non configuré";
  ps.className = "pill " + (b.payments_enabled ? "ok" : "bad");

  const list = $("histList");
  list.replaceChildren();
  $("histEmpty").classList.toggle("hidden", b.items.length > 0);
  for (const it of b.items) {
    const el = document.createElement("article");
    el.className = "hist-item";
    const name = it.name || knownNames[it.mac] || it.mac;
    el.innerHTML = `
      <div class="hist-main">
        <div class="dname">${esc(name)} ${isOnline(it) ? `<span class="pill ok">en ligne</span>` : ""}</div>
        <div class="meta"><code>${esc(it.mac)}</code><span>IP ${esc(it.ip || "—")}</span>
          <span>Vu ${fmt(it.last_seen)}</span></div>
      </div>
      <div class="hist-num"><span class="lbl">Conso du mois</span><strong>${it.bytes ? fmtBytes(it.bytes) : "—"}</strong></div>
      <div class="hist-num"><span class="lbl">Reste à payer</span>
        <strong>${it.due_bytes ? fcfa(it.estimate_xof) : "—"}</strong>
        ${it.due_bytes && it.due_bytes !== it.bytes ? `<small class="muted">${fmtBytes(it.due_bytes)}</small>` : ""}</div>
      <div class="hist-pay">
        ${it.pending ? `${statusPillHTML("pending")} <small class="muted">${fcfa(it.pending.amount_xof)}</small>` : ""}
        <button class="btn sm" data-pay="${esc(it.mac)}" ${it.due_bytes ? "" : "disabled"}>Tester le paiement</button>
      </div>`;
    list.appendChild(el);
  }
  $("histInfo").textContent = `Page ${b.page} / ${b.pages} — ${b.total} appareil${b.total > 1 ? "s" : ""}`;
  $("histPrev").disabled = b.page <= 1;
  $("histNext").disabled = b.page >= b.pages;
}

let histSearchTimer;
$("histSearch").addEventListener("input", () => {
  clearTimeout(histSearchTimer);
  histSearchTimer = setTimeout(() => { hist.page = 1; loadHistory(); }, 300);
});
$("histSize").addEventListener("change", () => { hist.page = 1; loadHistory(); });
$("histPrev").addEventListener("click", () => { if (hist.page > 1) { hist.page--; loadHistory(); } });
$("histNext").addEventListener("click", () => { if (hist.page < hist.pages) { hist.page++; loadHistory(); } });

$("priceForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const r = await api("/api/billing/settings", {
    method: "PUT", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ price_per_gb: Number($("pricePerGB").value), min_amount: Number($("minAmount").value) }),
  });
  if (r.ok) { toast("Tarif enregistré."); loadHistory(); }
  else toast(`Échec (${r.status}): ${(r.body && r.body.error) || ""}`);
});

// Payment dialog
let payMac = null;
let payCurrent = null;

function showPayment(p) {
  payCurrent = p;
  $("payResult").classList.remove("hidden");
  $("payAmount").textContent = fcfa(p.amount_xof);
  const st = $("payStatus");
  st.textContent = STATUS_FR[p.status] || p.status;
  st.className = "pill " + ({ paid: "ok", pending: "new" }[p.status] || "off");
  $("payLink").href = p.checkout_url;
  const pending = p.status === "pending";
  $("payLink").classList.toggle("hidden", !pending);
  $("payCopy").classList.toggle("hidden", !pending);
  $("payCancel").classList.toggle("hidden", !pending);
  $("paySubmit").textContent = pending ? "Recréer le lien (montant à jour)" : "Créer un nouveau lien";
}

function payError(msg) {
  $("payError").textContent = msg || "";
  $("payError").classList.toggle("hidden", !msg);
}

$("histList").addEventListener("click", (e) => {
  const btn = e.target.closest("button[data-pay]");
  if (!btn) return;
  const it = hist.items.find((x) => x.mac === btn.dataset.pay);
  if (!it) return;
  payMac = it.mac;
  payCurrent = null;
  payError("");
  $("payResult").classList.add("hidden");
  $("paySubmit").textContent = "Créer le lien de paiement";
  $("payWhat").textContent = `${it.name || it.mac} — ${fmtBytes(it.due_bytes)} non payés ce mois, soit ${fcfa(it.estimate_xof)} (+ frais NabooPay).`;
  try {
    const saved = JSON.parse(localStorage.getItem("kergui.payer." + payMac) || "{}");
    $("payName").value = saved.name || it.name || "";
    $("payPhone").value = saved.phone || "";
  } catch (_) { /* ignore */ }
  if (it.pending) showPayment(it.pending);
  $("payDialog").showModal();
});

$("payForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const btn = $("paySubmit");
  btn.disabled = true;
  payError("");
  const body = { customer_name: $("payName").value.trim(), phone: $("payPhone").value.trim() };
  const r = await api(`/api/devices/${payMac}/payments`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
  });
  btn.disabled = false;
  if (!r.ok) { payError((r.body && r.body.error) || `Erreur ${r.status}`); return; }
  try { localStorage.setItem("kergui.payer." + payMac, JSON.stringify({ name: body.customer_name, phone: body.phone })); } catch (_) { /* ignore */ }
  showPayment(r.body);
  loadHistory();
});

$("payRefresh").addEventListener("click", async () => {
  if (!payCurrent) return;
  const r = await api(`/api/payments/${payCurrent.id}/refresh`, { method: "POST" });
  if (!r.ok) { payError((r.body && r.body.error) || `Erreur ${r.status}`); return; }
  showPayment(r.body);
  if (r.body.status === "paid") { toast("Paiement reçu ✔"); loadHistory(); }
  else toast(`Statut : ${STATUS_FR[r.body.status] || r.body.status}`);
});

$("payCancel").addEventListener("click", async () => {
  if (!payCurrent || !confirm("Annuler ce lien de paiement ?")) return;
  const r = await api(`/api/payments/${payCurrent.id}/cancel`, { method: "POST" });
  if (!r.ok) { payError((r.body && r.body.error) || `Erreur ${r.status}`); return; }
  showPayment(r.body);
  loadHistory();
});

$("payCopy").addEventListener("click", async () => {
  try { await navigator.clipboard.writeText($("payLink").href); toast("Lien copié."); }
  catch (_) { prompt("Copiez le lien :", $("payLink").href); }
});
$("payClose").addEventListener("click", () => $("payDialog").close());

// Deep link: /#history, /#logs, ...
{
  const fromHash = document.querySelector(`.tab[data-tab="${CSS.escape(location.hash.slice(1))}"]`);
  if (fromHash) fromHash.click();
}
