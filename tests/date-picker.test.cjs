// Force a non-UTC, negative-offset zone so a UTC-based date bug (e.g. using
// toISOString() or `new Date(isoString)` for a date-only value) would show
// up as an observable off-by-one day near local midnight, instead of being
// silently hidden by a test machine that happens to run in UTC.
process.env.TZ = 'America/New_York';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../static/date-picker.js'), 'utf8');

class FakeElement {
  constructor(tag = 'div') {
    this.tag = tag;
    this.id = '';
    this.value = '';
    this.disabled = false;
    this.className = '';
    this._text = '';
    this.children = [];
    this.attrs = {};
    this.listeners = {};
  }
  set textContent(v) { this._text = String(v); this.children = []; }
  get textContent() { return this._text; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  removeAttribute(k) { delete this.attrs[k]; }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter(f => f !== fn); }
  dispatch(type, evt = {}) { for (const fn of this.listeners[type] || []) fn(evt); }
  append(...nodes) { this.children.push(...nodes); for (const n of nodes) n.parent = this; }
  appendChild(node) { this.append(node); return node; }
  replaceChildren(...nodes) { this.children = []; this.append(...nodes); }
  remove() { if (this.parent) this.parent.children = this.parent.children.filter(c => c !== this); this.parent = null; }
  contains(node) {
    if (node === this) return true;
    return this.children.some(c => c.contains ? c.contains(node) : c === node);
  }
}

function makeDocument(ids) {
  const nodes = new Map();
  for (const id of ids) { const n = new FakeElement('input'); n.id = id; nodes.set(id, n); }
  const docListeners = {};
  return {
    nodes,
    getElementById: id => nodes.get(id),
    createElement: tag => new FakeElement(tag),
    addEventListener(type, fn, _opts) { (docListeners[type] ||= []).push(fn); },
    removeEventListener(type, fn) { docListeners[type] = (docListeners[type] || []).filter(f => f !== fn); },
    dispatch(type, evt = {}) { for (const fn of (docListeners[type] || []).slice()) fn(evt); },
  };
}

function loadContext(ids) {
  const document = makeDocument(ids);
  const context = vm.createContext({ document, console });
  vm.runInContext(source, context);
  return { context, document };
}

test('dpParseISO/dpToISO round-trip local calendar dates without a UTC shift', () => {
  const { context } = loadContext([]);
  for (const iso of ['2026-01-01', '2026-02-28', '2026-12-31', '2026-10-28']) {
    const roundTripped = vm.runInContext(`dpToISO(dpParseISO(${JSON.stringify(iso)}))`, context);
    assert.equal(roundTripped, iso);
  }
  // The exact bug class this guards against: a UTC-based parse of a date-only
  // string shifts backward a day in this negative-offset test timezone.
  const utcParsed = new Date('2026-10-28');
  assert.notEqual(utcParsed.getDate(), 28, 'test setup sanity: TZ must expose the UTC-shift bug class');
});

test('dpAddDays crosses month and year boundaries in local time', () => {
  const { context } = loadContext([]);
  assert.equal(vm.runInContext("dpToISO(dpAddDays(dpParseISO('2026-10-31'), 1))", context), '2026-11-01');
  assert.equal(vm.runInContext("dpToISO(dpAddDays(dpParseISO('2026-12-31'), 1))", context), '2027-01-01');
  assert.equal(vm.runInContext("dpToISO(dpAddDays(dpParseISO('2026-11-05'), -1))", context), '2026-11-04');
});

