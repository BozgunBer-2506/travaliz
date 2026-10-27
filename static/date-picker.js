// Themed replacement for the native <input type="date"> picker. No external
// library: this project has no package.json/bundler, so a small vanilla
// component is the smallest correct fix (see CLAUDE.md dependency ladder).
//
// Local-calendar date helpers. Never use Date.prototype.toISOString() or
// `new Date(isoString)` for a date-only value: both parse/format via UTC and
// silently shift the day near local midnight depending on the visitor's
// timezone offset. Every date here is built from explicit local Y/M/D parts.
function dpToday() {
  const d = new Date();
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}
function dpParseISO(iso) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(iso || ''));
  if (!m) return null;
  const date = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  return Number.isNaN(date.getTime()) ? null : date;
}
function dpToISO(date) {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, '0');
  const d = String(date.getDate()).padStart(2, '0');
  return `${y}-${m}-${d}`;
}
function dpAddDays(date, n) {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate() + n);
}
function dpSameDay(a, b) {
  return !!a && !!b && dpToISO(a) === dpToISO(b);
}
function dpFormatDisplay(date) {
  return date.toLocaleDateString(undefined, { day: '2-digit', month: 'short', year: 'numeric' });
}

// attachDatePicker wires a readonly display input + a hidden form input to a
// themed calendar popover. opts: { wrapperId, displayId, hiddenId, label,
// getMin, getMax, onChange }. getMin/getMax are called fresh on every open
// and every render so a linked field (e.g. check-out after check-in changes)
// always disables the correct days.
function attachDatePicker(opts) {
  const wrapper = document.getElementById(opts.wrapperId);
  const display = document.getElementById(opts.displayId);
  const hidden = document.getElementById(opts.hiddenId);
  if (!wrapper || !display || !hidden) return null;
  let panel = null;
  let viewYear = 0;
  let viewMonth = 0;

  function currentDate() { return dpParseISO(hidden.value); }
  function syncDisplay() {
    const d = currentDate();
    display.value = d ? dpFormatDisplay(d) : '';
  }
  syncDisplay();

  function onOutsideClick(e) {
    if (panel && !panel.contains(e.target) && !wrapper.contains(e.target)) close();
  }
  function onKeydown(e) {
    if (e.key === 'Escape') { close(); display.focus(); }
  }
  function close() {
    if (!panel) return;
    panel.remove();
    panel = null;
    display.setAttribute('aria-expanded', 'false');
    document.removeEventListener('click', onOutsideClick, true);
    document.removeEventListener('keydown', onKeydown, true);
  }
  function setValue(date) {
    hidden.value = dpToISO(date);
    syncDisplay();
    close();
    if (opts.onChange) opts.onChange(date);
  }

  function render() {
    panel.replaceChildren();
    const min = opts.getMin ? opts.getMin() : null;
    const max = opts.getMax ? opts.getMax() : null;

    const header = document.createElement('div');
    header.className = 'dp-header';
    const prev = document.createElement('button');
    prev.type = 'button'; prev.className = 'dp-nav'; prev.textContent = '‹';
    prev.setAttribute('aria-label', 'Previous month');
    const label = document.createElement('span');
    label.className = 'dp-month-label';
    const monthDate = new Date(viewYear, viewMonth, 1);
    label.textContent = monthDate.toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
    const next = document.createElement('button');
    next.type = 'button'; next.className = 'dp-nav'; next.textContent = '›';
    next.setAttribute('aria-label', 'Next month');
    prev.addEventListener('click', () => { viewMonth--; if (viewMonth < 0) { viewMonth = 11; viewYear--; } render(); });
    next.addEventListener('click', () => { viewMonth++; if (viewMonth > 11) { viewMonth = 0; viewYear++; } render(); });
    header.append(prev, label, next);
    panel.append(header);

    const grid = document.createElement('div');
    grid.className = 'dp-grid';
    grid.setAttribute('role', 'grid');
    const weekdayBase = new Date(2023, 0, 2); // a Monday, for locale weekday labels
    for (let i = 0; i < 7; i++) {
      const cell = document.createElement('div');
      cell.className = 'dp-weekday';
      cell.setAttribute('aria-hidden', 'true');
      cell.textContent = new Date(weekdayBase.getFullYear(), weekdayBase.getMonth(), weekdayBase.getDate() + i)
        .toLocaleDateString(undefined, { weekday: 'narrow' });
      grid.append(cell);
    }
    const firstOfMonth = new Date(viewYear, viewMonth, 1);
    let startOffset = firstOfMonth.getDay() - 1; // Monday-first grid
    if (startOffset < 0) startOffset = 6;
    const daysInMonth = new Date(viewYear, viewMonth + 1, 0).getDate();
    for (let i = 0; i < startOffset; i++) grid.append(document.createElement('div'));
    const selected = currentDate();
    const today = dpToday();
    for (let day = 1; day <= daysInMonth; day++) {
      const date = new Date(viewYear, viewMonth, day);
      const disabled = !!(min && date < min) || !!(max && date > max);
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'dp-day' + (dpSameDay(date, selected) ? ' dp-day-selected' : '') + (dpSameDay(date, today) ? ' dp-day-today' : '');
      btn.textContent = String(day);
      btn.disabled = disabled;
      btn.setAttribute('aria-disabled', String(disabled));
      if (dpSameDay(date, selected)) btn.setAttribute('aria-selected', 'true');
      btn.setAttribute('aria-label', date.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' }));
      if (!disabled) btn.addEventListener('click', () => setValue(date));
      grid.append(btn);
    }
    panel.append(grid);
  }

  function open() {
    if (panel) return;
    const base = currentDate() || (opts.getMin && opts.getMin()) || dpToday();
    viewYear = base.getFullYear();
    viewMonth = base.getMonth();
    panel = document.createElement('div');
    panel.className = 'dp-panel';
    panel.setAttribute('role', 'dialog');
    panel.setAttribute('aria-modal', 'false');
    panel.setAttribute('aria-label', opts.label || 'Choose a date');
    wrapper.appendChild(panel);
    render();
    display.setAttribute('aria-expanded', 'true');
    document.addEventListener('click', onOutsideClick, true);
    document.addEventListener('keydown', onKeydown, true);
    // Flip to the other side if the panel would overflow the viewport;
    // real layout metrics only exist once the panel is actually in the DOM.
    if (typeof window !== 'undefined' && panel.getBoundingClientRect) {
      const rect = panel.getBoundingClientRect();
      if (rect.right > window.innerWidth) { panel.style.left = 'auto'; panel.style.right = '0'; }
      if (rect.bottom > window.innerHeight) { panel.style.top = 'auto'; panel.style.bottom = 'calc(100% + 8px)'; }
    }
  }

  display.setAttribute('role', 'button');
  display.setAttribute('tabindex', '0');
  display.setAttribute('aria-haspopup', 'dialog');
  display.setAttribute('aria-expanded', 'false');
  if (opts.label) display.setAttribute('aria-label', opts.label);
  display.addEventListener('click', open);
  display.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(); }
  });

  return { refresh: syncDisplay, close };
}
