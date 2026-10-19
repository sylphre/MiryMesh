// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
async function initNodes() {
  const el = document.getElementById("nodes");
  MiryMeshAPI.renderState(el, { loading: true });
  try {
    const data = await MiryMeshAPI.nodes();
    if (!data.nodes.length) { MiryMeshAPI.renderState(el, { empty: "No nodes" }); return; }
    const rows = data.nodes.map((n) => [n.ID, n.Addr, n.Status]);
    el.innerHTML = MiryMeshAPI.renderTable(["ID", "Addr", "Status"], rows);
    el.className = "card";
  } catch (e) {
    MiryMeshAPI.renderState(el, { error: e });
  }
}
initNodes();
