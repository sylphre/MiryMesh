// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
async function initDisks() {
  const el = document.getElementById("disks");
  MiryMeshAPI.renderState(el, { loading: true });
  try {
    const data = await MiryMeshAPI.disks();
    if (!data.disks.length) { MiryMeshAPI.renderState(el, { empty: "No disks" }); return; }
    const rows = data.disks.map((d) => [d.NodeID, d.Path, String(d.Capacity), String(d.Used), d.Limit ? String(d.Limit) : "95% (auto)"]);
    el.innerHTML = MiryMeshAPI.renderTable(["Node", "Path", "Capacity", "Used", "Limit"], rows);
    el.className = "card";
  } catch (e) {
    MiryMeshAPI.renderState(el, { error: e });
  }
}
initDisks();
