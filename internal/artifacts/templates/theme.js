(function () {
  var button = document.getElementById("theme");
  if (!button) return;
  var order = ["auto", "light", "dark"];
  var labels = { auto: "◐ Auto", light: "☀ Light", dark: "☾ Dark" };
  function current() {
    try { return localStorage.getItem("devstation-theme") || "auto"; } catch (e) { return "auto"; }
  }
  function show(theme) {
    button.textContent = labels[theme];
    button.setAttribute("aria-label", "Color theme: " + theme + " (click to change)");
  }
  show(current());
  button.addEventListener("click", function () {
    var next = order[(order.indexOf(current()) + 1) % order.length];
    try {
      if (next === "auto") localStorage.removeItem("devstation-theme");
      else localStorage.setItem("devstation-theme", next);
    } catch (e) { /* storage blocked: still switch for this page */ }
    document.documentElement.classList.remove("light", "dark");
    if (next !== "auto") document.documentElement.classList.add(next);
    show(next);
  });
})();
