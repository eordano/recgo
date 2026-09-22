// Injected into the page under test via Page.addScriptToEvaluateOnNewDocument.
// Reports clicks back over a Runtime binding named __rtEmit.
//
// Why in-page rather than an OS-level input hook: the page knows *what* was
// clicked. An evdev or CGEventTap hook only knows a button went down at
// (840, 291). It also means no input permissions are needed on either platform,
// which on Wayland would otherwise require /dev/input access.
//
// Runs before any page script, so it cannot depend on the app's globals.

(() => {
  if (window.__rtInstalled) return;
  window.__rtInstalled = true;

  // The script is installed in every frame of the page, and the binding is
  // context-less, so an iframe reports through the same channel as the top
  // document. Its location is not where the user is: the recorder keeps a
  // frame's clicks but takes navigation and visibility from the top frame.
  let isTop = true;
  try {
    isTop = window === window.top;
  } catch {
    isTop = false;
  }

  const emit = (payload) => {
    try {
      // The binding is installed by Runtime.addBinding; if injection races
      // ahead of it, drop the event rather than throwing inside the page.
      if (typeof window.__rtEmit === 'function') {
        window.__rtEmit(JSON.stringify({ top: isTop, ...payload }));
      }
    } catch {
      /* never let instrumentation break the app under test */
    }
  };

  // A selector stable enough to hand an LLM. Preference order matches what a
  // developer would actually grep for in the codebase: a test id or id on the
  // element itself, else a short path of named ancestors. Classes name the
  // thing; nth-of-type only where the classes do not tell siblings apart;
  // anonymous wrappers (no id, no meaningful class, no role) are skipped, and
  // the walk stops at the first id or landmark, since a region already
  // bounds where the control is.
  const STATE_CLASSES = new Set([
    'active', 'hover', 'focus', 'focused', 'visible', 'hidden', 'present', 'past',
    'future', 'fragment', 'current', 'selected', 'open', 'closed', 'disabled',
    'enabled', 'checked', 'expanded', 'collapsed', 'loading', 'loaded', 'stack',
    'flex', 'grid', 'block', 'inline', 'relative', 'absolute', 'fixed', 'sticky',
    'container', 'wrapper', 'inner', 'outer',
  ]);
  const meaningfulClasses = (el) => {
    const raw = typeof el.className === 'string' ? el.className : '';
    const out = [];
    for (const c of raw.trim().split(/\s+/)) {
      if (!c || c.length < 3) continue;
      if (/[\d:\/\[\]!]/.test(c)) continue; // hashed (css-1x2y), utilities (mt-4, md:flex, w-1/2)
      if (/^(is|has|js)-/.test(c) || STATE_CLASSES.has(c)) continue;
      out.push(c);
      if (out.length === 2) break;
    }
    return out;
  };
  const LANDMARKS = new Set(['main', 'nav', 'form', 'dialog', 'section', 'article',
    'aside', 'header', 'footer', 'table']);
  const attrPart = (el) => {
    for (const attr of ['role', 'aria-label']) {
      const v = el.getAttribute?.(attr);
      if (v) return `[${attr}="${v.replace(/["\\]/g, '\\$&')}"]`;
    }
    return '';
  };
  const selectorFor = (el) => {
    if (!(el instanceof Element)) return null;

    for (const attr of ['data-testid', 'data-test-id', 'data-test', 'data-cy']) {
      const v = el.getAttribute?.(attr);
      if (v) return `[${attr}="${CSS.escape(v)}"]`;
    }
    if (el.id) return `#${CSS.escape(el.id)}`;

    const parts = [];
    let skipped = false;
    // A skipped wrapper between two named parts turns the child combinator
    // into a descendant one, so the path stays a valid selector.
    const emit = (part) => {
      parts.unshift(parts.length ? part + (skipped ? ' ' : ' > ') : part);
      skipped = false;
    };
    let node = el;
    while (node && node.nodeType === 1 && parts.length < 4) {
      if (node.id) {
        emit(`#${CSS.escape(node.id)}`);
        break;
      }
      const tag = node.tagName.toLowerCase();
      const classes = meaningfulClasses(node);
      const attr = attrPart(node);
      const landmark = LANDMARKS.has(tag) || attr !== '';
      if (node !== el && !classes.length && !landmark) {
        skipped = true;
        node = node.parentElement;
        continue;
      }
      let part = tag + classes.map((c) => '.' + CSS.escape(c)).join('') + attr;
      const parent = node.parentElement;
      if (parent) {
        const twins = [...parent.children].filter(
          (c) => c !== node && c.tagName === node.tagName && node.matches(part) && c.matches(part),
        );
        if (twins.length) {
          const same = [...parent.children].filter((c) => c.tagName === node.tagName);
          part += `:nth-of-type(${same.indexOf(node) + 1})`;
        }
      }
      emit(part);
      if (landmark || !parent) break;
      node = parent;
    }
    return parts.join('');
  };

  const describe = (el) => {
    if (!(el instanceof Element)) return null;
    const rect = el.getBoundingClientRect();
    const classes = typeof el.className === 'string' ? el.className.trim() : '';
    return {
      selector: selectorFor(el),
      tag: el.tagName.toLowerCase(),
      id: el.id || null,
      classes: classes || null,
      testid: el.getAttribute('data-testid') ?? null,
      role: el.getAttribute('role') ?? null,
      ariaLabel: el.getAttribute('aria-label') ?? null,
      name: el.getAttribute('name') ?? null,
      type: el.getAttribute('type') ?? null,
      href: el.getAttribute('href') ?? null,
      // Own visible text, not textContent: the subtree text of a container
      // is the entire page, which told a reader nothing 13 identical times.
      text: ownText(el),
      rect: { x: rect.x, y: rect.y, w: rect.width, h: rect.height },
    };
  };

  const ownText = (el) => {
    const t = (el.innerText ?? el.textContent ?? '').trim().replace(/\s+/g, ' ');
    if (t && t.length <= 80) return t;
    const label = el.getAttribute?.('aria-label');
    return label || null;
  };

  // An "interactive ancestor" that spans most of the viewport is a
  // delegation root (SPA containers carry role/tabindex/onclick too), not
  // the control the user clicked.
  const isControl = (n) => {
    if (!(n instanceof Element)) return false;
    if (
      !n.matches(
        'a,button,input,select,textarea,summary,[onclick],' +
          '[role="button"],[role="link"],[role="tab"],[role="menuitem"],' +
          '[role="checkbox"],[role="radio"],[role="option"],[role="switch"],' +
          '[tabindex]:not([tabindex="-1"])',
      )
    ) {
      return false;
    }
    const r = n.getBoundingClientRect();
    const area = r.width * r.height;
    const viewport = innerWidth * innerHeight || 1;
    return area / viewport < 0.5;
  };

  // pointerdown is the closest event to the physical press; `click` is the
  // semantic one. Timestamp from pointerdown and report on click, so the -100ms
  // screenshot is measured from when the button actually went down.
  let lastDown = null;
  addEventListener(
    'pointerdown',
    (e) => {
      lastDown = { t: e.timeStamp, x: e.clientX, y: e.clientY };
    },
    { capture: true, passive: true },
  );

  addEventListener(
    'click',
    (e) => {
      const downT = lastDown && e.timeStamp - lastDown.t < 2000 ? lastDown.t : e.timeStamp;
      const path = typeof e.composedPath === 'function' ? e.composedPath() : [];

      const target = describe(e.target);
      // The nearest interactive ancestor is usually what the developer means
      // by "the button", even when the click landed on a span inside it.
      const interactive = describe(path.find(isControl) ?? null);
      if (interactive && !interactive.text && target?.text) {
        // The ancestor's identity with the clicked node's words: "clicked
        // the span 'V1' inside that button" is what a reader needs.
        interactive.text = target.text;
      }

      emit({
        kind: 'click',
        timeOrigin: performance.timeOrigin,
        pageTime: downT,
        clickPageTime: e.timeStamp,
        button: e.button,
        x: e.clientX,
        y: e.clientY,
        url: location.href,
        title: document.title,
        target,
        interactive,
      });
    },
    { capture: true, passive: true },
  );

  // reveal.js and most SPA routers write the URL with history.pushState /
  // replaceState, which fire neither popstate nor hashchange, so the History
  // methods are wrapped too. The report is deferred a tick so a title the app
  // sets right after the URL is on the same line; the recorder drops reports
  // whose URL did not change, so the overlapping sources cost nothing.
  const reportNavigation = () => {
    const pageTime = performance.now();
    setTimeout(() => {
      emit({
        kind: 'navigation',
        timeOrigin: performance.timeOrigin,
        pageTime,
        url: location.href,
        title: document.title,
      });
    }, 0);
  };
  for (const method of ['pushState', 'replaceState']) {
    const original = History.prototype[method];
    if (typeof original !== 'function') continue;
    History.prototype[method] = function (...args) {
      const result = original.apply(this, args);
      reportNavigation();
      return result;
    };
  }
  addEventListener('popstate', reportNavigation);
  addEventListener('hashchange', reportNavigation);

  // A fresh document reports itself before its <title> is parsed, and apps
  // set the title after the URL: report again once the head exists and
  // whenever the title changes. The recorder attaches the first title it sees
  // to the navigation it belongs to.
  const watchTitle = () => {
    reportNavigation();
    if (!document.head || typeof MutationObserver !== 'function') return;
    let last = document.title;
    new MutationObserver(() => {
      if (document.title !== last) {
        last = document.title;
        reportNavigation();
      }
    }).observe(document.head, { childList: true, subtree: true, characterData: true });
  };
  if (document.readyState === 'loading') {
    addEventListener('DOMContentLoaded', watchTitle);
  } else {
    watchTitle();
  }

  // Which tab the user is looking at. CDP's /json/list cannot answer this, but
  // the page can: only the foreground tab of a focused window is "visible". This
  // is how recgo-browser follows along without polling every target.
  const reportVisibility = () => {
    emit({
      kind: 'visibility',
      timeOrigin: performance.timeOrigin,
      pageTime: performance.now(),
      visible: document.visibilityState === 'visible',
      focused: typeof document.hasFocus === 'function' ? document.hasFocus() : null,
      url: location.href,
      title: document.title,
    });
  };
  addEventListener('visibilitychange', reportVisibility);
  addEventListener('focus', reportVisibility);
  addEventListener('blur', reportVisibility);
  // Announce the current state on attach, so the follower knows where the user
  // already is rather than waiting for them to switch away and back.
  reportVisibility();

  // Calibration tone. walk-and-talk's one advantage over a plain recorder is a
  // browser clock already calibrated to sub-millisecond, so the audio anchor can
  // be *measured* rather than assumed: emit a burst at a known page timestamp,
  // find it in the captured audio, and the offset between them is ffmpeg's true
  // capture latency. Returns the exact page time the tone was scheduled for.
  window.__rtTone = (freq = 1000, durMs = 120) => {
    const ctx = new (window.AudioContext || window.webkitAudioContext)();
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    osc.frequency.value = freq;
    // Hard edges make the onset easy to locate; a ramp would smear it.
    gain.gain.value = 0.25;
    osc.connect(gain).connect(ctx.destination);

    const startAt = ctx.currentTime + 0.05;
    osc.start(startAt);
    osc.stop(startAt + durMs / 1000);

    // AudioContext.currentTime and performance.now() share timeOrigin via
    // getOutputTimestamp when available; fall back to now + the lead-in.
    // A context that has not rendered audio yet reports zeros, and trusting
    // them anchors the tone at page load -- minutes off on a long-open tab.
    let pageTime = performance.now() + 50;
    if (typeof ctx.getOutputTimestamp === 'function') {
      const ts = ctx.getOutputTimestamp();
      if (ts && ts.contextTime > 0 && ts.performanceTime > 0) {
        pageTime = ts.performanceTime + (startAt - ts.contextTime) * 1000;
      }
    }
    return { timeOrigin: performance.timeOrigin, pageTime, freq, durMs };
  };
})();
