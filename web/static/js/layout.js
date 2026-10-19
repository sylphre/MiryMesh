// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
// Fallback nav renderer for plain static hosting (no html/template). Generates
// the topbar links in one place so 5 HTML files don't duplicate markup.
(function () {
  const NAV = [
    { href: "/static/index.html", label: "Dashboard", key: "dashboard" },
    { href: "/static/nodes.html", label: "Nodes", key: "nodes" },
    { href: "/static/disks.html", label: "Disks", key: "disks" },
    { href: "/static/buckets.html", label: "Buckets", key: "buckets" },
    { href: "/static/jobs.html", label: "Jobs", key: "jobs" },
    { href: "/api-doc", label: "API Doc", key: "api-doc" },
  ];
  function renderTopbar(activeKey) {
    const nav = document.querySelector(".topbar__nav");
    if (!nav) return;
    // If nav already populated (static HTML), just toggle active class.
    if (nav.children.length > 0) {
      for (const a of nav.children) {
        const isActive = a.getAttribute("href") === NAV.find((n) => n.key === activeKey)?.href;
        a.classList.toggle("topbar__link--active", isActive);
      }
      return;
    }
    for (const item of NAV) {
      const a = document.createElement("a");
      a.href = item.href;
      a.textContent = item.label;
      a.className = "topbar__link" + (item.key === activeKey ? " topbar__link--active" : "");
      nav.appendChild(a);
    }
  }
  window.MiryMeshLayout = { renderTopbar };
})();
