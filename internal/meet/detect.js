// Read only: no media capture, clicks, or inspection of conversation content.
(() => {
  if (location.origin !== 'https://meet.google.com' ||
      !/^\/[a-z]{3}-[a-z]{4}-[a-z]{3}\/?$/.test(location.pathname)) return false;
  return Array.from(document.querySelectorAll('button, [role="button"]')).some(button => {
    if (!button.getClientRects().length || getComputedStyle(button).visibility !== 'visible' ||
        button.closest('[aria-hidden="true"]')) return false;
    // Material's icon name is independent of the UI language. Accessible
    // names cover layouts that render the hangup icon as an SVG instead.
    const icon = Array.from(button.querySelectorAll('i, span')).some(el =>
      el.textContent.trim() === 'call_end');
    const label = (button.getAttribute('aria-label') || '').trim().toLowerCase();
    return icon || ['leave call', 'leave the call', 'salir de la llamada',
      'abandonar la llamada', 'anruf verlassen', 'appel beenden'].includes(label);
  });
})()
