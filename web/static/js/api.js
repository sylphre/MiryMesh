// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
// MiryMesh admin API helper. Plain JS, no build, no IIFE.
const MiryMeshAPI = (() => {
  const base = "/api/admin";
  function escapeHTML(s) {
    return String(s).replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;").replaceAll("'", "&#39;");
  }
  async function request(method, path, body) {
    const opts = { method, headers: { "Content-Type": "application/json" } };
    if (body !== undefined) opts.body = JSON.stringify(body);
    const res = await fetch(base + path, opts);
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || res.statusText);
    }
    const ct = res.headers.get("content-type") || "";
    if (ct.includes("application/json")) return res.json();
    return res.text();
  }
  async function get(path) { return request("GET", path); }
  async function post(path, body) { return request("POST", path, body); }
  async function put(path, body) { return request("PUT", path, body); }
  function renderState(el, state) {
    // state: {loading:true} | {empty:"No nodes"} | {error:Error|string} | {html:string}
    if (!el) return;
    if (state.loading) { el.textContent = "Loading…"; el.className = "card state--loading"; return; }
    if (state.error) { const msg = state.error.message || String(state.error); el.textContent = "Error: " + msg; el.className = "card state--error"; return; }
    if (state.empty) { el.textContent = state.empty; el.className = "card state--empty"; return; }
    if (state.html !== undefined) { el.innerHTML = state.html; el.className = "card"; }
  }
  function renderTable(headers, rows) {
    if (!rows || rows.length === 0) return "";
    const thead = `<thead><tr>${headers.map((h) => `<th class="table__head">${escapeHTML(h)}</th>`).join("")}</tr></thead>`;
    const tbody = `<tbody>${rows.map((row) => `<tr>${row.map((c) => `<td>${escapeHTML(c)}</td>`).join("")}</tr>`).join("")}</tbody>`;
    return `<table class="table">${thead}${tbody}</table>`;
  }
  return {
    cluster: () => get("/cluster"),
    nodes: () => get("/nodes"),
    disks: () => get("/disks"),
    buckets: () => get("/buckets"),
    createBucket: (name) => post("/buckets", { name }),
    getBucketPolicy: (b) => get(`/buckets/${encodeURIComponent(b)}/policy`),
    setBucketPolicy: (b, policy) => put(`/buckets/${encodeURIComponent(b)}/policy`, { policy }),
    jobs: () => get("/jobs"),
    escapeHTML,
    renderState,
    renderTable,
  };
})();
