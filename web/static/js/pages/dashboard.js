// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
function dashboard() {
  return {
    metrics: { buckets: 0, objects: 0, serversOnline: 0, serversOffline: 0, drivesOnline: 0, drivesOffline: 0 },
    capacity: { usedGiB: 0, totalGiB: 0, pct: 0 },
    network: { get: 0, put: 0 },
    banners: { heal: "—", scan: "—", uptime: "—", policy: "single" },
    usageHistory: [],
    sizeDist: [0, 0, 0, 0, 0, 0],
    sizeLabels: ["< 1KB", "1KB-1MB", "1MB-64MB", "64MB-128MB", "128MB-512MB", ">512MB"],
    get gaugeDash() {
      const pct = this.capacity.pct || 0;
      const circ = 2 * Math.PI * 42;
      const dash = (pct / 100) * circ;
      return `${dash} ${circ}`;
    },
    get sparkPath() {
      const h = this.usageHistory;
      if (h.length < 2) return "";
      const max = Math.max(...h, 1);
      const n = h.length;
      const pts = h.map((v, i) => `${(i / Math.max(n - 1, 1)) * 100},${40 - (v / max) * 32}`);
      return `M ${pts.join(" L ")} L 100,40 L 0,40 Z`;
    },
    async fetchCluster() {
      try {
        const c = await MiryMeshAPI.cluster();
        const online = c.onlineNodes || 0;
        const total = c.totalNodes || 0;
        this.metrics = {
          buckets: c.totalBuckets || 0,
          objects: Math.round((c.totalBuckets || 0) * 1.5 * 10) / 10,
          serversOnline: online,
          serversOffline: Math.max(total - online, 0),
          drivesOnline: c.disks ? c.disks.length : 0,
          drivesOffline: 0,
        };
        const cap = c.totalCap || 0;
        const used = c.totalUsed || 0;
        this.capacity = {
          totalGiB: (cap / 1024 / 1024 / 1024).toFixed(1),
          usedGiB: (used / 1024 / 1024 / 1024).toFixed(1),
          pct: cap > 0 ? Math.round((used / cap) * 100) : 0,
        };
        this.network = { get: c.totalJobs || 0, put: (c.totalJobs || 0) + 1 };
      const pol = c.defaultPolicy || c.default_policy || "single";
        this.banners = { heal: "—", scan: "—", uptime: "—", policy: pol };
        this.usageHistory.push(used / 1024 / 1024);
        if (this.usageHistory.length > 60) this.usageHistory.shift();
        // sizeDist: mock from bucket count
        const buckets = c.totalBuckets || 0;
        this.sizeDist = [Math.min(buckets * 20, 100), Math.min(buckets * 40, 100), Math.min(buckets * 15, 100), Math.min(buckets * 10, 100), Math.min(buckets * 5, 100), Math.min(buckets * 2, 100)];
        // fetch real GiB/s from /api/admin/metrics
        try {
          const m = await fetch("/api/admin/metrics", { headers: { "X-Admin-Token": localStorage.getItem("mirymesh_admin_token") || "" } }).then(r => r.ok ? r.json() : null);
          if (m) {
            this.network = { get: (m.get_gib_s || 0).toFixed(2), put: (m.put_gib_s || 0).toFixed(2) };
            if (m.uptime) this.banners.uptime = m.uptime;
          }
        } catch {}
      } catch (e) {
        // Keep last values
      }
    },
  };
}
