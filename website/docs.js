// Documentation page behavior: the theme control, showing one guide at a time
// by the address's hash, and copy buttons on code blocks. Without JavaScript
// every guide shows one after another and the sidebar links jump to them.
(function () {
  "use strict";

  var root = document.documentElement;
  var THEME_KEY = "karakuri-theme-kawaii";

  // Theme control: Auto, Light, Dark, shared with the main page.
  var themeButtons = document.querySelectorAll("[data-theme-choice]");
  function currentTheme() {
    var t = root.getAttribute("data-theme");
    return t === "light" || t === "dark" ? t : "auto";
  }
  function renderTheme() {
    var t = currentTheme();
    themeButtons.forEach(function (b) {
      b.setAttribute("aria-pressed", b.getAttribute("data-theme-choice") === t ? "true" : "false");
    });
  }
  themeButtons.forEach(function (b) {
    b.addEventListener("click", function () {
      var t = b.getAttribute("data-theme-choice");
      if (t === "auto") root.removeAttribute("data-theme");
      else root.setAttribute("data-theme", t);
      try {
        if (t === "auto") localStorage.removeItem(THEME_KEY);
        else localStorage.setItem(THEME_KEY, t);
      } catch (e) {}
      renderTheme();
    });
  });
  renderTheme();

  // Routing: "#" is the index, "#<guide>" a guide from its top, and
  // "#<guide>--<section>" a guide scrolled to a heading.
  var index = document.querySelector("[data-index]");
  var articles = Array.prototype.slice.call(document.querySelectorAll("[data-guide]"));
  var links = Array.prototype.slice.call(document.querySelectorAll("[data-guide-link]"));
  var sectionNavs = Array.prototype.slice.call(document.querySelectorAll("[data-sections]"));
  var titles = {};
  links.forEach(function (a) {
    if (a.getAttribute("data-guide-link")) titles[a.getAttribute("data-guide-link")] = a.textContent;
  });

  function route() {
    var hash = decodeURIComponent(location.hash.replace(/^#/, ""));
    var guide = hash.split("--")[0];
    if (!titles[guide]) guide = "";

    index.hidden = guide !== "";
    articles.forEach(function (a) { a.hidden = a.getAttribute("data-guide") !== guide; });
    links.forEach(function (a) {
      var on = a.getAttribute("data-guide-link") === guide;
      a.classList.toggle("active", on);
      if (on) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    });
    sectionNavs.forEach(function (n) {
      n.hidden = n.getAttribute("data-sections") !== guide;
      Array.prototype.forEach.call(n.querySelectorAll("a"), function (a) {
        a.classList.toggle("current", a.getAttribute("href") === "#" + hash);
      });
    });
    document.title = guide ? titles[guide] + ": Karakuri documentation" : "Karakuri documentation";

    // The browser jumps to the element a hash names on its own, after this
    // runs; scrolling a frame later puts a guide's top, not its article
    // anchor, at the top of the page, with the guide list above it.
    var target = hash && hash !== guide ? document.getElementById(hash) : null;
    requestAnimationFrame(function () {
      if (target) target.scrollIntoView({ block: "start" });
      else window.scrollTo(0, 0);
    });
  }
  // On a phone the guide list folds away above the page, and closes again
  // once a guide is picked from it.
  var menu = document.querySelector("[data-menu]");
  var narrow = window.matchMedia("(max-width: 899px)");
  function fitMenu() {
    if (menu) menu.open = !narrow.matches;
  }
  if (narrow.addEventListener) narrow.addEventListener("change", fitMenu);
  fitMenu();

  root.classList.add("docs-routed");
  window.addEventListener("hashchange", function () {
    route();
    if (menu && narrow.matches) menu.open = false;
  });
  route();
  window.addEventListener("load", route);

  // Copy buttons on code blocks.
  Array.prototype.forEach.call(document.querySelectorAll(".docs-body pre"), function (pre) {
    var wrap = document.createElement("div");
    wrap.className = "docs-code";
    pre.parentNode.insertBefore(wrap, pre);
    wrap.appendChild(pre);
    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = "copy";
    btn.textContent = "Copy";
    btn.addEventListener("click", function () {
      var text = pre.innerText.replace(/\n$/, "");
      var done = function () {
        btn.textContent = "Copied";
        btn.classList.add("is-copied");
        setTimeout(function () {
          btn.textContent = "Copy";
          btn.classList.remove("is-copied");
        }, 1500);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(done, function () {});
      }
    });
    wrap.appendChild(btn);
  });
})();
