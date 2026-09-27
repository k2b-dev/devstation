(function () {
  var items = [].slice.call(document.querySelectorAll("figure.cell"));
  var box = document.getElementById("lightbox");
  if (!items.length || !box || !box.showModal) return;
  var media = box.querySelector(".lb-media");
  var name = box.querySelector(".lb-name");
  var meta = box.querySelector(".lb-meta");
  var count = box.querySelector(".lb-count");
  var original = box.querySelector(".lb-original");
  var current = 0;
  function href(f) { return f.querySelector("a.thumb").getAttribute("href"); }
  function show(n) {
    current = (n + items.length) % items.length;
    var f = items[current];
    var el = document.createElement(f.dataset.video ? "video" : "img");
    el.src = href(f);
    if (f.dataset.video) { el.controls = true; el.muted = true; el.loop = true; el.autoplay = true; el.playsInline = true; }
    else { el.alt = f.dataset.name; }
    media.replaceChildren(el);
    media.scrollTop = 0;
    name.textContent = f.dataset.name;
    meta.textContent = [f.dataset.section, f.dataset.row, f.dataset.col].filter(Boolean).join(" · ");
    count.textContent = current + 1 + " / " + items.length;
    original.href = href(f);
    history.replaceState(null, "", "#" + encodeURIComponent(f.id));
    [1, -1].forEach(function (k) {
      var g = items[(current + k + items.length) % items.length];
      if (!g.dataset.video) new Image().src = href(g);
    });
  }
  function open(n) { show(n); if (!box.open) box.showModal(); }
  items.forEach(function (f, n) {
    f.querySelector("a.thumb").addEventListener("click", function (e) {
      if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
      e.preventDefault();
      open(n);
    });
  });
  box.querySelector(".lb-prev").addEventListener("click", function () { show(current - 1); });
  box.querySelector(".lb-next").addEventListener("click", function () { show(current + 1); });
  box.querySelector(".lb-close").addEventListener("click", function () { box.close(); });
  box.addEventListener("click", function (e) { if (e.target === box || e.target === media) box.close(); });
  // On the document: after opening from a URL anchor, focus may sit outside
  // the dialog.
  document.addEventListener("keydown", function (e) {
    if (!box.open) return;
    if (e.key === "ArrowLeft") { show(current - 1); e.preventDefault(); }
    if (e.key === "ArrowRight") { show(current + 1); e.preventDefault(); }
  });
  box.addEventListener("close", function () {
    media.replaceChildren();
    items[current].querySelector("a.thumb").focus({ preventScroll: true });
    items[current].scrollIntoView({ block: "center" });
  });
  var startX = null;
  media.addEventListener("touchstart", function (e) { startX = e.touches.length === 1 ? e.touches[0].clientX : null; }, { passive: true });
  media.addEventListener("touchend", function (e) {
    if (startX === null) return;
    var dx = e.changedTouches[0].clientX - startX;
    startX = null;
    if (Math.abs(dx) > 60) show(current + (dx < 0 ? 1 : -1));
  });
  var target = location.hash.slice(1);
  try { target = decodeURIComponent(target); } catch (e) { /* keep the raw anchor */ }
  for (var i = 0; target && i < items.length; i++) {
    if (items[i].id === target) { open(i); break; }
  }
})();
