(function () {
  var table = document.getElementById("artifact-table");
  if (!table) return;
  var body = table.tBodies[0], rows = Array.prototype.slice.call(body.rows);
  var search = document.querySelector(".tsearch"), count = document.querySelector(".tcount"), empty = document.querySelector(".tempty");
  var heads = table.querySelectorAll("th[data-key]");
  heads.forEach(function (th) {
    var b = document.createElement("button");
    b.type = "button";
    b.textContent = th.textContent;
    th.textContent = "";
    th.appendChild(b);
    b.addEventListener("click", function () {
      var dir = th.getAttribute("aria-sort") === "descending" ? "ascending" : th.getAttribute("aria-sort") === "ascending" ? "descending" : th.dataset.type === "num" ? "descending" : "ascending";
      heads.forEach(function (h) { h.removeAttribute("aria-sort"); });
      th.setAttribute("aria-sort", dir);
      sortBy(th.dataset.key, th.dataset.type, dir);
    });
  });
  function sortBy(key, type, dir) {
    var sign = dir === "ascending" ? 1 : -1;
    rows.sort(function (a, b) {
      var x = a.dataset[key], y = b.dataset[key];
      var d = type === "num" ? Number(x) - Number(y) : x.localeCompare(y, undefined, { numeric: true, sensitivity: "base" });
      return d * sign || Number(b.dataset.updated) - Number(a.dataset.updated);
    });
    rows.forEach(function (r) { body.appendChild(r); });
  }
  function filter() {
    var q = search.value.trim().toLowerCase().split(/\s+/).filter(Boolean), shown = 0;
    rows.forEach(function (r) {
      var text = r.textContent.toLowerCase(), hit = q.every(function (w) { return text.indexOf(w) >= 0; });
      r.hidden = !hit;
      if (hit) shown++;
    });
    count.textContent = q.length ? shown + " of " + rows.length : "";
    empty.hidden = shown > 0;
  }
  search.addEventListener("input", filter);
  search.addEventListener("keydown", function (e) { if (e.key === "Escape") { search.value = ""; filter(); } });
})();