test('attachDatePicker disables days before getMin and does not select them on click', () => {
  const ids = ['wrap', 'display', 'hidden'];
  const { context, document } = loadContext(ids);
  document.getElementById('hidden').value = '2026-10-15';
  vm.runInContext(
    "var picker = attachDatePicker({ wrapperId: 'wrap', displayId: 'display', hiddenId: 'hidden', getMin: () => new Date(2026, 9, 10) })",
    context,
  );
  document.getElementById('display').dispatch('click');
  const panel = document.getElementById('wrap').children.find(c => c.className === 'dp-panel');
  assert.ok(panel, 'calendar panel should open');
  const grid = panel.children.find(c => c.className === 'dp-grid');
  const dayButtons = grid.children.filter(c => c.className?.startsWith('dp-day'));
  const day5 = dayButtons.find(b => b.textContent === '5'); // Oct 5 < min (Oct 10)
  const day20 = dayButtons.find(b => b.textContent === '20');
  assert.equal(day5.disabled, true);
  assert.equal(day5.getAttribute('aria-disabled'), 'true');
  day5.dispatch('click'); // no listener was attached for a disabled day
  assert.equal(document.getElementById('hidden').value, '2026-10-15', 'disabled day must not change the value');
  assert.equal(day20.disabled, false);
  day20.dispatch('click');
  assert.equal(document.getElementById('hidden').value, '2026-10-20');
});

test('selecting a day updates the hidden field, the display text and fires onChange, then closes', () => {
  const ids = ['wrap', 'display', 'hidden'];
  const { context, document } = loadContext(ids);
  const calls = [];
  // The callback executes in the outer realm; only plain data access (not
  // `instanceof`) on the vm-native Date it receives is meaningful here.
  context.__onChange = d => calls.push(`${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`);
  vm.runInContext(
    "var picker = attachDatePicker({ wrapperId: 'wrap', displayId: 'display', hiddenId: 'hidden', getMin: dpToday, onChange: __onChange })",
    context,
  );
  document.getElementById('display').dispatch('click');
  const panel = document.getElementById('wrap').children.find(c => c.className === 'dp-panel');
  const grid = panel.children.find(c => c.className === 'dp-grid');
  const today = vm.runInContext('dpToday()', context);
  const daysInMonth = new Date(today.getFullYear(), today.getMonth() + 1, 0).getDate();
  const targetDay = today.getDate() + 2 <= daysInMonth ? today.getDate() + 2 : daysInMonth;
  const btn = grid.children.find(c => c.textContent === String(targetDay) && !c.disabled);
  btn.dispatch('click');
  assert.equal(document.getElementById('hidden').value, vm.runInContext(`dpToISO(new Date(${today.getFullYear()}, ${today.getMonth()}, ${targetDay}))`, context));
  assert.ok(document.getElementById('display').value.length > 0, 'display text should be populated');
  assert.deepEqual(calls, [`${today.getFullYear()}-${today.getMonth()}-${targetDay}`]);
  assert.equal(document.getElementById('wrap').children.some(c => c.className === 'dp-panel'), false, 'panel should close after selection');
});

// Builds fixture dates relative to the real "today" (dpToday is not mocked),
// then clicks the panel's month-navigation an exact, pre-computed number of
// times so these tests keep passing regardless of which real month they run
// in. Matching by day-of-month text alone would be ambiguous (every month
// has a "5"), so the month step count is computed directly instead of
// probed from the rendered grid.
function addDaysReal(date, n) { return new Date(date.getFullYear(), date.getMonth(), date.getDate() + n); }
function selectDate(document, wrapperId, displayId, hiddenId, target) {
  const openingBase = dpParseISOReal(document.getElementById(hiddenId).value) || new Date();
  const monthsForward = (target.getFullYear() * 12 + target.getMonth()) - (openingBase.getFullYear() * 12 + openingBase.getMonth());
  document.getElementById(displayId).dispatch('click');
  const wrapper = document.getElementById(wrapperId);
  const panel = wrapper.children.find(c => c.className === 'dp-panel');
  const navLabel = monthsForward >= 0 ? 'Next month' : 'Previous month';
  const nav = panel.children.find(c => c.className === 'dp-header').children.find(c => c.getAttribute('aria-label') === navLabel);
  for (let i = 0; i < Math.abs(monthsForward); i++) nav.dispatch('click');
  const grid = panel.children.find(c => c.className === 'dp-grid');
  const hit = grid.children.find(c => c.textContent === String(target.getDate()) && c.className?.startsWith('dp-day'));
  if (!hit) throw new Error('target day button not found after navigating to its month');
  hit.dispatch('click');
}
function dpParseISOReal(iso) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(iso || ''));
  return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : null;
}

