/* pravo timeline runner: scripted loops from one clock.
   Steps are [ms, fn] pairs. Started when the root is in view, paused when the
   tab is hidden, the pointer is over the root, keyboard focus moves into the
   root (except onto the playback controls), or the root leaves the view.
   seek(beat) renders the end state of a beat instantly. Under reduced motion
   the final state is rendered and nothing starts. No dependencies.

   var tl = pravoTimeline.create(root, {
     steps:  [[0, fn], [3000, fn], ...],   // fn(cx): cx.instant, cx.after(ms, fn), cx.type(el, text, perChar)
     beats:  [{t: 0, label: "Write", say: "One sentence for the live region."}, ...],
     length: 24000,                        // ms; default: last step + 600
     loops:  2,                            // default 1
     reset:  function (cx) {},             // render the state before time 0
     live:   element,                      // aria-live region receiving beat.say
     onbeat: function (i, beat) {}
   });
   tl.controls() returns the pause, replay and beat controls; append it anywhere. */
(function () {
  "use strict";

  var mq = window.matchMedia ? window.matchMedia("(prefers-reduced-motion: reduce)") : { matches: false };

  function create(root, o) {
    var steps = (o.steps || []).map(function (s) { return { t: s[0], fn: s[1] }; })
      .sort(function (a, b) { return a.t - b.t; });
    var beats = o.beats || [];
    var length = o.length || ((steps.length ? steps[steps.length - 1].t : 0) + 600);
    var loops = o.loops || 1;
    var queue = [], clock = 0, cur = 0, last = 0, raf = 0, loop = 0, beat = -1;
    var started = false, inView = false, hover = false, hidden = document.hidden, paused = false, ended = false;
    var dots = [], toggleBtn = null, api = {};

    function cx(instant) {
      return {
        instant: instant,
        after: function (ms, fn) {
          if (typeof ms !== "number") return;
          insert({ t: cur + ms, fn: fn });
        },
        type: function (el, text, per) {
          if (instant || !(per > 0)) { el.textContent = text; return; }
          el.textContent = "";
          for (var k = 1; k <= text.length; k++) (function (n) {
            insert({ t: cur + n * per, fn: function () { el.textContent = text.slice(0, n); } });
          })(k);
        }
      };
    }
    function insert(it) {
      var i = queue.length;
      while (i > 0 && queue[i - 1].t > it.t) i--;
      queue.splice(i, 0, it);
    }
    function load() { queue = steps.slice(); }
    function advance(to, instant) {
      while (queue.length && queue[0].t <= to) {
        var it = queue.shift();
        cur = it.t;
        it.fn(cx(instant));
      }
      cur = to;
    }
    function beatAt(t) {
      var b = -1;
      for (var i = 0; i < beats.length; i++) if (beats[i].t <= t) b = i;
      return b;
    }
    function setBeat(b, announce) {
      if (b === beat) return;
      beat = b;
      dots.forEach(function (d, i) {
        if (i === b) d.setAttribute("aria-current", "step"); else d.removeAttribute("aria-current");
        d.classList.toggle("is-past", i < b);
      });
      if (b >= 0 && announce !== false) {
        if (o.live && beats[b].say) o.live.textContent = beats[b].say;
        if (o.onbeat) o.onbeat(b, beats[b]);
      }
    }
    function state(s) {
      root.setAttribute("data-state", s);
      if (toggleBtn) {
        var playing = s === "playing";
        toggleBtn.setAttribute("aria-label", playing ? "Pause" : "Play");
        toggleBtn.title = playing ? "Pause" : "Play";
      }
    }
    function running() {
      return started && inView && !hover && !hidden && !paused && !ended;
    }
    function frame(now) {
      raf = 0;
      if (!running()) { last = 0; return; }
      var dt = last ? Math.min(now - last, 100) : 0;
      last = now;
      clock += dt;
      advance(clock, false);
      setBeat(beatAt(clock));
      if (clock >= length) {
        loop++;
        if (loop < loops) {
          if (o.reset) o.reset(cx(true));
          load(); clock = 0; cur = 0; beat = -1;
        } else {
          ended = true; state("ended"); return;
        }
      }
      raf = requestAnimationFrame(frame);
    }
    function sync() {
      if (running()) {
        state("playing");
        if (!raf) { last = 0; raf = requestAnimationFrame(frame); }
      } else {
        if (raf) { cancelAnimationFrame(raf); raf = 0; }
        if (!ended) state(started ? "paused" : "idle");
      }
    }
    /* run every step up to t without animation: transitions are switched off
       by [data-seeking] and running animations are jumped to their end */
    function renderTo(t) {
      root.setAttribute("data-seeking", "");
      if (o.reset) o.reset(cx(true));
      load(); cur = 0; beat = -1;
      advance(t, true);
      clock = t;
      void root.offsetWidth;
      if (root.getAnimations) root.getAnimations({ subtree: true }).forEach(function (a) {
        try { if (a.effect.getComputedTiming().iterations !== Infinity) a.finish(); } catch (e) { /* not finishable */ }
      });
      requestAnimationFrame(function () { root.removeAttribute("data-seeking"); });
    }
    function beatEnd(i) { return i + 1 < beats.length ? beats[i + 1].t - 1 : length; }

    api.seek = function (i) {
      if (i < 0) i = 0;
      if (i >= beats.length) i = beats.length - 1;
      ended = false; started = true; paused = true;
      renderTo(beatEnd(i));
      setBeat(-2, false);
      setBeat(i);
      state("paused");
      if (raf) { cancelAnimationFrame(raf); raf = 0; }
    };
    api.pause = function () { paused = true; sync(); };
    api.play = function () {
      if (mq.matches) { api.finish(); return; }
      if (ended) { api.replay(); return; }
      paused = false; started = true; sync();
    };
    api.replay = function () {
      if (mq.matches) { api.finish(); return; }
      ended = false; paused = false; started = true; loop = 0;
      root.setAttribute("data-seeking", "");
      if (o.reset) o.reset(cx(true));
      void root.offsetWidth;
      requestAnimationFrame(function () { root.removeAttribute("data-seeking"); });
      load(); clock = 0; cur = 0;
      setBeat(-2, false); beat = -1;
      sync();
    };
    api.finish = function () {
      ended = true; paused = true;
      renderTo(length + 1);
      setBeat(beats.length - 1);
      state("ended");
    };
    api.destroy = function () { if (raf) cancelAnimationFrame(raf); raf = 0; paused = true; };

    api.controls = function () {
      var el = document.createElement("div");
      el.className = "tl-controls";
      el.setAttribute("role", "group");
      el.setAttribute("aria-label", "Playback");
      function btn(act, label) {
        var b = document.createElement("button");
        b.type = "button"; b.className = "tl-btn"; b.setAttribute("data-act", act);
        b.setAttribute("aria-label", label); b.title = label;
        el.appendChild(b);
        return b;
      }
      toggleBtn = btn("toggle", "Pause");
      toggleBtn.addEventListener("click", function () {
        if (root.getAttribute("data-state") === "playing") api.pause(); else api.play();
      });
      btn("replay", "Replay").addEventListener("click", api.replay);
      if (beats.length) {
        var wrap = document.createElement("div");
        wrap.className = "beats";
        wrap.setAttribute("role", "group");
        wrap.setAttribute("aria-label", "Jump to a step");
        beats.forEach(function (b, i) {
          var d = document.createElement("button");
          d.type = "button"; d.className = "beat";
          d.setAttribute("aria-label", (i + 1) + ". " + (b.label || "Step " + (i + 1)));
          d.title = b.label || "Step " + (i + 1);
          d.addEventListener("click", function () { api.seek(i); });
          dots.push(d); wrap.appendChild(d);
        });
        el.appendChild(wrap);
      }
      if (beat >= 0) { var b = beat; beat = -2; setBeat(b, false); }
      state(root.getAttribute("data-state") || "idle");
      return el;
    };

    /* start */
    if (o.reset) o.reset(cx(true));
    load();
    state("idle");
    document.addEventListener("visibilitychange", function () { hidden = document.hidden; sync(); });
    if (o.hover !== false) {
      root.addEventListener("pointerenter", function (e) { if (e.pointerType === "mouse") { hover = true; sync(); } });
      root.addEventListener("pointerleave", function () { hover = false; sync(); });
      /* a keyboard user who moves into the stage is reading it: stop the loop, and let Play start it again */
      root.addEventListener("focusin", function (e) {
        if (e.target.closest && e.target.closest(".tl-controls")) return;
        if (!paused && !ended) { paused = true; sync(); }
      });
    }
    if (mq.matches) {
      api.finish();
    } else if ("IntersectionObserver" in window) {
      new IntersectionObserver(function (es) {
        es.forEach(function (e) {
          inView = e.isIntersecting;
          if (inView) started = true;
          sync();
        });
      }, { threshold: o.threshold || 0.4 }).observe(root);
    } else {
      inView = started = true; sync();
    }
    return api;
  }

  window.pravoTimeline = { create: create, reduced: function () { return mq.matches; } };
})();
