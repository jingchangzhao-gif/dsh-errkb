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
      "<h2>" +
      escapeText(candidate.title) +
      "</h2>" +
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