test('check-out re-opens with check-in + 1 as its minimum and the wiring auto-advances an invalid check-out', () => {
  const ids = ['checkin-wrapper', 'checkin-display', 'checkin', 'checkout-wrapper', 'checkout-display', 'checkout'];
  const { context, document } = loadContext(ids);
  const today = new Date();
  const iso = d => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  const initialCheckin = addDaysReal(today, 5);
  const initialCheckout = addDaysReal(today, 7);
  const newCheckin = addDaysReal(today, 8);
  const farCheckout = addDaysReal(today, 40);
  const laterCheckin = addDaysReal(today, 10);
  document.getElementById('checkin').value = iso(initialCheckin);
  document.getElementById('checkout').value = iso(initialCheckout);
  const wiring = `
    var checkoutPicker = null;
    var checkinPicker = attachDatePicker({
      wrapperId: 'checkin-wrapper', displayId: 'checkin-display', hiddenId: 'checkin', getMin: dpToday,
      onChange: (date) => {
        const checkoutHidden = document.getElementById('checkout');
        const minCheckout = dpAddDays(date, 1);
        const current = dpParseISO(checkoutHidden.value);
        if (!current || current < minCheckout) {
          checkoutHidden.value = dpToISO(minCheckout);
          checkoutPicker && checkoutPicker.refresh();
        }
      },
    });
    checkoutPicker = attachDatePicker({
      wrapperId: 'checkout-wrapper', displayId: 'checkout-display', hiddenId: 'checkout',
      getMin: () => dpAddDays(dpParseISO(document.getElementById('checkin').value) || dpToday(), 1),
    });
  `;
  vm.runInContext(wiring, context);

  // Moving check-in forward leaves the old check-out in the past relative to
  // the new stay, so it must be safely advanced, never left invalid.
  selectDate(document, 'checkin-wrapper', 'checkin-display', 'checkin', newCheckin);
  assert.equal(document.getElementById('checkin').value, iso(newCheckin));
  assert.equal(document.getElementById('checkout').value, iso(addDaysReal(newCheckin, 1)), 'check-out must move to check-in + 1');

  // Check-out's own calendar must disable every day up to and including the
  // (new) check-in date.
  document.getElementById('checkout-display').dispatch('click');
  let panel = document.getElementById('checkout-wrapper').children.find(c => c.className === 'dp-panel');
  let grid = panel.children.find(c => c.className === 'dp-grid');
  const sameDayAsCheckin = grid.children.find(c => c.textContent === String(newCheckin.getDate()) && c.className?.startsWith('dp-day'));
  const dayAfterCheckin = grid.children.find(c => c.textContent === String(addDaysReal(newCheckin, 1).getDate()) && c.className?.startsWith('dp-day'));
  assert.equal(sameDayAsCheckin.disabled, true, 'check-out cannot equal check-in');
  assert.equal(dayAfterCheckin.disabled, false);

  // Picking a check-out that is already valid and later than check-in+1 is left untouched.
  document.getElementById('checkout').value = iso(farCheckout);
  selectDate(document, 'checkin-wrapper', 'checkin-display', 'checkin', laterCheckin);
  assert.equal(document.getElementById('checkout').value, iso(farCheckout), 'a still-valid check-out must not be overwritten');
});

test('Escape closes an open panel and returns focus to the trigger', () => {
  const ids = ['wrap', 'display', 'hidden'];
  const { context, document } = loadContext(ids);
  vm.runInContext("attachDatePicker({ wrapperId: 'wrap', displayId: 'display', hiddenId: 'hidden' })", context);
  const display = document.getElementById('display');
  let focused = false;
  display.focus = () => { focused = true; };
  display.dispatch('click');
  assert.equal(document.getElementById('wrap').children.some(c => c.className === 'dp-panel'), true);
  document.dispatch('keydown', { key: 'Escape' });
  assert.equal(document.getElementById('wrap').children.some(c => c.className === 'dp-panel'), false);
  assert.equal(focused, true);
});
