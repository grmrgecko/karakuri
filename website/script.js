// Mascot design behavior: theme control, eyes that follow the cursor, blinking,
// the wind-up demo run, bounce-in reveals, install tabs, copy buttons, and the clock tile.
(function () {
  "use strict";

  var root = document.documentElement;
  var reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  var THEME_KEY = "karakuri-theme-kawaii";

  // Theme control: Auto, Light, Dark.
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

  // Eyes follow the pointer within a small radius.
  var mascot = document.querySelector("[data-mascot]");
  var eyes = mascot ? Array.prototype.slice.call(mascot.querySelectorAll("[data-eye]")) : [];
  var pointer = null;
  var eyeFrame = false;
  var MAX_LOOK = 1.1;

  function lookAt() {
    eyeFrame = false;
    if (!pointer) return;
    eyes.forEach(function (eye) {
      var r = eye.getBoundingClientRect();
      var dx = pointer.x - (r.left + r.width / 2);
      var dy = pointer.y - (r.top + r.height / 2);
      var dist = Math.sqrt(dx * dx + dy * dy) || 1;
      var reach = Math.min(1, dist / 220);
      var x = (dx / dist) * MAX_LOOK * reach;
      var y = (dy / dist) * MAX_LOOK * reach * 0.8;
      eye.setAttribute("transform", "translate(" + x.toFixed(2) + " " + y.toFixed(2) + ")");
    });
  }

  if (eyes.length) {
    window.addEventListener("pointermove", function (e) {
      pointer = { x: e.clientX, y: e.clientY };
      if (!eyeFrame) {
        eyeFrame = true;
        window.requestAnimationFrame(lookAt);
      }
    }, { passive: true });
  }

  // Blink at irregular intervals, sometimes twice.
  function blinkOnce(done) {
    mascot.classList.add("is-blink");
    setTimeout(function () {
      mascot.classList.remove("is-blink");
      if (done) setTimeout(done, 140);
    }, 130);
  }

  function scheduleBlink() {
    var wait = 2200 + Math.random() * 3800;
    setTimeout(function () {
      if (Math.random() < 0.25) blinkOnce(function () { blinkOnce(); });
      else blinkOnce();
      scheduleBlink();
    }, wait);
  }
  if (mascot) scheduleBlink();

  // Demo run. Odd runs recover on the third attempt; even runs exhaust the
  // retries and take the error branch.
  var card = document.querySelector("[data-flowcard]");
  var bubble = document.querySelector("[data-bubble]");
  var log = card && card.querySelector("[data-fc-log]");
  var badge = card && card.querySelector("[data-fc-badge]");
  var windButtons = document.querySelectorAll("[data-wind]");
  var narrow = window.matchMedia("(max-width: 639px)");
  var runCount = 0;
  var running = false;

  function node(name) { return card.querySelector("[data-node='" + name + "']"); }
  function setState(name, cls, label) {
    var n = node(name);
    n.classList.remove("is-active", "is-failed", "is-ok", "is-dim");
    if (cls) n.classList.add(cls);
    var s = n.querySelector("[data-state]");
    if (s && label) s.textContent = label;
  }
  function setTry(n, cls) {
    var dot = card.querySelector("[data-try='" + n + "']");
    dot.classList.remove("is-run", "is-fail", "is-ok");
    if (cls) dot.classList.add(cls);
  }
  function say(text) {
    if (!bubble) return;
    bubble.textContent = text;
    bubble.classList.remove("is-new");
    void bubble.offsetWidth;
    bubble.classList.add("is-new");
  }
  function note(text) { if (log) log.textContent = text; }
  function mood(cls, ms) {
    mascot.classList.add(cls);
    if (ms) setTimeout(function () { mascot.classList.remove(cls); }, ms);
  }

  function resetCard() {
    ["trigger", "light", "ok", "err"].forEach(function (n) { setState(n, null, "Idle"); });
    [1, 2, 3].forEach(function (n) { setTry(n, null); });
    mascot.classList.remove("is-happy", "is-running", "is-hop", "is-shake", "is-winding");
  }

  function wind() {
    if (running || !card || !mascot) return;
    running = true;
    runCount += 1;
    var recover = runCount % 2 === 1;
    var fast = reduceMotion.matches;
    var t = 0;
    var steps = [];
    function at(delay, fn) { t += delay; steps.push([t, fn]); }

    resetCard();
    windButtons.forEach(function (b) { b.setAttribute("aria-disabled", "true"); });

    // On phones, bring the mascot and the card into view together.
    if (narrow.matches) {
      var stage = mascot.closest(".stage-mascot");
      if (stage) stage.scrollIntoView({ block: "start", behavior: fast ? "auto" : "smooth" });
    }
    if (badge) badge.textContent = "Running";

    at(0, function () { mood("is-winding", 1400); say("Winding up. Three turns of the key."); note("Winding the key."); });
    at(1400, function () { mascot.classList.add("is-running"); setState("trigger", "is-active", "Fired"); say("The front door opened. Zigbee2MQTT says so."); note("Trigger fired: front door contact opened."); });
    at(1000, function () { setState("trigger", "is-ok", "Done"); setState("light", "is-active", "Try 1"); setTry(1, "is-run"); say("Asking Home Assistant to turn on the hall light."); note("Attempt 1 of 3."); });
    at(1000, function () { setTry(1, "is-fail"); setState("light", "is-failed", "Wait 1 s"); mood("is-shake", 450); say("No answer. I will wait 1 s and try again."); note("Attempt 1 failed. Retrying in 1 s."); });
    at(1300, function () { setTry(2, "is-run"); setState("light", "is-active", "Try 2"); note("Attempt 2 of 3."); });
    at(900, function () { setTry(2, "is-fail"); setState("light", "is-failed", "Wait 2 s"); mood("is-shake", 450); say("Still nothing. The wait doubles to 2 s."); note("Attempt 2 failed. Retrying in 2 s, backoff ×2."); });
    at(1800, function () { setTry(3, "is-run"); setState("light", "is-active", "Try 3"); say("Last attempt."); note("Attempt 3 of 3."); });

    if (recover) {
      at(900, function () {
        setTry(3, "is-ok"); setState("light", "is-ok", "On");
        setState("ok", "is-ok"); setState("err", "is-dim");
        say("The light answered on the third try. The run ends on the ok branch.");
        note("Attempt 3 succeeded. Run finished on the ok branch.");
      });
    } else {
      at(900, function () {
        setTry(3, "is-fail"); setState("light", "is-failed", "Failed");
        mood("is-shake", 450);
        say("Three tries, no light. The message goes out the error branch.");
        note("Attempts exhausted. Message routed to the error branch.");
      });
      at(1000, function () {
        setState("err", "is-ok"); setState("ok", "is-dim");
        say("Your phone got a notification, and the run still finished.");
        note("Notification sent. Run finished on the error branch.");
      });
    }

    at(700, function () {
      mascot.classList.remove("is-running");
      mascot.classList.add("is-happy");
      mood("is-hop", 1300);
      if (badge) badge.textContent = "Deployed";
      say(recover ? "Wind me again and I will show you the error branch." : "Wind me again and the light gets another chance.");
    });
    at(2200, function () {
      mascot.classList.remove("is-happy");
      running = false;
      windButtons.forEach(function (b) { b.removeAttribute("aria-disabled"); });
    });

    // Reduced motion keeps the narration but shortens the waits.
    steps.forEach(function (s) { setTimeout(s[1], fast ? s[0] * 0.6 : s[0]); });
  }

  windButtons.forEach(function (b) { b.addEventListener("click", wind); });

  // Sections bounce in as they enter the viewport.
  var reveals = document.querySelectorAll(".reveal");
  if ("IntersectionObserver" in window && !reduceMotion.matches) {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          entry.target.classList.add("is-in");
          io.unobserve(entry.target);
        }
      });
    }, { threshold: 0.15, rootMargin: "0px 0px -40px 0px" });
    reveals.forEach(function (el) { io.observe(el); });
  } else {
    reveals.forEach(function (el) { el.classList.add("is-in"); });
  }

  // Install tabs. Without JS both panels stay visible.
  var tabList = document.querySelector("[data-tabs]");
  if (tabList) {
    var tabs = Array.prototype.slice.call(tabList.querySelectorAll("[role='tab']"));
    tabList.hidden = false;
    tabList.parentElement.classList.add("js-tabs");

    var select = function (tab, focus) {
      tabs.forEach(function (t) {
        var on = t === tab;
        t.setAttribute("aria-selected", on ? "true" : "false");
        t.tabIndex = on ? 0 : -1;
        document.getElementById(t.getAttribute("aria-controls")).hidden = !on;
      });
      if (focus) tab.focus();
    };

    tabs.forEach(function (tab, i) {
      tab.addEventListener("click", function () { select(tab, false); });
      tab.addEventListener("keydown", function (e) {
        if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
          e.preventDefault();
          var next = tabs[(i + (e.key === "ArrowRight" ? 1 : tabs.length - 1)) % tabs.length];
          select(next, true);
        }
      });
    });
    select(tabs[0], false);
  }

  // Copy buttons.
  document.querySelectorAll("[data-copy]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var src = document.getElementById(btn.getAttribute("data-copy"));
      if (!src) return;
      var text = src.textContent;
      var done = function () {
        btn.textContent = "Copied";
        btn.classList.add("is-copied");
        setTimeout(function () { btn.textContent = "Copy"; btn.classList.remove("is-copied"); }, 1600);
      };
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(done, function () { fallbackCopy(text); done(); });
      } else {
        fallbackCopy(text);
        done();
      }
    });
  });

  function fallbackCopy(text) {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); } catch (e) {}
    ta.remove();
  }

  // Flow editor illustration: edges are drawn between the port dots so they stay
  // attached in both layouts, and a run plays while the canvas is on screen.
  var editor = document.querySelector("[data-editor]");
  if (editor) {
    var edInner = editor.querySelector(".ed-inner");
    var edSvg = editor.querySelector(".ed-edges");
    var SVGNS = "http://www.w3.org/2000/svg";
    var edgeSpec = [["mqtt.out", "tw.in"], ["tw.inside", "ha.in"], ["ha.error", "notify.in"]];
    var edgeEls = [];

    // Positions are read from layout offsets inside the canvas, not from the
    // screen, so a card still moving through its reveal animation does not
    // pull the edges off its ports.
    var offsetIn = function (el) {
      var x = 0, y = 0;
      var node = el;
      while (node && node !== edInner) {
        x += node.offsetLeft;
        y += node.offsetTop;
        var parent = node.offsetParent;
        if (parent && parent !== edInner) {
          x += parent.clientLeft;
          y += parent.clientTop;
        }
        node = parent;
      }
      return { x: x, y: y };
    };
    var portPoint = function (id) {
      var dot = edInner.querySelector('[data-port="' + id + '"]');
      var at = offsetIn(dot);
      return { x: at.x + dot.offsetWidth / 2, y: at.y + dot.offsetHeight / 2 };
    };
    var nodeBox = function (id) {
      var n = edInner.querySelector('[data-port="' + id + '"]').closest(".en");
      return { top: n.offsetTop, bottom: n.offsetTop + n.offsetHeight };
    };

    var drawEdges = function () {
      // Size the canvas to the lowest node, so either layout fits.
      var lowest = 0;
      edInner.querySelectorAll(".en").forEach(function (n) {
        lowest = Math.max(lowest, n.offsetTop + n.offsetHeight);
      });
      editor.style.height = lowest + 36 + "px";
      edgeSpec.forEach(function (spec, i) {
        var a = portPoint(spec[0]);
        var b = portPoint(spec[1]);
        var d;
        if (b.x - a.x > 16) {
          // Forward edge: the bezier Svelte Flow draws between side ports.
          var dx = Math.max(24, (b.x - a.x) / 2);
          d = "M" + a.x + " " + a.y + "C" + (a.x + dx) + " " + a.y + " " + (b.x - dx) + " " + b.y + " " + b.x + " " + b.y;
        } else {
          // Backward edge: step out, run through the gap between the rows, step in.
          var mid = (nodeBox(spec[0]).bottom + nodeBox(spec[1]).top) / 2;
          var r = 8, ox = a.x + 14, ix = b.x - 14;
          d = "M" + a.x + " " + a.y + "H" + (ox - r) + "Q" + ox + " " + a.y + " " + ox + " " + (a.y + r) +
            "V" + (mid - r) + "Q" + ox + " " + mid + " " + (ox - r) + " " + mid +
            "H" + (ix + r) + "Q" + ix + " " + mid + " " + ix + " " + (mid + r) +
            "V" + (b.y - r) + "Q" + ix + " " + b.y + " " + (ix + r) + " " + b.y + "H" + b.x;
        }
        if (!edgeEls[i]) {
          edgeEls[i] = document.createElementNS(SVGNS, "path");
          edSvg.appendChild(edgeEls[i]);
        }
        edgeEls[i].setAttribute("d", d);
      });
    };

    drawEdges();
    window.addEventListener("resize", drawEdges);
    if (document.fonts && document.fonts.ready) document.fonts.ready.then(drawEdges);
    if ("ResizeObserver" in window) new ResizeObserver(drawEdges).observe(editor);

    var enNode = function (id) { return edInner.querySelector('[data-en="' + id + '"]'); };
    var chip = editor.querySelector("[data-en-chip]");
    var setNode = function (id, state) {
      var n = enNode(id);
      n.classList.remove("is-run", "is-retry", "is-ok", "is-fail");
      if (state) n.classList.add("is-" + state);
    };
    var setChip = function (text) {
      chip.hidden = !text;
      chip.textContent = text || "";
    };
    var setEdge = function (i, cls) {
      edgeEls[i].classList.remove("is-active", "is-err");
      if (cls) cls.split(" ").forEach(function (c) { edgeEls[i].classList.add(c); });
    };
    var finalState = function () {
      ["mqtt", "tw", "notify"].forEach(function (id) { setNode(id, "ok"); });
      setNode("ha", "fail");
      setChip("3/3");
      setEdge(0, ""); setEdge(1, ""); setEdge(2, "is-err");
    };

    if (reduceMotion.matches) {
      finalState();
    } else {
      // One run: the service call fails three times and the Error output takes over.
      var edSteps = [
        [0, function () { ["mqtt", "tw", "ha", "notify"].forEach(function (id) { setNode(id, ""); }); setChip(""); [0, 1, 2].forEach(function (i) { setEdge(i, ""); }); }],
        [400, function () { setNode("mqtt", "run"); }],
        [1000, function () { setNode("mqtt", "ok"); setEdge(0, "is-active"); }],
        [1600, function () { setEdge(0, ""); setNode("tw", "run"); }],
        [2200, function () { setNode("tw", "ok"); setEdge(1, "is-active"); }],
        [2800, function () { setEdge(1, ""); setNode("ha", "run"); setChip("1/3"); }],
        [3600, function () { setNode("ha", "retry"); }],
        [4600, function () { setNode("ha", "run"); setChip("2/3"); }],
        [5400, function () { setNode("ha", "retry"); }],
        [7400, function () { setNode("ha", "run"); setChip("3/3"); }],
        [8200, function () { setNode("ha", "fail"); setEdge(2, "is-active is-err"); }],
        [8900, function () { setEdge(2, "is-err"); setNode("notify", "run"); }],
        [9500, function () { setNode("notify", "ok"); }]
      ];
      var edTimers = [];
      var edLoop = null;
      var playEditor = function () {
        edTimers.forEach(clearTimeout);
        edTimers = edSteps.map(function (s) { return setTimeout(s[1], s[0]); });
      };
      var startEditor = function () {
        if (edLoop) return;
        playEditor();
        edLoop = setInterval(playEditor, 13000);
      };
      var stopEditor = function () {
        clearInterval(edLoop);
        edLoop = null;
        edTimers.forEach(clearTimeout);
        finalState();
      };
      finalState();
      if ("IntersectionObserver" in window) {
        new IntersectionObserver(function (entries) {
          entries.forEach(function (e) { if (e.isIntersecting) startEditor(); else stopEditor(); });
        }, { threshold: 0.4 }).observe(editor);
      } else {
        startEditor();
      }
    }
  }

  // Clock tile shows the visitor's local time.
  var clock = document.querySelector("[data-clock]");
  function tick() {
    if (!clock) return;
    var d = new Date();
    clock.textContent = String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
  }
  tick();
  setInterval(tick, 15000);
})();
