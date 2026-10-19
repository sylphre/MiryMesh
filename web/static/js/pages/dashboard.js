// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
async function initDashboard() {
  const el = document.getElementById("cluster");
  const nodesEl = document.getElementById("nodes");
  const bucketsEl = document.getElementById("buckets");
  const jobsEl = document.getElementById("jobs");
  MiryMeshAPI.renderState(el, { loading: true });
  try {
    const c = await MiryMeshAPI.cluster();
    el.textContent = `Online ${c.onlineNodes}/${c.totalNodes} nodes, ${c.totalBuckets} buckets, ${c.totalJobs} jobs`;
    el.className = "card";
    const esc = MiryMeshAPI.escapeHTML;
    nodesEl.innerHTML = c.nodes.map((n) => `<div>${esc(n.ID)} <span class="badge badge--${esc(n.Status)}">${esc(n.Status)}</span></div>`).join("") || `<span class="state--empty">—</span>`;
    bucketsEl.innerHTML = c.buckets.map((b) => `<div>${esc(b)}</div>`).join("") || `<span class="state--empty">—</span>`;
    jobsEl.innerHTML = c.jobs.map((j) => `<div>${esc(j.ID)} <span class="badge">${esc(j.Status)}</span></div>`).join("") || `<span class="state--empty">—</span>`;
  } catch (e) {
    MiryMeshAPI.renderState(el, { error: e });
  }
}
initDashboard();
