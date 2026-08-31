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

  const emit = (payload) => {
    try {
      // The binding is installed by Runtime.addBinding; if injection races
      // ahead of it, drop the event rather than throwing inside the page.
      if (typeof window.__rtEmit === 'function') {
        window.__rtEmit(JSON.stringify(payload));
      }
    } catch {
      /* never let instrumentation break the app under test */
    }
  };

  // A selector stable enough to hand an LLM. Preference order matches what a
  // developer would actually grep for in the codebase.
  const selectorFor = (el) => {
    if (!(el instanceof Element)) return null;

    for (const attr of ['data-testid', 'data-test-id', 'data-test', 'data-cy']) {
      const v = el.getAttribute?.(attr);
      if (v) return `[${attr}="${CSS.escape(v)}"]`;
    }
    if (el.id) return `#${CSS.escape(el.id)}`;

    const parts = [];
    let node = el;
    while (node && node.nodeType === 1 && parts.length < 6) {
      if (node.id) {
        parts.unshift(`#${CSS.escape(node.id)}`);
        break;
      }
      const tag = node.tagName.toLowerCase();
      const parent = node.parentElement;
      if (!parent) {
        parts.unshift(tag);
        break;
      }
      const siblings = [...parent.children].filter((c) => c.tagName === node.tagName);
      parts.unshift(siblings.length > 1 ? `${tag}:nth-of-type(${siblings.indexOf(node) + 1})` : tag);
      node = parent;
    }
    return parts.join(' > ');
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

  addEventListener('popstate', () => {
    emit({
      kind: 'navigation',
      timeOrigin: performance.timeOrigin,
      pageTime: performance.now(),
      url: location.href,
    });
  });

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
