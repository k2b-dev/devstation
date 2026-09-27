// Comments inside an HTML mockup. The mockup runs unchanged in a frame of
// the same origin. Comment mode (the button or c) catches the next click in
// the frame and pins it to the element under it; the comment keeps the page,
// the element (selector, text, and the dialog or form around it), the clicks
// since the page loaded, and the viewport. Outside comment mode the mockup
// works as usual.
(function () {
  var D = devComments, frame = document.getElementById("mockup"), config = document.getElementById("comment-config");
  if (!D || !frame || !document.getElementById("comment-panel")) return;
  var overlay = document.querySelector(".rv-overlay"), modeButton = document.querySelector(".rv-mode");
  var label = document.querySelector(".rv-page"), status = document.querySelector(".rv-status");
  var pages = JSON.parse(config.dataset.pages), reviews = JSON.parse(config.dataset.reviews);
  var base = new URL(config.dataset.base, location.href), root = document.getElementById("comment-panel").dataset.root;
  var page = null, steps = [], mode = false, pending = null, hovered = null, pins = {}, loop = 0, lastDoc = null, pinned = false, label1 = null;
  var INTERACTIVE = "a, button, input, select, textarea, label, summary, [role=button], [role=link], [role=tab], [role=menuitem], [role=option], [role=checkbox], [role=switch]";

  function doc() {
    try { return frame.contentDocument; } catch (e) { return null; }
  }
  function clip(s, n) { return s.length > n ? s.slice(0, n - 1) + "…" : s; }
  // clean drops what the comment service refuses: control and text direction characters.
  function clean(s) { return String(s).replace(/[\u0000-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/g, " ").replace(/\s+/g, " ").trim(); }
  function onPage(c) { return pages.indexOf(c.path) >= 0; }
  function mine(c) { return c.version === D.version && onPage(c); }
  function here(c) { return mine(c) && page && c.path === page.path && ((c.anchor && c.anchor.route) || "") === page.route; }

  // where names a comment's target, e.g. index.html#billing · button “Send”.
  function where(c) {
    var a = c.anchor || {}, s = c.path + (a.route || "");
    if (a.selector) s += " · " + a.selector.split(" > ").pop().replace(/[#:.].*/, "") + (a.quote ? " “" + clip(a.quote, 40) + "”" : "");
    if (a.width) s += " · " + a.width + "×" + a.height + " " + a.theme;
    return s;
  }

  // The frame may only show pages of this version.
  function locate() {
    var d = doc();
    if (!d) return null;
    var loc = d.location;
    if (loc.origin !== location.origin || loc.pathname.indexOf(base.pathname) !== 0) return null;
    var rel = decodeURIComponent(loc.pathname.slice(base.pathname.length));
    if (rel === "" || rel.slice(-1) === "/") rel += "index.html";
    return pages.indexOf(rel) < 0 ? null : { path: rel, route: loc.search + loc.hash };
  }
  function go(path, route) {
    var u = new URL(path.split("/").map(encodeURIComponent).join("/") + (route || ""), base);
    if (u.origin === location.origin && u.pathname.indexOf(base.pathname) === 0) frame.src = u.href;
  }

  // selector walks up to an element with a unique id, naming each step by its
  // tag and, among siblings of the same tag, its position.
  function selector(el) {
    var d = el.ownerDocument, parts = [];
    for (var e = el; e && e !== d.documentElement; e = e.parentElement) {
      var tag = e.localName;
      if (e.id && d.querySelectorAll("#" + CSS.escape(e.id)).length === 1) {
        parts.unshift(tag + "#" + CSS.escape(e.id));
        break;
      }
      var same = e.parentElement ? [].filter.call(e.parentElement.children, function (c) { return c.localName === tag; }) : [e];
      parts.unshift(same.length > 1 ? tag + ":nth-of-type(" + (same.indexOf(e) + 1) + ")" : tag);
    }
    // Deep pages give long paths; the end of the path names the element best.
    while (parts.length > 1 && parts.join(" > ").length > 480) parts.shift();
    return parts.join(" > ");
  }
  // target raises a click to the control it belongs to.
  function target(node) {
    var el = node && (node.nodeType === 1 ? node : node.parentElement);
    if (!el) return null;
    var svg = el.closest("svg");
    while (svg && svg.parentElement && svg.parentElement.closest("svg")) svg = svg.parentElement.closest("svg");
    if (svg) el = svg;
    return el.closest(INTERACTIVE) || el;
  }
  function textOf(el) {
    var t = el.getAttribute("aria-label") || el.innerText || el.getAttribute("alt") || el.getAttribute("placeholder") || el.getAttribute("title") || el.value || "";
    return clean(t).slice(0, 200);
  }
  // context names up to three enclosing landmarks, outermost first.
  function context(el) {
    var out = [], titles = [];
    for (var e = el.parentElement; e && out.length < 3; e = e.parentElement) {
      var tag = e.localName, role = e.getAttribute("role"), name = "";
      var heading = e.querySelector("h1, h2, h3, h4, h5, h6, legend");
      var title = clean(e.getAttribute("aria-label") || (heading ? heading.textContent : "")).slice(0, 60);
      if (tag === "dialog" || role === "dialog") name = "dialog";
      else if (["form", "section", "article", "nav", "header", "footer", "aside", "main"].indexOf(tag) >= 0) name = tag;
      if (!name) continue;
      // An inner form named by its dialog's heading keeps just its kind.
      if (["nav", "header", "footer", "main"].indexOf(name) >= 0) title = "";
      out.unshift(name);
      titles.unshift(title);
    }
    return out.map(function (name, i) {
      return titles[i] && titles[i] !== titles[i - 1] ? name + " “" + titles[i] + "”" : name;
    }).join(" › ").slice(0, 300);
  }
  // view is what every comment on a page records: where and how it was seen.
  function view(extra) {
    var w = frame.contentWindow, a = { width: w.innerWidth, height: w.innerHeight, theme: w.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light" };
    if (page.route) a.route = page.route;
    if (steps.length) a.steps = steps.slice();
    Object.keys(extra || {}).forEach(function (k) { if (extra[k]) a[k] = extra[k]; });
    return a;
  }

  function setMode(on) {
    mode = on && !!page;
    modeButton.setAttribute("aria-pressed", String(mode));
    document.documentElement.classList.toggle("rv-commenting", mode);
    status.textContent = mode ? "Click an element to comment · Esc to cancel" : page ? "" : "This page is not part of the mockup.";
    hovered = null;
    kick();
  }
  function pin(el, cx, cy) {
    var r = el.getBoundingClientRect();
    var x = r.width ? Math.min(1, Math.max(0, (cx - r.left) / r.width)) : 0.5, y = r.height ? Math.min(1, Math.max(0, (cy - r.top) / r.height)) : 0.5;
    var a = view({ selector: selector(el), quote: textOf(el), context: context(el) });
    pending = { el: el, x: x, y: y, label: where({ path: page.path, anchor: a }), body: { path: page.path, x: x, y: y, anchor: a } };
    setMode(false);
    P.update();
    P.open(true);
  }

  // Events in the frame: in comment mode, the pointer pins instead of acting
  // (on pointerup, which disabled controls get too); otherwise clicks are
  // recorded as steps.
  var BLOCK = ["pointerdown", "mousedown", "mouseup", "dblclick", "auxclick", "contextmenu", "submit", "touchend"];
  function stop(e) { e.preventDefault(); e.stopImmediatePropagation(); }
  function attach() {
    var d = doc();
    if (!d || d.__devComments) return;
    d.__devComments = true;
    var w = d.defaultView;
    BLOCK.forEach(function (t) {
      w.addEventListener(t, function (e) {
        if (mode || (pinned && (t === "mouseup" || t === "touchend"))) stop(e); // also the rest of the pinning tap
      }, { capture: true, passive: false });
    });
    w.addEventListener("pointerup", function (e) {
      if (!mode) return;
      stop(e);
      var t = target(e.target);
      if (t) { pinned = true; pin(t, e.clientX, e.clientY); }
    }, true);
    w.addEventListener("click", function (e) {
      if (mode || pinned) { pinned = false; stop(e); return; } // the click after a pin
      var t = e.isTrusted && target(e.target);
      if (!t) return;
      // A label passes its click on to its control; record it once.
      if (label1 && label1.control === t) { label1 = null; return; }
      label1 = t.localName === "label" ? t : null;
      var s = selector(t);
      if (s.length <= 500) { steps.push(s); if (steps.length > 20) steps.shift(); }
    }, true);
    w.addEventListener("pointerdown", function () { pinned = false; }, true);
    w.addEventListener("pointermove", function (e) {
      if (!mode) return;
      hovered = target(e.target);
      kick();
    }, true);
    w.addEventListener("keydown", function (e) {
      if (mode && e.key === "Escape") { e.preventDefault(); e.stopImmediatePropagation(); setMode(false); return; }
      if (mode && (e.key === "Enter" || e.key === " ")) {
        var el = target(d.activeElement);
        if (el && el !== d.body) {
          e.preventDefault();
          e.stopImmediatePropagation();
          var r = el.getBoundingClientRect();
          pin(el, r.left + r.width / 2, r.top + r.height / 2);
        }
        return;
      }
      var origin = e.composedPath ? e.composedPath()[0] : e.target; // inside shadow roots too
      if (e.key === "c" && !e.ctrlKey && !e.metaKey && !e.altKey && !(origin.closest && origin.closest("input, textarea, select, [contenteditable]")) && !origin.isContentEditable) {
        e.preventDefault();
        setMode(!mode);
      }
    }, true);
    w.addEventListener("hashchange", follow);
  }
  // loaded starts over with a new document in the frame, as soon as it can
  // be used rather than at its load event, which slow assets delay.
  function loaded() {
    var d = doc();
    if (!d || d === lastDoc || d.readyState === "loading" || d.location.href === "about:blank") return;
    lastDoc = d;
    attach();
    steps = [];
    pending = null;
    setMode(false);
    follow();
  }
  // follow shows the page and route in the frame; a route change keeps the
  // steps, as the page still holds the state they led to.
  function follow() {
    page = locate();
    label.textContent = page ? page.path + page.route : "";
    if (page) history.replaceState(null, "", "#" + encodeURIComponent(page.path + page.route));
    else setMode(false);
    P.update();
    drawPins();
    var c = D.comments.filter(function (c) { return c.id === P.active; })[0];
    if (c && here(c)) P.activate(c.id, true); // a link to a comment on another page lands here
  }
  frame.addEventListener("load", loaded);
  setInterval(loaded, 300);

  // Pins sit in an overlay over the frame. One animation loop keeps them on
  // their elements while anything is shown; a pin whose element is missing,
  // hidden, or covered (by an open dialog, say) is hidden until it is back.
  function drawPins() {
    overlay.replaceChildren();
    pins = {};
    D.comments.forEach(function (c) {
      if (!here(c) || !c.anchor || !c.anchor.selector) return;
      var node = D.el("button", "pin" + (c.resolved ? " resolved" : "") + (c.id === P.active ? " active" : ""), String(c.number));
      node.type = "button";
      node.dataset.id = c.id;
      node.setAttribute("aria-label", "Comment " + c.number);
      node.addEventListener("click", function () { P.open(false); P.activate(c.id); });
      overlay.appendChild(node);
      pins[c.id] = { c: c, node: node, el: null, miss: 0 };
    });
    kick();
  }
  function resolve(d, a) {
    var el = null;
    try { el = d.querySelector(a.selector); } catch (e) { /* not a valid selector here */ }
    if (el && a.quote && textOf(el) !== a.quote) {
      // The structure moved; a unique element of the same tag and text is it.
      var tag = a.selector.split(" > ").pop().replace(/[#:.].*/, "");
      var same = [].filter.call(d.getElementsByTagName(tag), function (e) { return textOf(e) === a.quote; });
      el = same.length === 1 ? same[0] : el;
    }
    return el;
  }
  function place(node, el, x, y) {
    var r = el.getBoundingClientRect(), d = el.ownerDocument;
    var px = r.left + x * r.width, py = r.top + y * r.height;
    var hit = d.elementFromPoint(px, py);
    var shown = r.width > 0 && r.height > 0 && (!el.checkVisibility || el.checkVisibility()) && hit && (hit === el || el.contains(hit));
    node.hidden = !shown;
    if (!shown) return false;
    node.style.left = px + "px";
    node.style.top = py + "px";
    node.classList.toggle("flip-x", px > overlay.clientWidth - 48);
    node.classList.toggle("flip-y", py < 48);
    return true;
  }
  function kick() { if (!loop) loop = requestAnimationFrame(tick); }
  function tick() {
    loop = 0;
    var d = doc(), busy = false;
    if (!d || document.hidden) return;
    Object.keys(pins).forEach(function (id) {
      var p = pins[id];
      busy = true;
      if (!p.el || !p.el.isConnected) {
        if (p.miss-- > 0) { p.node.hidden = true; return; }
        p.el = resolve(d, p.c.anchor);
        p.miss = p.el ? 0 : 30;
      }
      if (!p.el) { p.node.hidden = true; return; }
      place(p.node, p.el, p.c.x, p.c.y);
    });
    var spot = overlay.querySelector(".pin.pending"), outline = overlay.querySelector(".rv-hover");
    if (pending && pending.el && pending.el.isConnected) {
      busy = true;
      if (!spot) { spot = D.el("span", "pin pending", String(D.next(page.path))); overlay.appendChild(spot); }
      place(spot, pending.el, pending.x, pending.y);
    } else if (spot) spot.remove();
    if (mode && hovered && hovered.isConnected) {
      busy = true;
      if (!outline) { outline = D.el("div", "rv-hover"); outline.appendChild(D.el("span")); overlay.appendChild(outline); }
      var r = hovered.getBoundingClientRect();
      outline.style.cssText = "left:" + r.left + "px;top:" + r.top + "px;width:" + r.width + "px;height:" + r.height + "px";
      var name = hovered.localName, quote = textOf(hovered);
      outline.firstChild.textContent = name + (quote ? " · “" + clip(quote, 30) + "”" : "");
    } else if (outline) outline.remove();
    if (busy) loop = requestAnimationFrame(tick);
  }
  document.addEventListener("visibilitychange", kick);

  D.on(drawPins);
  var P = D.panel({
    mine: mine,
    onPage: onPage,
    name: function (c) { return pages.length > 1 ? c.path + " " : ""; },
    where: where,
    order: function (a, b) { return pages.indexOf(a.path) - pages.indexOf(b.path) || a.number - b.number; },
    pending: function () { return pending; },
    plain: function () { return page ? { path: page.path, anchor: view() } : null; },
    drop: function () { pending = null; kick(); },
    mark: function (id) {
      var c = D.comments.filter(function (c) { return c.id === id; })[0];
      if (!c) return;
      if (!here(c)) { go(c.path, c.anchor && c.anchor.route); return; }
      overlay.querySelectorAll(".pin").forEach(function (e) { e.classList.toggle("active", e.dataset.id === id); });
      var p = pins[id];
      if (!p) { status.textContent = ""; return; }
      if (!p.el) p.el = resolve(doc(), c.anchor);
      // Scroll first: an element below the fold is only out of view.
      if (p.el && (!p.el.checkVisibility || p.el.checkVisibility())) p.el.scrollIntoView({ block: "center" });
      if (!(p.el && place(p.node, p.el, c.x, c.y))) status.textContent = "#" + c.number + " is not visible now" + (c.anchor.steps ? "; it was made after clicking " + c.anchor.steps.join(" → ") : "") + ".";
      else status.textContent = "";
    },
    earlier: function (c) {
      return root + (reviews.indexOf(c.version) >= 0 ? "review/" + c.version + "/#comment-" + c.id : "v/" + c.version + "/");
    },
  });

  modeButton.addEventListener("click", function () { setMode(!mode); if (mode) frame.focus(); });
  document.addEventListener("keydown", function (e) {
    var t = e.target;
    if (mode && e.key === "Escape") { setMode(false); return; }
    if (e.key !== "c" || e.ctrlKey || e.metaKey || e.altKey || (t.closest && t.closest("input, textarea, select, [contenteditable]"))) return;
    e.preventDefault();
    setMode(!mode);
    if (mode) frame.focus();
  });

  // The address mirrors the page in the frame, so reloading returns to it.
  var hash = location.hash.slice(1);
  if (hash && !/^comment-/.test(hash)) {
    var wanted = decodeURIComponent(hash), cut = wanted.search(/[?#]/);
    var path = cut < 0 ? wanted : wanted.slice(0, cut), route = cut < 0 ? "" : wanted.slice(cut);
    if (pages.indexOf(path) >= 0) go(path, route);
  }
  loaded();
})();
