(function () {
  var items = [].slice.call(document.querySelectorAll("figure.cell"));
  var box = document.getElementById("lightbox");
  if (!items.length || !box || !box.showModal) return;
  function q(selector) { return box.querySelector(selector); }
  var caption = q(".lb-caption"), stage = q(".lb-stage"), name = q(".lb-name"), meta = q(".lb-meta"), count = q(".lb-count"), original = q(".lb-original");
  var list = q(".lb-list"), form = q(".lb-form"), text = q("textarea"), send = q(".lb-actions button"), note = q(".lb-pin-note"), status = q(".lb-error");
  var D = devComments;
  if (!D) return;
  var version = D.version, current = 0, busy = false, pending = null, active = "";
  var drafts = {}; // unsent text and pin per file while browsing

  function href(f) { return f.querySelector("a.thumb").getAttribute("href"); }
  var el = D.el;
  function forPath(path) { return D.comments.filter(function (c) { return c.path === path; }); }
  function badges() {
    items.forEach(function (f) {
      var open = forPath(f.id).filter(function (c) { return !c.resolved; }).length;
      var badge = f.querySelector(".pins");
      if (!open) { if (badge) badge.remove(); return; }
      if (!badge) { badge = el("span", "pins"); f.querySelector("figcaption").appendChild(badge); }
      badge.textContent = open + (open === 1 ? " comment" : " comments");
    });
  }
  D.on(function () {
    badges();
    if (D.error) showError(new Error(D.error));
    if (box.open) render();
  });

  function nextNumber() { return D.next(items[current].id); }
  // The circle sits up and to the right of the spot, or on the other side
  // near an edge, so the spot stays visible.
  function place(pin, x, y) {
    pin.style.left = x * 100 + "%";
    pin.style.top = y * 100 + "%";
    pin.classList.toggle("flip-x", x > 0.9);
    pin.classList.toggle("flip-y", y < 0.1);
  }
  function renderPins() {
    [].slice.call(stage.querySelectorAll(".pin")).forEach(function (p) { p.remove(); });
    var path = items[current].id;
    forPath(path).forEach(function (c) {
      if (c.version !== version || c.x == null) return;
      var pin = el("button", "pin" + (c.resolved ? " resolved" : "") + (c.id === active ? " active" : ""), String(c.number));
      pin.dataset.id = c.id;
      pin.type = "button";
      place(pin, c.x, c.y);
      pin.setAttribute("aria-label", "Comment " + c.number);
      pin.addEventListener("click", function (e) { e.stopPropagation(); highlight(c.id); });
      stage.appendChild(pin);
    });
    if (pending) {
      var p = el("span", "pin pending", String(nextNumber()));
      place(p, pending.x, pending.y);
      stage.appendChild(p);
    }
  }
  function renderList() {
    list.replaceChildren();
    var path = items[current].id;
    var shown = forPath(path).sort(function (a, b) { return (b.version === version) - (a.version === version); });
    if (!shown.length) list.appendChild(el("li", "empty", !D.loaded ? "Loading comments…" : D.error ? "Comments could not be loaded." : "No comments yet."));
    shown.forEach(function (c) {
      list.appendChild(D.item(c, {
        label: (c.x != null ? "● " : "") + "#" + c.number + (c.version !== version ? " · v" + c.version : ""),
        active: c.id === active, onError: showError,
        onEnter: function () { highlight(c.id, true); },
      }));
    });
    note.textContent = pending ? "Pin " + nextNumber() + " placed" : "";
    send.disabled = busy || !D.loaded;
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
  function showError(err) { status.textContent = err ? err.message : ""; }

  // Zoom: null fits the image into the viewer, a number scales its natural
  // size. Pins sit in percent of the image, so they follow.
  var zoom = null, fitted = 1, scroller = q(".lb-media"), zoomBar = q(".lb-zoom"), zoomLevel = q(".lb-zoom-level");
  var STEPS = [0.5, 0.75, 1, 1.5, 2, 3, 4];
  function image() {
    var m = stage.firstElementChild;
    return m && m.tagName === "IMG" && m.naturalWidth ? m : null;
  }
  // setZoom keeps the point under (cx, cy), or the middle of the view, in place.
  function setZoom(z, cx, cy) {
    var img = image();
    if (!img) return;
    var before = img.getBoundingClientRect(), view = scroller.getBoundingClientRect();
    if (zoom === null) fitted = before.width / img.naturalWidth;
    if (z !== null && z <= fitted * 1.01) z = null;
    if (cx == null) { cx = view.left + view.width / 2; cy = view.top + view.height / 2; }
    var fx = (cx - before.left) / before.width, fy = (cy - before.top) / before.height;
    zoom = z;
    stage.classList.toggle("zoomed", z !== null);
    img.style.width = z === null ? "" : Math.round(img.naturalWidth * z) + "px";
    zoomLevel.textContent = z === null ? "Fit" : Math.round(z * 100) + "%";
    var after = img.getBoundingClientRect();
    scroller.scrollLeft += after.left + fx * after.width - cx;
    scroller.scrollTop += after.top + fy * after.height - cy;
  }
  function stepZoom(dir) {
    var img = image();
    if (!img) return;
    var now = zoom === null ? img.getBoundingClientRect().width / img.naturalWidth : zoom;
    var next = null;
    STEPS.forEach(function (s) { // skip steps that would barely change the size
      if (dir > 0 && s > now * 1.15 && next === null) next = s;
      if (dir < 0 && s < now / 1.15) next = s;
    });
    if (next !== null) setZoom(next);
    else if (dir < 0) setZoom(null);
  }
  q(".lb-zoom-in").addEventListener("click", function () { stepZoom(1); });
  q(".lb-zoom-out").addEventListener("click", function () { stepZoom(-1); });
  zoomLevel.addEventListener("click", function () { setZoom(zoom === null ? 1 : null); });
  // Ctrl/⌘/⌥ + wheel, and pinching on a trackpad, zoom smoothly at the pointer.
  scroller.addEventListener("wheel", function (e) {
    var img = image();
    if (!(e.ctrlKey || e.metaKey || e.altKey) || !img) return;
    e.preventDefault();
    var now = zoom === null ? img.getBoundingClientRect().width / img.naturalWidth : zoom;
    setZoom(Math.min(4, now * Math.exp(-e.deltaY * 0.002)), e.clientX, e.clientY);
  }, { passive: false });

  // Holding ⌥ turns the pointer into a hand: dragging moves the zoomed image,
  // and a click does not place a pin.
  var drag = null;
  function hand(on) { stage.classList.toggle("hand", on); }
  document.addEventListener("keydown", function (e) { if (e.key === "Alt" && box.open) hand(true); });
  document.addEventListener("keyup", function (e) { if (e.key === "Alt") hand(false); });
  window.addEventListener("blur", function () { hand(false); });
  stage.addEventListener("pointermove", function (e) {
    hand(e.altKey || !!drag);
    if (!drag) return;
    scroller.scrollLeft = drag.left - (e.clientX - drag.x);
    scroller.scrollTop = drag.top - (e.clientY - drag.y);
  });
  stage.addEventListener("pointerdown", function (e) {
    if (!e.altKey || !image()) return;
    e.preventDefault();
    drag = { x: e.clientX, y: e.clientY, left: scroller.scrollLeft, top: scroller.scrollTop };
    stage.setPointerCapture(e.pointerId);
    stage.classList.add("dragging");
  });
  function endDrag() { drag = null; stage.classList.remove("dragging"); }
  stage.addEventListener("pointerup", endDrag);
  stage.addEventListener("pointercancel", endDrag);

  function show(n) {
    if (box.open) drafts[items[current].id] = { text: text.value, pending: pending };
    current = (n + items.length) % items.length;
    var f = items[current];
    var media = document.createElement(f.dataset.video ? "video" : "img");
    media.src = href(f);
    if (f.dataset.video) { media.controls = true; media.muted = true; media.loop = true; media.autoplay = true; media.playsInline = true; }
    else {
      media.alt = f.dataset.name;
      media.draggable = false; // the browser's own image dragging would fight the pointer
      media.addEventListener("click", function (e) {
        if (e.altKey) return;
        var r = media.getBoundingClientRect();
        pending = { x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)), y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)) };
        render();
        text.focus();
      });
    }
    stage.replaceChildren(media);
    zoom = null;
    stage.classList.remove("zoomed");
    zoomLevel.textContent = "Fit";
    zoomBar.hidden = !!f.dataset.video;
    scroller.scrollTop = 0;
    var draft = drafts[f.id] || {};
    text.value = draft.text || "";
    pending = draft.pending || null;
    active = "";
    status.textContent = D.error;
    name.textContent = f.dataset.name;
    meta.textContent = [f.dataset.section, f.dataset.row, f.dataset.col].filter(Boolean).join(" · ");
    caption.hidden = !f.dataset.title;
    caption.firstChild.textContent = f.dataset.title || "";
    caption.lastChild.textContent = f.dataset.text || "";
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
    if (!sent || busy || !D.loaded) return;
    var body = { path: path, version: version, text: sent };
    if (pending) { body.x = pending.x; body.y = pending.y; }
    busy = true;
    send.disabled = true;
    status.textContent = "";
    D.post("", body).then(function () {
      // Clear only what was sent: the viewer may show another file or be
      // closed by now.
      if (box.open && items[current].id === path && text.value.trim() === sent) {
        text.value = "";
        pending = null;
      }
      if (drafts[path] && drafts[path].text.trim() === sent) delete drafts[path];
      return D.load();
    }, showError).then(function () {
      busy = false;
      send.disabled = !D.loaded;
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
    if (!e.ctrlKey && !e.metaKey && !e.altKey) {
      if (e.key === "+" || e.key === "=") { stepZoom(1); e.preventDefault(); }
      if (e.key === "-") { stepZoom(-1); e.preventDefault(); }
      if (e.key === "0") { setZoom(zoom === null ? 1 : null); e.preventDefault(); }
    }
    // Zoomed in, the arrow keys move across the image instead of to the next one.
    if (zoom !== null && (e.key === "ArrowLeft" || e.key === "ArrowRight")) {
      scroller.scrollLeft += e.key === "ArrowLeft" ? -80 : 80;
      e.preventDefault();
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
    if (start === null || zoom !== null) return;
    var dx = e.changedTouches[0].clientX - start.x, dy = e.changedTouches[0].clientY - start.y;
    start = null;
    if (Math.abs(dx) > 60 && Math.abs(dx) > 2 * Math.abs(dy)) show(current + (dx < 0 ? 1 : -1));
  });
  var target = location.hash.slice(1);
  try { target = decodeURIComponent(target); } catch (e) { /* keep the raw anchor */ }
  for (var i = 0; target && i < items.length; i++) {
    if (items[i].id === target) { open(i); break; }
  }
})();
