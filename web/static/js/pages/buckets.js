// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
async function initBuckets() {
  const listEl = document.getElementById("buckets");
  const form = document.getElementById("create-bucket-form");
  async function load() {
    MiryMeshAPI.renderState(listEl, { loading: true });
    let data;
    try { data = await MiryMeshAPI.buckets(); } catch (e) { MiryMeshAPI.renderState(listEl, { error: e }); return; }
    if (!data.buckets.length) { MiryMeshAPI.renderState(listEl, { empty: "No buckets" }); return; }
    // Prefer single cluster call to avoid N+1 policy fetches.
    const policyMap = {};
    try {
      const cluster = await MiryMeshAPI.cluster();
      // cluster.buckets is string[]; policies not yet in cluster, so fallback to per-bucket only if needed.
      // For now use cached policies from cluster if available (future: include in cluster).
      void cluster;
    } catch {}
    const esc = MiryMeshAPI.escapeHTML;
    if (Object.keys(policyMap).length === 0) {
      // Fetch policies sequentially with escape; still N+1 but isolated for now.
      // TODO: extend /api/admin/buckets to return policies in one call to eliminate N+1.
      for (const b of data.buckets) {
        try { const p = await MiryMeshAPI.getBucketPolicy(b); policyMap[b] = p.policy; } catch { policyMap[b] = "?"; }
      }
    }
    listEl.innerHTML = data.buckets.map((b) => {
      const pol = policyMap[b] || "?";
      return `<div>${esc(b)} <span class="badge">${esc(pol)}</span> <select data-bucket="${esc(b)}" class="policy-select"><option value="single" ${pol==="single"?"selected":""}>single</option><option value="dual" ${pol==="dual"?"selected":""}>dual</option><option value="triple" ${pol==="triple"?"selected":""}>triple</option><option value="ALL" ${pol==="ALL"?"selected":""}>ALL</option></select></div>`;
    }).join("");
    listEl.className = "card";
    listEl.querySelectorAll(".policy-select").forEach((sel) => {
      sel.addEventListener("change", async () => {
        try { await MiryMeshAPI.setBucketPolicy(sel.dataset.bucket, sel.value); } catch (e) { alert(e.message); }
      });
    });
  }
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = form.elements.name.value;
    try { await MiryMeshAPI.createBucket(name); form.reset(); await load(); } catch (err) { alert(err.message); }
  });
  await load();
}
initBuckets();
