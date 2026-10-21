"use strict";

const token = document.querySelector('meta[name="errkb-token"]').content;
const input = document.getElementById("query");
const status = document.getElementById("status");
const hits = document.getElementById("hits");
let sequence = 0;

function escapeText(value) {
  const node = document.createElement("span");
  node.textContent = value;
  return node.innerHTML;
}

function setStatus(text, isError) {
  status.textContent = text;
  status.className = isError ? "error" : "";
}

function renderEmpty() {
  hits.innerHTML =
    '<li class="empty">No matching issue yet. Record it first: <code>errkb record</code> — or capture the failure with <code>errkb run -- &lt;command&gt;</code>.</li>';
}

function renderHits(candidates) {
  hits.innerHTML = "";
  if (!candidates || candidates.length === 0) {
    renderEmpty();
    return;
  }
  for (const candidate of candidates.slice(0, 3)) {
    const item = document.createElement("li");
    item.className = "hit";
    const badge = candidate.approximate
      ? "APPROXIMATE — confirm before reuse"
      : "exact";
    const badgeClass = candidate.approximate ? "approximate" : "exact";
    item.innerHTML =
      '<h2><a href="/issues/' +
      encodeURIComponent(candidate.issue_id) +
      '">' +
      escapeText(candidate.title) +
      "</a></h2>" +
      '<div class="meta">' +
      '<span class="badge ' +
      badgeClass +
      '">' +
      badge +
      "</span>" +
      "<span>" +
      escapeText(candidate.match_reason) +
      "</span>" +
      "<span>issue " +
      escapeText(candidate.issue_id) +
      "</span>" +
      "<span>fingerprint " +
      escapeText(candidate.fingerprint) +
      "</span>" +
      (typeof candidate.hits === "number"
        ? "<span>" + candidate.hits + " hit(s)</span>"
        : "") +
      "</div>" +
      (candidate.match_detail
        ? '<p class="reason">' + escapeText(candidate.match_detail) + "</p>"
        : "") +
      (candidate.solution && candidate.solution.body
        ? '<pre class="fix">' + escapeText(candidate.solution.body) + "</pre>"
        : "");
    hits.appendChild(item);
  }
}

async function search() {
  const query = input.value.trim();
  if (query === "") {
    hits.innerHTML = "";
    setStatus("");
    return;
  }
  const ticket = ++sequence;
  setStatus("searching…");
  try {
    const response = await fetch("/api/search", {
      method: "POST",
      headers: { "content-type": "application/json", "x-errkb-token": token },
      body: JSON.stringify({ query }),
    });
    if (ticket !== sequence) return;
    if (!response.ok) {
      const payload = await response.json().catch(() => ({}));
      setStatus(payload.message || "search failed", true);
      return;
    }
    const payload = await response.json();
    const candidates = payload.candidates || [];
    setStatus(
      candidates.length === 0
        ? ""
        : candidates.length + " candidate(s), newest relevance first",
    );
    renderHits(candidates);
  } catch (error) {
    if (ticket === sequence) setStatus("the errkb core is not reachable", true);
  }
}

let timer;
input.addEventListener("input", () => {
  clearTimeout(timer);
  timer = setTimeout(search, 180);
});
input.addEventListener("keydown", (event) => {
  if (event.key === "Enter") {
    clearTimeout(timer);
    search();
  }
});

// ---- detail page (WF-005) -------------------------------------------------
function environmentText(environment) {
  return Object.keys(environment || {})
    .sort()
    .map((key) => key + "=" + environment[key])
    .join(", ");
}

function renderVersions(solutions) {
  const versions = [];
  for (const solution of solutions || []) {
    for (const version of solution.versions || []) {
      versions.push({ status: solution.status, version });
    }
  }
  versions.sort((left, right) => (right.version.version || 0) - (left.version.version || 0));
  if (versions.length === 0) return "<p>no solution recorded yet</p>";
  return versions
    .map((entry) => {
      const version = entry.version;
      const copy = [version.body || ""];
      if (version.source) copy.push("source: " + version.source);
      if (version.applies_to) copy.push("applies to: " + version.applies_to);
      if (version.risks) copy.push("risks: " + version.risks);
      return (
        '<article class="version">' +
        "<h3>v" + version.version + " · " + escapeText(entry.status) + "</h3>" +
        "<pre>" + escapeText(version.body) + "</pre>" +
        '<p class="meta">' +
        (version.source ? "<span>source " + escapeText(version.source) + "</span>" : "") +
        (version.applies_to ? "<span>applies to " + escapeText(version.applies_to) + "</span>" : "") +
        (version.risks ? "<span>risks " + escapeText(version.risks) + "</span>" : "") +
        (version.preconditions
          ? "<span>preconditions " + escapeText(version.preconditions) + "</span>"
          : "") +
        "</p>" +
        '<button type="button" data-copy="' + escapeText(copy.join("\n")) + '">Copy fix</button>' +
        "</article>"
      );
    })
    .join("");
}

