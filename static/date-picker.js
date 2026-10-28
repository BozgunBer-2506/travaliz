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
// getMin, getMax, onChange, yearRange: {from, to} }. getMin/getMax are
// called fresh on every open and every render so a linked field (e.g.
// check-out after check-in changes) always disables the correct days.
// yearRange adds month/year <select>s for fields like date of birth, where
// stepping back a decade one month at a time is unusable.
//
// The panel is appended to document.body and positioned with `fixed`
// coordinates from the trigger's own getBoundingClientRect, not as an
// absolutely-positioned child of the field. This is a deliberate structural
// choice, not a flip hack: a popover nested inside any ancestor with
// overflow other than visible (e.g. the booking modal's scrollable body) is
// clipped by that ancestor regardless of its own position/z-index, and CSS
// computes an explicit overflow-y also clips overflow-x. Escaping to body
// removes the clipping ancestor and the modal's stacking context entirely.
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
  function onReflow() { reposition(); }
  function close() {
    if (!panel) return;
    panel.remove();
    panel = null;
    display.setAttribute('aria-expanded', 'false');
    document.removeEventListener('click', onOutsideClick, true);
    document.removeEventListener('keydown', onKeydown, true);
    window.removeEventListener('resize', onReflow);
    window.removeEventListener('scroll', onReflow, true);
  }
  function setValue(date) {
    hidden.value = dpToISO(date);
    syncDisplay();
    close();
    // The day button that was just clicked/activated is removed from the DOM
    // by close(); without this, focus silently falls back to <body>, same as
    // Escape already restores it explicitly above.
    display.focus();
    if (opts.onChange) opts.onChange(date);
  }

  // Positions the panel against the trigger in viewport coordinates and
  // clamps it fully inside the viewport, flipping above the trigger when
  // there is no room below. Runs after every render(): the grid's height
  // varies between 4 and 6 week-rows across months.
  function reposition() {
    if (!panel) return;
    const rect = display.getBoundingClientRect();
    const margin = 8;
    const panelWidth = panel.offsetWidth || 300;
    const panelHeight = panel.offsetHeight || 320;
    let left = rect.left;
    if (left + panelWidth > window.innerWidth - margin) left = window.innerWidth - panelWidth - margin;
    if (left < margin) left = margin;
    let top = rect.bottom + margin;
    if (top + panelHeight > window.innerHeight - margin && rect.top - panelHeight - margin > 0) {
      top = rect.top - panelHeight - margin;
    }
    panel.style.left = left + 'px';
    panel.style.top = top + 'px';
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
    const next = document.createElement('button');
    next.type = 'button'; next.className = 'dp-nav'; next.textContent = '›';
    next.setAttribute('aria-label', 'Next month');
    prev.addEventListener('click', () => { viewMonth--; if (viewMonth < 0) { viewMonth = 11; viewYear--; } render(); });
    next.addEventListener('click', () => { viewMonth++; if (viewMonth > 11) { viewMonth = 0; viewYear++; } render(); });
    if (opts.yearRange) {
      const monthSelect = document.createElement('select');
      monthSelect.className = 'dp-select';
      monthSelect.setAttribute('aria-label', 'Month');
      for (let m = 0; m < 12; m++) {
        const o = document.createElement('option');
        o.value = String(m);
        o.textContent = new Date(2000, m, 1).toLocaleDateString(undefined, { month: 'short' });
        if (m === viewMonth) o.selected = true;
        monthSelect.append(o);
      }
      monthSelect.addEventListener('change', () => { viewMonth = Number(monthSelect.value); render(); });
      const yearSelect = document.createElement('select');
      yearSelect.className = 'dp-select';
      yearSelect.setAttribute('aria-label', 'Year');
      for (let y = opts.yearRange.to; y >= opts.yearRange.from; y--) {
        const o = document.createElement('option');
        o.value = String(y);
        o.textContent = String(y);
        if (y === viewYear) o.selected = true;
        yearSelect.append(o);
      }
      yearSelect.addEventListener('change', () => { viewYear = Number(yearSelect.value); render(); });
      header.append(prev, monthSelect, yearSelect, next);
    } else {
      const label = document.createElement('span');
      label.className = 'dp-month-label';
      const monthDate = new Date(viewYear, viewMonth, 1);
      label.textContent = monthDate.toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
      header.append(prev, label, next);
    }
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
    reposition();
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
    document.body.appendChild(panel);
    render(); // also positions the panel; see reposition()
    display.setAttribute('aria-expanded', 'true');
    document.addEventListener('click', onOutsideClick, true);
    document.addEventListener('keydown', onKeydown, true);
    window.addEventListener('resize', onReflow);
    // Capture so a scroll inside the modal's own scrollable body (an
    // ancestor of the trigger, not of this body-level panel) is still seen.
    window.addEventListener('scroll', onReflow, true);
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
