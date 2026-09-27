(function () {
  var items = [].slice.call(document.querySelectorAll("figure.cell"));
  var box = document.getElementById("lightbox");
  if (!items.length || !box || !box.showModal) return;
  function q(selector) { return box.querySelector(selector); }
  var stage = q(".lb-stage"), name = q(".lb-name"), meta = q(".lb-meta"), count = q(".lb-count"), original = q(".lb-original");
  var list = q(".lb-list"), form = q(".lb-form"), text = q("textarea"), send = q(".lb-actions button"), note = q(".lb-pin-note"), status = q(".lb-error");
  var version = Number(box.dataset.version), api = box.dataset.api, source = box.dataset.comments;
  var current = 0, comments = [], loaded = false, loadError = "", busy = false, pending = null, active = "";
  var drafts = {}; // unsent text and pin per file while browsing

  function href(f) { return f.querySelector("a.thumb").getAttribute("href"); }
  function el(tag, cls, content) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (content) e.textContent = content;
    return e;
  }

  // comments.jsonl is an append-only log: comments, then resolve events.
  // Numbers count per version and file, as in `dev comments`.
  function parse(body) {
    var byID = {}, numbers = {}, out = [];
    body.split("\n").forEach(function (line) {
      var e;
      try { e = JSON.parse(line); } catch (err) { return; }
      if (e.type === "comment") {
        var key = e.version + "/" + e.path;
        numbers[key] = (numbers[key] || 0) + 1;
        byID[e.id] = { id: e.id, path: e.path, version: e.version, number: numbers[key], x: e.x, y: e.y, text: e.text, at: e.at, resolved: false };
        out.push(byID[e.id]);
      } else if (e.type === "resolve" && byID[e.id]) {
        byID[e.id].resolved = !!e.resolved;
      }
    });
    return out;
  }
  // load never fails: errors show in the viewer and keep the last comments.
  function load() {
    return fetch(source, { cache: "no-store" })
      .then(function (r) {
        if (r.ok) return r.text();
        if (r.status === 404) return ""; // no comments yet
        throw new Error("Could not load comments (HTTP " + r.status + ").");
      }, function () { throw new Error("Could not load comments."); })
      .then(function (body) { comments = parse(body); loadError = ""; }, function (err) { loadError = err.message; showError(err); })
      .then(function () {
        loaded = true;
        badges();
        if (box.open) render();
      });
  }
  function forPath(path) { return comments.filter(function (c) { return c.path === path; }); }
  function badges() {
    var total = 0;
    items.forEach(function (f) {
      var open = forPath(f.id).filter(function (c) { return !c.resolved; }).length;
      total += open;
      var badge = f.querySelector(".pins");
      if (!open) { if (badge) badge.remove(); return; }
      if (!badge) { badge = el("span", "pins"); f.querySelector("figcaption").appendChild(badge); }
      badge.textContent = open + (open === 1 ? " comment" : " comments");
    });
    var header = document.querySelector(".open-comments");
    if (header) {
      header.hidden = !total;
      header.textContent = total + (total === 1 ? " open comment" : " open comments");
    }
  }

  function post(url, body) {
    return fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) })
      .then(function (r) {
        if (r.ok) return r;
        return r.json().then(
          function (j) { throw new Error(j.error || "HTTP " + r.status); },
          function () { throw new Error("The Devstation daemon is not reachable (dev daemon)."); });
      }, function () { throw new Error("The Devstation daemon is not reachable (dev daemon)."); });
  }

  function nextNumber() {
    var path = items[current].id;
    return forPath(path).filter(function (c) { return c.version === version; }).length + 1;
  }
  function renderPins() {
    [].slice.call(stage.querySelectorAll(".pin")).forEach(function (p) { p.remove(); });
    var path = items[current].id;
    forPath(path).forEach(function (c) {
      if (c.version !== version || c.x == null) return;
      var pin = el("button", "pin" + (c.resolved ? " resolved" : "") + (c.id === active ? " active" : ""), String(c.number));
      pin.dataset.id = c.id;
      pin.type = "button";
      pin.style.left = c.x * 100 + "%";
      pin.style.top = c.y * 100 + "%";
      pin.setAttribute("aria-label", "Comment " + c.number);
      pin.addEventListener("click", function (e) { e.stopPropagation(); highlight(c.id); });
      stage.appendChild(pin);
    });
    if (pending) {
      var p = el("span", "pin pending", String(nextNumber()));
      p.style.left = pending.x * 100 + "%";
      p.style.top = pending.y * 100 + "%";
      stage.appendChild(p);
    }
  }
  function renderList() {
    list.replaceChildren();
    var path = items[current].id;
    var shown = forPath(path).sort(function (a, b) { return (b.version === version) - (a.version === version); });
    if (!shown.length) list.appendChild(el("li", "empty", !loaded ? "Loading comments…" : loadError ? "Comments could not be loaded." : "No comments yet."));
    shown.forEach(function (c) {
      var li = el("li", (c.resolved ? "resolved" : "") + (c.id === active ? " active" : ""));
      li.dataset.id = c.id;
      var head = el("div", "lb-comment-head");
      head.appendChild(el("span", "num", (c.x != null ? "● " : "") + "#" + c.number + (c.version !== version ? " · v" + c.version : "")));
      head.appendChild(el("time", "", new Date(c.at).toLocaleString(undefined, { dateStyle: "short", timeStyle: "short" })));
      var done = el("label", "done");
      var check = el("input");
      check.type = "checkbox";
      check.checked = c.resolved;
      check.setAttribute("aria-label", "Mark #" + c.number + " done");
      check.addEventListener("change", function () {
        status.textContent = "";
        post(api + "/resolve", { ids: [c.id], resolved: check.checked })
          .then(load, function (err) { showError(err); render(); })
          .then(function () { // the list was drawn anew; keep the keyboard focus
            if (document.activeElement && document.activeElement !== document.body) return;
            [].slice.call(list.children).forEach(function (li) {
              if (li.dataset.id === c.id) li.querySelector("input").focus();
            });
          });
      });
      done.appendChild(check);
      done.appendChild(document.createTextNode(" done"));
      head.appendChild(done);
      li.appendChild(head);
      li.appendChild(el("p", "", c.text));
      li.addEventListener("mouseenter", function () { highlight(c.id, true); });
      list.appendChild(li);
    });
    note.textContent = pending ? "Pin " + nextNumber() + " placed" : "";
    send.disabled = busy || !loaded;
  }
  function render() { renderPins(); renderList(); }
  // Highlighting only toggles classes, so elements under the pointer stay.
  function highlight(id, quiet) {
    active = id;
    [].slice.call(box.querySelectorAll(".lb-list li, .lb-stage .pin")).forEach(function (e) {
      e.classList.toggle("active", e.dataset.id === id);
    });
    if (!quiet) {
      var li = list.querySelector("li.active");
      if (li) li.scrollIntoView({ block: "nearest" });
    }
  }
  function showError(err) { status.textContent = err.message; }

  function show(n) {
    if (box.open) drafts[items[current].id] = { text: text.value, pending: pending };
    current = (n + items.length) % items.length;
    var f = items[current];
    var media = document.createElement(f.dataset.video ? "video" : "img");
    media.src = href(f);
    if (f.dataset.video) { media.controls = true; media.muted = true; media.loop = true; media.autoplay = true; media.playsInline = true; }
    else {
      media.alt = f.dataset.name;
      media.addEventListener("click", function (e) {
        var r = media.getBoundingClientRect();
        pending = { x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)), y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)) };
        render();
        text.focus();
      });
    }
    stage.replaceChildren(media);
    q(".lb-media").scrollTop = 0;
    var draft = drafts[f.id] || {};
    text.value = draft.text || "";
    pending = draft.pending || null;
    active = "";
    status.textContent = loadError;
    name.textContent = f.dataset.name;
    meta.textContent = [f.dataset.section, f.dataset.row, f.dataset.col].filter(Boolean).join(" · ");
    count.textContent = current + 1 + " / " + items.length;
    original.href = href(f);
    history.replaceState(null, "", "#" + encodeURIComponent(f.id));
    render();
    [1, -1].forEach(function (k) {
      var g = items[(current + k + items.length) % items.length];
      if (!g.dataset.video) new Image().src = href(g);
    });
  }
  function open(n) { show(n); if (!box.open) box.showModal(); }

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    var path = items[current].id, sent = text.value.trim();
    if (!sent || busy || !loaded) return;
    var body = { path: path, version: version, text: sent };
    if (pending) { body.x = pending.x; body.y = pending.y; }
    busy = true;
    send.disabled = true;
    status.textContent = "";
    post(api, body).then(function () {
      // Clear only what was sent: the viewer may show another file or be
      // closed by now.
      if (box.open && items[current].id === path && text.value.trim() === sent) {
        text.value = "";
        pending = null;
      }
      if (drafts[path] && drafts[path].text.trim() === sent) delete drafts[path];
      return load();
    }, showError).then(function () {
      busy = false;
      send.disabled = !loaded;
    });
  });
  items.forEach(function (f, n) {
    f.querySelector("a.thumb").addEventListener("click", function (e) {
      if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
      e.preventDefault();
      open(n);
    });
  });
  q(".lb-prev").addEventListener("click", function () { show(current - 1); });
  q(".lb-next").addEventListener("click", function () { show(current + 1); });
  q(".lb-close").addEventListener("click", function () { box.close(); });
  // On the document: after opening from a URL anchor, focus may sit outside
  // the dialog. Typing a comment keeps the arrow keys for the text.
  document.addEventListener("keydown", function (e) {
    if (!box.open) return;
    if (e.target === text) {
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); form.requestSubmit(); }
      return;
    }
    if (e.key === "ArrowLeft") { show(current - 1); e.preventDefault(); }
    if (e.key === "ArrowRight") { show(current + 1); e.preventDefault(); }
  });
  // Escape while writing leaves the text field instead of closing the viewer.
  box.addEventListener("cancel", function (e) {
    if (document.activeElement === text && text.value.trim()) { e.preventDefault(); send.focus(); }
  });
  box.addEventListener("close", function () {
    drafts[items[current].id] = { text: text.value, pending: pending };
    stage.replaceChildren();
    items[current].querySelector("a.thumb").focus({ preventScroll: true });
    items[current].scrollIntoView({ block: "center" });
  });
  // Only a mostly horizontal swipe changes the image; scrolling a tall one does not.
  var start = null;
  stage.addEventListener("touchstart", function (e) { start = e.touches.length === 1 ? { x: e.touches[0].clientX, y: e.touches[0].clientY } : null; }, { passive: true });
  stage.addEventListener("touchend", function (e) {
    if (start === null) return;
    var dx = e.changedTouches[0].clientX - start.x, dy = e.changedTouches[0].clientY - start.y;
    start = null;
    if (Math.abs(dx) > 60 && Math.abs(dx) > 2 * Math.abs(dy)) show(current + (dx < 0 ? 1 : -1));
  });
  var target = location.hash.slice(1);
  try { target = decodeURIComponent(target); } catch (e) { /* keep the raw anchor */ }
  for (var i = 0; target && i < items.length; i++) {
    if (items[i].id === target) { open(i); break; }
  }
  load();
})();
