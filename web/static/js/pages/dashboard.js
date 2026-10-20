// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
async function initDashboard() {
  const el = document.getElementById("cluster");
  const nodesEl = document.getElementById("nodes");
  const bucketsEl = document.getElementById("buckets");
  const jobsEl = document.getElementById("jobs");
  const metricNodes = document.getElementById("metric-nodes");
  const metricBuckets = document.getElementById("metric-buckets");
  const metricJobs = document.getElementById("metric-jobs");
  const capacityEl = document.getElementById("capacity");
  const placementEl = document.getElementById("placement");
  MiryMeshAPI.renderState(el, { loading: true });
  try {
    const c = await MiryMeshAPI.cluster();
    const online = c.onlineNodes || 0;
    const total = c.totalNodes || 0;
    el.textContent = `Online ${online}/${total} nodes · ${c.totalBuckets} buckets · ${c.totalJobs} jobs`;
    el.className = "card";
    if (metricNodes) metricNodes.textContent = `${online}/${total}`;
    if (metricBuckets) metricBuckets.textContent = String(c.totalBuckets);
    if (metricJobs) metricJobs.textContent = String(c.totalJobs);
    const esc = MiryMeshAPI.escapeHTML;
    nodesEl.innerHTML = (c.nodes || []).map((n) => `<div style="display:flex;justify-content:space-between;align-items:center;padding:6px 0;border-bottom:1px solid var(--color-border);"><span>${esc(n.ID)}</span><span class="badge badge--${esc(n.Status)}">${esc(n.Status)}</span></div>`).join("") || `<span class="state--empty">—</span>`;
    bucketsEl.innerHTML = (c.buckets || []).map((b) => `<div style="padding:4px 0;font-size:14px;">${esc(b)}</div>`).join("") || `<span class="state--empty">—</span>`;
    jobsEl.innerHTML = (c.jobs || []).map((j) => `<div style="display:flex;justify-content:space-between;padding:4px 0;"><span>${esc(j.ID)}</span><span class="badge badge--${esc(j.Status)}">${esc(j.Status)}</span></div>`).join("") || `<span class="state--empty">—</span>`;
    if (capacityEl) {
      const cap = c.totalCap || 0;
      const used = c.totalUsed || 0;
      const pct = cap > 0 ? Math.round((used / cap) * 100) : 0;
      capacityEl.innerHTML = `<div style="display:flex;justify-content:space-between;align-items:baseline;"><span style="font-size:22px;font-weight:600;">${pct}%</span><span class="caption">${(used / 1024 / 1024).toFixed(0)} / ${(cap / 1024 / 1024).toFixed(0)} MiB</span></div><div style="height:6px;background:var(--color-border);border-radius:999px;margin-top:10px;overflow:hidden;"><div style="width:${pct}%;height:100%;background:var(--color-primary);border-radius:999px;"></div></div>`;
    }
    if (placementEl) {
      const pol = c.defaultPolicy || c.default_policy || "single";
      placementEl.innerHTML = `<span class="badge badge--policy">${esc(pol)}</span><span class="caption" style="margin-left:8px;">default placement</span>`;
    }
  } catch (e) {
    MiryMeshAPI.renderState(el, { error: e });
    if (capacityEl) MiryMeshAPI.renderState(capacityEl, { error: e });
    if (placementEl) MiryMeshAPI.renderState(placementEl, { error: e });
  }
}
initDashboard();
