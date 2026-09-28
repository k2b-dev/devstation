// Comments shared by the image viewer and the plan panel: loading
// comments.jsonl, sending, the header count, and list entries with the done
// checkbox and the trash button. Views subscribe with on() and draw again
// after every load.
var devComments = (function () {
  var config = document.getElementById("comment-config");
  if (!config) return null;
  var cfg = config.dataset;
  var D = { version: Number(cfg.version), comments: [], counts: {}, loaded: false, error: "" }, subs = [];

  D.el = function (tag, cls, content) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (content) e.textContent = content;
    return e;
  };

  // comments.jsonl is a log of comments, resolve events, and the places of
  // deleted comments. Numbers count per version and file, as in `dev comments`.
  function parse(body) {
    var byID = {}, numbers = {}, out = [];
    body.split("\n").forEach(function (line) {
      var e;
      try { e = JSON.parse(line); } catch (err) { return; }
      var key = e.version + "/" + e.path;
      if (e.type === "comment") {
        numbers[key] = (numbers[key] || 0) + 1;
        byID[e.id] = { id: e.id, path: e.path, version: e.version, number: numbers[key], x: e.x, y: e.y, anchor: e.anchor || null, text: e.text, at: e.at, resolved: false };
        out.push(byID[e.id]);
      } else if (e.type === "deleted") { // keeps its number, so the others keep theirs
        numbers[key] = (numbers[key] || 0) + 1;
      } else if (e.type === "resolve" && byID[e.id]) {
        byID[e.id].resolved = !!e.resolved;
      }
    });
    D.comments = out;
    D.counts = numbers;
  }

  // load never fails: an error is kept in D.error with the last comments.
  D.load = function () {
    return fetch(cfg.comments, { cache: "no-store" })
      .then(function (r) {
        if (r.ok) return r.text();
        if (r.status === 404) return ""; // no comments yet
        throw new Error("Could not load comments (HTTP " + r.status + ").");
      }, function () { throw new Error("Could not load comments."); })
      .then(function (body) { parse(body); D.error = ""; }, function (err) { D.error = err.message; })
      .then(function () { D.loaded = true; D.redraw(); });
  };
  D.on = function (fn) { subs.push(fn); };
  D.redraw = function () {
    var open = D.comments.filter(function (c) { return !c.resolved; }).length;
    var header = document.querySelector(".open-comments");
    if (header) {
      header.hidden = !open;
      header.textContent = open + (open === 1 ? " open comment" : " open comments");
    }
    // One view failing does not blank the others.
    subs.forEach(function (fn) { try { fn(); } catch (e) { setTimeout(function () { throw e; }); } });
  };
  D.next = function (path) { return (D.counts[D.version + "/" + path] || 0) + 1; };

  // post sends to the comment API: "" adds, "/resolve" and "/delete" change.
  D.post = function (action, body) {
    var down = "The Devstation daemon is not reachable (dev daemon).";
    return fetch(cfg.api + action, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) })
      .then(function (r) {
        if (r.ok) return r;
        return r.json().then(function (j) { throw new Error(j.error || "HTTP " + r.status); }, function () { throw new Error(down); });
      }, function () { throw new Error(down); });
  };

  var TRASH = '<svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true"><path fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" d="M2.5 4h11M6.5 4V2.5h3V4M4 4l.7 9.5h6.6L12 4M6.8 6.5v4.5M9.2 6.5v4.5"/></svg>';

  // item is one list entry. opts: label (instead of "#n"), where (what it
  // points at; a button when onSelect is set), name (prefix for the controls'
  // names, e.g. the file), active, onError, onEnter, onSelect.
  D.item = function (c, opts) {
    var li = D.el("li", (c.resolved ? "resolved" : "") + (opts.active ? " active" : ""));
    li.dataset.id = c.id;
    var head = D.el("div", "c-head");
    head.appendChild(D.el("span", "num", opts.label || "#" + c.number + (c.version !== D.version ? " · v" + c.version : "")));
    if (opts.where) {
      var target = D.el(opts.onSelect ? "button" : "span", "where", opts.where);
      if (opts.onSelect) { target.type = "button"; target.addEventListener("click", opts.onSelect); }
      head.appendChild(target);
    }
    head.appendChild(D.el("time", "", new Date(c.at).toLocaleString(undefined, { dateStyle: "short", timeStyle: "short" })));
    var check = D.el("input", "done");
    check.type = "checkbox";
    check.checked = c.resolved;
    check.title = "Done";
    check.setAttribute("aria-label", "Mark " + (opts.name || "") + "#" + c.number + " done");
    check.addEventListener("change", function () {
      change("/resolve", { ids: [c.id], resolved: check.checked }, function () { // keep the keyboard focus
        document.querySelectorAll("li[data-id] input.done").forEach(function (box) {
          if (box.closest("li").dataset.id === c.id) box.focus();
        });
      });
    });
    head.appendChild(check);
    head.appendChild(trash(c, change, opts.name || ""));
    li.appendChild(head);
    li.appendChild(D.el("p", "", c.text));
    if (opts.onEnter) li.addEventListener("mouseenter", opts.onEnter);
    if (opts.onSelect) li.addEventListener("click", function (e) { if (!e.target.closest("input, button")) opts.onSelect(); });
    // change sends, loads, and puts the focus back if drawing again lost it.
    function change(action, body, refocus) {
      var list = li.parentNode;
      if (opts.onError) opts.onError(null);
      D.post(action, body)
        .then(D.load, function (err) { if (opts.onError) opts.onError(err); D.redraw(); })
        .then(function () {
          if (document.activeElement && document.activeElement !== document.body) return;
          if (refocus) refocus();
          if (document.activeElement === document.body && list) list.focus();
        });
    }
    return li;
  };

  // The first click arms the button, a second one within three seconds
  // deletes: deleting cannot be undone.
  function trash(c, change, name) {
    var b = D.el("button", "trash");
    b.type = "button";
    function idle() { b.classList.remove("armed"); b.innerHTML = TRASH; b.title = "Delete"; b.setAttribute("aria-label", "Delete " + name + "#" + c.number); }
    idle();
    b.addEventListener("click", function () {
      if (!b.classList.contains("armed")) {
        b.classList.add("armed");
        b.textContent = "Delete?";
        b.setAttribute("aria-label", "Confirm: delete " + name + "#" + c.number);
        setTimeout(function () { if (b.isConnected) idle(); }, 3000);
        return;
      }
      change("/delete", { ids: [c.id] });
    });
    return b;
  }
  // D.panel runs the comment panel of plan and review pages: opening and
  // closing, the list, and the composer. The page supplies what differs:
  //   mine(c)          comments on this page's version, which have markers
  //   name(c)          prefix for the names of a comment's controls, or ""
  //   onPage(c)        comments on this page's files in any version
  //   where(c)         what a comment points at, e.g. "plan.md:3 · “quote”"
  //   order(a, b)      list order of mine
  //   pending()        {label, body} of the anchor being placed, or null
  //   plain()          the body of a comment without one, e.g. {path}, or
  //                    null when nothing here can be commented
  //   drop()           forget the pending anchor
  //   mark(id, scroll) show the marker of the active comment
  //   earlier(c)       the address of a comment on another version
  D.panel = function (o) {
    var panel = document.getElementById("comment-panel"), toggle = document.querySelector(".cp-toggle");
    function q(s) { return panel.querySelector(s); }
    var list = q(".cp-list"), form = q(".cp-form"), text = q("textarea"), send = q(".cp-send"), anchor = q(".cp-anchor"), status = q(".cp-error"), count = q(".cp-count");
    var P = { active: "", text: text }, busy = false, loadError = false;
    function openCount() { return D.comments.filter(function (c) { return o.onPage(c) && !c.resolved; }).length; }
    function showError(err) { status.textContent = err ? err.message : ""; loadError = false; }
    // open moves the focus into the panel before the toggle disappears.
    P.open = function (focus) {
      panel.hidden = false;
      document.documentElement.classList.add("cp-open");
      if (focus || !list.querySelector("li[data-id]")) text.focus();
      else list.focus();
      toggle.hidden = true;
    };
    P.close = function () {
      panel.hidden = true;
      document.documentElement.classList.remove("cp-open");
      toggle.hidden = false;
      toggle.focus();
    };
    // update shows the pending anchor; the page calls it when that changes.
    P.update = function () {
      var p = o.pending();
      anchor.hidden = !p;
      anchor.firstChild.textContent = p ? p.label : "";
      panel.classList.toggle("anchored", !!p);
      toggle.textContent = p ? "Comment on selection" : "Comments" + (openCount() ? " · " + openCount() : "");
    };
    P.activate = function (id, scroll) {
      P.active = id;
      list.querySelectorAll("li").forEach(function (li) { li.classList.toggle("active", li.dataset.id === id); });
      var li = list.querySelector("li.active");
      if (li) li.scrollIntoView({ block: "nearest" });
      o.mark(id, scroll);
    };
    function draw() {
      list.replaceChildren();
      var here = D.comments.filter(o.mine).sort(o.order);
      var earlier = D.comments.filter(function (c) { return o.onPage(c) && c.version !== D.version && !c.resolved; });
      if (!here.length && !earlier.length) list.appendChild(D.el("li", "empty", !D.loaded ? "Loading comments…" : D.error ? "Comments could not be loaded." : "No comments here yet."));
      here.forEach(function (c) {
        list.appendChild(D.item(c, { where: o.where(c), name: o.name(c), active: c.id === P.active, onError: showError, onSelect: function () { P.activate(c.id, true); } }));
      });
      if (earlier.length) list.appendChild(D.el("li", "cp-earlier", "Other versions"));
      earlier.forEach(function (c) {
        list.appendChild(D.item(c, { where: o.where(c), name: o.name(c) + "v" + c.version + " ", onError: showError, onSelect: function () { location.href = o.earlier(c); } }));
      });
      count.textContent = openCount() ? openCount() + " open" : "";
      send.disabled = busy || !D.loaded;
      P.update();
      if (D.error) { showError(new Error(D.error)); loadError = true; } else if (loadError) showError(null);
    }
    D.on(draw);
    // Keep the page's selection when pressing the button.
    toggle.addEventListener("pointerdown", function (e) { e.preventDefault(); });
    toggle.addEventListener("click", function () { P.open(!!o.pending() || !openCount()); });
    q(".cp-close").addEventListener("click", P.close);
    anchor.querySelector("button").addEventListener("click", function () { o.drop(); P.update(); text.focus(); });
    form.addEventListener("submit", function (e) {
      e.preventDefault();
      var sent = text.value.trim(), p = o.pending();
      if (!sent || busy || !D.loaded) return;
      var body = p ? p.body : o.plain();
      if (!body) { showError(new Error("Open a page of the mockup to comment on it.")); return; }
      body.version = D.version;
      body.text = sent;
      busy = true;
      send.disabled = true;
      showError(null);
      D.post("", body).then(function (r) { return r.json(); }).then(function (saved) {
        // A daemon older than this page drops anchors it does not know.
        if (body.anchor && !saved.anchor) showError(new Error("Saved without its place: the Devstation daemon is older than this page and needs a restart."));
        if (text.value.trim() === sent) text.value = "";
        if (p && o.pending() === p) o.drop();
        return D.load();
      }, showError).then(function () {
        busy = false;
        send.disabled = !D.loaded;
      });
    });
    text.addEventListener("keydown", function (e) {
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); form.requestSubmit(); }
    });
    panel.addEventListener("keydown", function (e) {
      if (e.key !== "Escape") return;
      if (o.pending()) { o.drop(); P.update(); } else P.close();
    });
    // #comment-ID opens the panel on that comment.
    var target = location.hash.match(/^#comment-([0-9a-f]+)$/);
    D.on(function () {
      if (!target) return;
      var c = D.comments.filter(function (c) { return c.id === target[1] && o.mine(c); })[0];
      target = null;
      if (c) { P.open(false); P.activate(c.id, true); }
    });
    return P;
  };

  setTimeout(D.load); // after every view has subscribed
  return D;
})();
