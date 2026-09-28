// Comments on the Markdown documents of a page. Select text and press
// Comment (or c): the comment keeps the source lines of the selected blocks
// (data-line="A-B", stamped at publish), the quote, and the heading path. A
// numbered chip marks the block, and the quote is highlighted.
(function () {
  var D = devComments;
  if (!D || !document.getElementById("comment-panel")) return;
  var docs = [].slice.call(document.querySelectorAll("article.doc"));
  var paths = docs.map(function (d) { return d.id; });
  var docSelect = document.querySelector(".cp-doc"), root = document.getElementById("comment-panel").dataset.root;
  var pending = null, ranges = {}, index = null;
  var marks = window.CSS && CSS.highlights && window.Highlight;

  function onPage(c) { return paths.indexOf(c.path) >= 0; }
  function mine(c) { return c.version === D.version && onPage(c); }
  function docOf(node) {
    var e = node && (node.nodeType === 1 ? node : node.parentElement);
    return e && e.closest("article.doc");
  }
  function lines(b) { return b.dataset.line.split("-").map(Number); }
  // block finds the stamped element starting (or, with last, ending) at a
  // line; the innermost one wins, as a loose list item holds its paragraph.
  // The index is built once per drawing.
  function block(doc, line, last) {
    if (!index) {
      index = {};
      docs.forEach(function (d) {
        var i = index[d.id] = { first: {}, last: {} };
        d.querySelectorAll("[data-line]").forEach(function (b) { i.first[lines(b)[0]] = b; i.last[lines(b)[1]] = b; });
      });
    }
    var i = index[doc.id];
    return (i && (last ? i.last : i.first)[line]) || null;
  }
  function blockIn(doc, node) {
    var e = node.nodeType === 1 ? node : node.parentElement;
    var b = e && e.closest("[data-line]");
    return b && doc.contains(b) ? b : null;
  }
  // headingPath names the section of a block, like "Rollout › Phase 2".
  function headingPath(doc, b) {
    var stack = [];
    doc.querySelectorAll("h1, h2, h3, h4, h5, h6").forEach(function (h) {
      if (h !== b && !(h.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)) return;
      var level = Number(h.tagName[1]);
      while (stack.length && stack[stack.length - 1].level >= level) stack.pop();
      stack.push({ level: level, text: h.textContent.trim() });
    });
    return stack.map(function (s) { return s.text; }).join(" › ").slice(0, 300);
  }
  function clip(s, n) { return s.length > n ? s.slice(0, n - 1) + "…" : s; }
  function where(path, a) {
    if (!a || !a.line) return path;
    return path + ":" + a.line + (a.end_line > a.line ? "–" + a.end_line : "") + (a.quote ? " · “" + clip(a.quote, 60) + "”" : "");
  }

  // Chips at the end of commented blocks; the number comes from CSS, so it
  // never ends up in a selection or quote.
  function drawChips() {
    document.querySelectorAll(".c-chip, .c-chips").forEach(function (c) { c.remove(); });
    D.comments.forEach(function (c) {
      var a = c.anchor, doc = document.getElementById(c.path);
      if (!mine(c) || !a || !a.line || !doc) return;
      var b = block(doc, a.line);
      if (!b) return;
      var chip = D.el("button", "c-chip" + (c.resolved ? " resolved" : "") + (c.id === P.active ? " active" : ""));
      chip.type = "button";
      chip.dataset.n = c.number;
      chip.dataset.id = c.id;
      chip.setAttribute("aria-label", "Comment " + c.number);
      chip.addEventListener("click", function () { P.open(false); P.activate(c.id); });
      var host = b.tagName === "TR" || b.tagName === "THEAD" ? b.querySelector("tr > :last-child") || b : b;
      if (host.tagName === "PRE") {
        // On the code block's top edge, outside the part that scrolls sideways.
        var row = host.previousElementSibling;
        if (!row || !row.classList.contains("c-chips")) { row = D.el("div", "c-chips"); host.parentNode.insertBefore(row, host); }
        row.appendChild(chip);
        return;
      }
      // In a list item, the chip follows the item's own text, before any sublist.
      host.insertBefore(chip, host.tagName === "LI" ? host.querySelector(":scope > ul, :scope > ol") : null);
    });
  }

  // findRange locates a quote in the text of its blocks, ignoring
  // differences in white space.
  function findRange(doc, a) {
    var start = block(doc, a.line), end = block(doc, a.end_line || a.line, true) || start;
    if (!start || !a.quote) return null;
    // Walk only the text from the start block to the end of the end block.
    var walker = document.createTreeWalker(doc, NodeFilter.SHOW_TEXT), map = [], flat = "", n;
    walker.currentNode = start;
    while ((n = walker.nextNode())) {
      if (!end.contains(n) && (end.compareDocumentPosition(n) & Node.DOCUMENT_POSITION_FOLLOWING)) break;
      for (var i = 0; i < n.data.length; i++) {
        var ch = /\s/.test(n.data[i]) ? " " : n.data[i];
        if (ch === " " && flat.slice(-1) === " ") continue;
        flat += ch;
        map.push([n, i]);
      }
    }
    var at = flat.indexOf(a.quote);
    if (at < 0) return null;
    var r = document.createRange(), last = map[at + a.quote.length - 1];
    r.setStart(map[at][0], map[at][1]);
    r.setEnd(last[0], last[1] + 1);
    return r;
  }
  // ranges caches the quotes' places; selecting text only redraws the pending one.
  function locate() {
    ranges = {};
    if (!marks) return;
    D.comments.forEach(function (c) {
      if (!mine(c) || c.resolved || !c.anchor) return;
      var r = findRange(document.getElementById(c.path), c.anchor);
      if (r) ranges[c.id] = r;
    });
  }
  function highlight() {
    if (!marks) return;
    var open = new Highlight(), current = new Highlight(), selected = new Highlight();
    Object.keys(ranges).forEach(function (id) { (id === P.active ? current : open).add(ranges[id]); });
    if (pending) selected.add(pending.range);
    CSS.highlights.set("dev-comment", open);
    CSS.highlights.set("dev-comment-active", current);
    CSS.highlights.set("dev-pending", selected);
  }

  D.on(function () { index = null; drawChips(); locate(); highlight(); });
  var P = D.panel({
    mine: mine,
    onPage: onPage,
    name: function (c) { return docs.length > 1 ? c.path + " " : ""; },
    where: function (c) { return where(c.path, c.anchor); },
    order: function (a, b) {
      return paths.indexOf(a.path) - paths.indexOf(b.path) || ((a.anchor && a.anchor.line) || 0) - ((b.anchor && b.anchor.line) || 0) || a.number - b.number;
    },
    pending: function () { return pending; },
    plain: function () { return { path: docSelect ? docSelect.value : paths[0] }; },
    drop: function () {
      if (docSelect && pending) docSelect.value = pending.body.path; // "×" keeps the document
      pending = null;
      if (docSelect) docSelect.hidden = false;
      highlight();
    },
    mark: function (id, scroll) {
      document.querySelectorAll(".c-chip").forEach(function (e) { e.classList.toggle("active", e.dataset.id === id); });
      var chip = document.querySelector(".c-chip.active");
      if (chip && scroll) chip.scrollIntoView({ block: "center", behavior: "smooth" });
      highlight();
    },
    earlier: function (c) { return root + "v/" + c.version + "/#comment-" + c.id; },
  });

  // The last selection inside one document becomes the pending anchor; it
  // stays while focus moves to the comment field.
  document.addEventListener("selectionchange", function () {
    var sel = document.getSelection();
    if (!sel || sel.isCollapsed || !sel.rangeCount) return;
    var r = sel.getRangeAt(0), doc = docOf(r.startContainer);
    var common = r.commonAncestorContainer.nodeType === 1 ? r.commonAncestorContainer : r.commonAncestorContainer.parentElement;
    if (common && common.closest("#comment-panel, #lightbox")) return;
    var start = doc && doc === docOf(r.endContainer) && blockIn(doc, r.startContainer);
    var quote = sel.toString().replace(/\s+/g, " ").trim().slice(0, 1000);
    if (!start || !quote) {
      // A selection that cannot be commented does not leave an old one behind.
      if (pending) { pending = null; P.update(); highlight(); }
      return;
    }
    var end = blockIn(doc, r.endContainer) || start;
    // A triple click ends at the start of the next block; that block is not selected.
    if (r.endOffset === 0 && end !== start && !end.contains(start)) {
      var before = start;
      doc.querySelectorAll("[data-line]").forEach(function (b) {
        if (b !== end && !b.contains(end) && (b.compareDocumentPosition(end) & Node.DOCUMENT_POSITION_FOLLOWING)) before = b;
      });
      end = before;
    }
    var a = { line: lines(start)[0], end_line: Math.max(lines(start)[0], lines(end)[1]), quote: quote, context: headingPath(doc, start) };
    pending = { label: where(doc.id, a), body: { path: doc.id, anchor: a }, range: r.cloneRange() };
    if (docSelect) docSelect.hidden = true;
    P.update();
    highlight();
  });
  document.addEventListener("keydown", function (e) {
    var lightbox = document.getElementById("lightbox"), t = e.target;
    if (lightbox && lightbox.open) return;
    if (e.key !== "c" || e.ctrlKey || e.metaKey || e.altKey || (t.closest && t.closest("input, textarea, select, [contenteditable]"))) return;
    e.preventDefault();
    P.open(true);
  });
})();
