// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
async function initJobs() {
  const el = document.getElementById("jobs");
  MiryMeshAPI.renderState(el, { loading: true });
  try {
    const data = await MiryMeshAPI.jobs();
    if (!data.jobs.length) { MiryMeshAPI.renderState(el, { empty: "No jobs" }); return; }
    const rows = data.jobs.map((j) => [j.ID, `${j.Bucket}/${j.Key}`, j.Status, (j.Targets || []).join(", ")]);
    el.innerHTML = MiryMeshAPI.renderTable(["ID", "Bucket/Key", "Status", "Targets"], rows);
    el.className = "card";
  } catch (e) {
    MiryMeshAPI.renderState(el, { error: e });
  }
}
initJobs();