function renderDetail(detail) {
  const counts = { success: 0, failure: 0, unknown: 0 };
  for (const outcome of detail.outcomes || []) {
    if (counts[outcome.outcome] !== undefined) counts[outcome.outcome] += 1;
  }
  const environmentRows = (detail.occurrences || [])
    .map((occurrence) => {
      const text = environmentText(occurrence.environment);
      return (
        "<li>" +
        escapeText(occurrence.headline || occurrence.id) +
        (text ? ' <span class="env">' + escapeText(text) + "</span>" : "") +
        "</li>"
      );
    })
    .join("");
  const trust = detail.trust || {};
  return (
    '<p><a href="/">← back to search</a></p>' +
    "<h2>" + escapeText(detail.title) + "</h2>" +
    '<p class="meta"><span>issue ' + escapeText(detail.id) + "</span>" +
    "<span>fingerprint " + escapeText(detail.fingerprint) + "</span>" +
    "<span>" + escapeText(detail.status) + "</span>" +
    "<span>" + detail.hits + " hit(s)</span></p>" +
    '<p class="reason">trust ' + Number(trust.score || 0).toFixed(2) +
    " (based on " + Number(trust.samples || 0) + " recorded outcome(s); read-only)</p>" +
    "<h3>Environment</h3><ul>" + (environmentRows || "<li>not recorded</li>") + "</ul>" +
    "<h3>Solution history (newest first)</h3>" + renderVersions(detail.solutions) +
    "<h3>Outcomes</h3><p>success " + counts.success + " · failure " + counts.failure +
    " · unknown " + counts.unknown + "</p>"
  );
}

async function loadDetail() {
  const match = location.pathname.match(/^\/issues\/([^/]+)$/);
  if (!match) return;
  const issueID = decodeURIComponent(match[1]);
  const main = document.querySelector("main");
  main.innerHTML = '<p id="status" role="status">loading…</p>';
  try {
    const response = await fetch("/api/issues/" + encodeURIComponent(issueID), {
      headers: { "x-errkb-token": token },
    });
    if (!response.ok) {
      const payload = await response.json().catch(() => ({}));
      main.innerHTML =
        '<p class="error">' + escapeText(payload.message || "could not load the issue") + "</p>" +
        '<p><a href="/">back to search</a></p>';
      return;
    }
    const payload = await response.json();
    main.innerHTML = renderDetail(payload.issue || {});
    for (const button of main.querySelectorAll("[data-copy]")) {
      button.addEventListener("click", async () => {
        try {
          await navigator.clipboard.writeText(button.getAttribute("data-copy"));
          button.textContent = "Copied";
        } catch (error) {
          button.textContent = "Copy failed";
        }
      });
    }
    main.insertAdjacentHTML("beforeend", editSection());
    wireEdit(issueID, main);
  } catch (error) {
    main.innerHTML = '<p class="error">the errkb core is not reachable</p>';
  }
}

function editSection() {
  return (
    '<section class="edit">' +
    "<h3>Record a fix</h3>" +
    '<form id="version-form">' +
    '<label>What fixes it? <textarea id="version-body" required></textarea></label>' +
    '<label>Preconditions <input id="version-preconditions" /></label>' +
    '<label>Source <input id="version-source" value="toolbox" /></label>' +
    '<button type="submit">Add version</button>' +
    "</form>" +
    "<h3>Issue status</h3>" +
    '<p class="status-buttons">' +
    '<button type="button" data-status="resolved">Mark resolved</button>' +
    '<button type="button" data-status="open">Mark open</button>' +
    '<button type="button" data-status="wontfix">Mark wontfix</button>' +
    '<button type="button" data-status="misjudged">Mark misjudged</button>' +
    "</p>" +
    '<p id="edit-status" role="status"></p>' +
    "</section>"
  );
}

function wireEdit(issueID, main) {
  const status = main.querySelector("#edit-status");
  const say = (text, isError) => {
    status.textContent = text;
    status.className = isError ? "error" : "";
  };
  const form = main.querySelector("#version-form");
  if (form) {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      say("saving…");
      try {
        const response = await fetch(
          "/api/issues/" + encodeURIComponent(issueID) + "/versions",
          {
            method: "POST",
            headers: { "content-type": "application/json", "x-errkb-token": token },
            body: JSON.stringify({
              body: main.querySelector("#version-body").value,
              preconditions: main.querySelector("#version-preconditions").value,
              source: main.querySelector("#version-source").value,
            }),
          },
        );
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          say(payload.message || "the daemon refused the write", true);
          return;
        }
        location.reload();
      } catch (error) {
        say("the errkb core is not reachable", true);
      }
    });
  }
  for (const button of main.querySelectorAll("[data-status]")) {
    button.addEventListener("click", async () => {
      say("saving…");
      try {
        const response = await fetch(
          "/api/issues/" + encodeURIComponent(issueID) + "/status",
          {
            method: "POST",
            headers: { "content-type": "application/json", "x-errkb-token": token },
            body: JSON.stringify({ status: button.getAttribute("data-status") }),
          },
        );
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          say(payload.message || "the daemon refused the write", true);
          return;
        }
        location.reload();
      } catch (error) {
        say("the errkb core is not reachable", true);
      }
    });
  }
}

loadDetail();
