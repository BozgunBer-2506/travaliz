const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const html = fs.readFileSync(path.join(__dirname, '../templates/index.html'), 'utf8');
const tripType = html.slice(html.indexOf('function setTripType'), html.indexOf('// Multi-city'));
const removeLeg = html.slice(html.indexOf('function removeLeg'), html.indexOf('function initLegAutocomplete'));
const routeLabel = html.slice(html.indexOf('function flightRouteLabel'), html.indexOf('async function loadMyBookings'));

function element(extra = {}) {
  const classes = new Set();
  return {
    style: {}, value: '', dataset: {},
    classList: {
      add: c => classes.add(c), remove: c => classes.delete(c), contains: c => classes.has(c),
      toggle: (c, on) => (on ? classes.add(c) : classes.delete(c)),
    },
    ...extra,
  };
}

test('round-trip and multi-city buttons are enabled and wired to setTripType', () => {
  for (const [id, type] of [['btn-rt', 'round'], ['btn-mc', 'multi']]) {
    const tag = html.match(new RegExp(`<button[^>]*id="${id}"[^>]*>`))[0];
    assert.ok(!/\sdisabled[\s>]/.test(tag), `${id} must not be disabled`);
    assert.match(tag, new RegExp(`onclick="setTripType\\('${type}'\\)"`));
  }
});

test('setTripType shows the matching form fields', () => {
  const els = Object.fromEntries(['trip-type-input', 'btn-ow', 'btn-rt', 'btn-mc', 'ow-rt-form', 'multi-form', 'return-date-wrap', 'flight-intro']
    .map(id => [id, element()]));
  const context = { Date, document: { getElementById: id => els[id] || null, querySelectorAll: () => [], addEventListener() {} } };
  vm.runInNewContext(tripType, context);
  context.setTripType('round');
  assert.match(els['flight-intro'].textContent, /^Search round-trip flights/);
  context.setTripType('multi');
  assert.match(els['flight-intro'].textContent, /^Search multi-city trips of 2 to 6 flights/);
  context.setTripType('oneway');
  assert.match(els['flight-intro'].textContent, /^Search one-way flights/);
  context.setTripType('round');
  assert.equal(els['trip-type-input'].value, 'round');
  assert.equal(els['return-date-wrap'].classList.contains('hidden'), false);
  assert.equal(els['multi-form'].classList.contains('hidden'), true);
  assert.equal(els['btn-rt'].classList.contains('active'), true);
  context.setTripType('multi');
  assert.equal(els['ow-rt-form'].classList.contains('hidden'), true);
  assert.equal(els['multi-form'].classList.contains('hidden'), false);
  context.setTripType('oneway');
  assert.equal(els['return-date-wrap'].classList.contains('hidden'), true);
});

test('removing a leg renumbers field names so none is duplicated', () => {
  const legs = [0, 1, 2].map(i => {
    const inputs = ['from', 'fromEntity', 'to', 'toEntity', 'date'].map(f => ({ name: `leg${i}${f}`, dataset: { leg: String(i) } }));
    const leg = {
      label: { textContent: '' }, inputs,
      querySelector: () => leg.label,
      querySelectorAll: sel => (sel.startsWith('[name') ? inputs : inputs.filter(x => 'leg' in x.dataset)),
    };
    return leg;
  });
  let remaining = legs;
  const context = {
    legCount: 3,
    syncFlightDateMins() {},
    document: {
      getElementById: () => element(),
      querySelectorAll: () => remaining,
    },
  };
  vm.runInNewContext(removeLeg.replace('function removeLeg', 'var removeLeg = function'), context);
  remaining = [legs[0], legs[2]];
  context.removeLeg({ closest: () => ({ remove() {} }) });
  assert.deepEqual(legs[2].inputs.map(x => x.name), ['leg1from', 'leg1fromEntity', 'leg1to', 'leg1toEntity', 'leg1date']);
  assert.equal(legs[2].label.textContent, 'Leg 2');
});

test('return and multi-city dates cannot precede earlier flights', () => {
  const ret = { value: '2099-12-01', min: '' };
  const depart = { value: '2099-12-05' };
  const legs = ['2099-12-10', '2099-12-08', '2099-12-12', ''].map(value => ({ value, min: '' }));
  let changeHandler;
  const context = {
    Date,
    document: {
      getElementById: id => ({ 'flight-date': depart, 'return-date': ret }[id] || null),
      querySelectorAll: sel => (sel === '#multi-legs .leg-date' ? legs : []),
      addEventListener: (type, fn) => { if (type === 'change') changeHandler = fn; },
    },
  };
  vm.runInNewContext(tripType, context);
  context.syncFlightDateMins();
  assert.equal(ret.min, '2099-12-05');
  assert.equal(ret.value, '', 'a return before the departure is cleared');
  ret.value = '2099-12-05';
  context.syncFlightDateMins();
  assert.equal(ret.value, '2099-12-05', 'returning on the departure day is allowed');
  assert.deepEqual(legs.map(l => l.value), ['2099-12-10', '', '2099-12-12', '']);
  assert.deepEqual(legs.slice(1).map(l => l.min), ['2099-12-10', '2099-12-10', '2099-12-12']);
  // Changing a leg date re-applies the order to the following legs.
  legs[0].value = '2099-12-20';
  changeHandler({ target: { matches: sel => sel.includes('.leg-date') } });
  assert.equal(legs[2].value, '', 'a later leg before the changed one is cleared');
  assert.equal(legs[3].min, '2099-12-20');
});

test('booking modal closes on Escape and returns focus to its opener', () => {
  const start = html.indexOf('let bookingModalOpener');
  const src = html.slice(start, html.indexOf('}, true);', start) + '}, true);'.length);
  const classes = new Set();
  const modal = { classList: { add: c => classes.add(c), contains: c => classes.has(c) }, firstElementChild: { querySelectorAll: () => [] } };
  let keydown, focused = false, pickerOpen = false;
  const context = {
    bornOnPicker: { close() {} }, pendingBooking: {},
    document: {
      getElementById: id => (id === 'booking-modal' ? modal : null),
      querySelector: () => (pickerOpen ? {} : null),
      addEventListener: (type, fn) => { if (type === 'keydown') keydown = fn; },
    },
  };
  vm.runInNewContext(src, context);
  vm.runInContext('bookingModalOpener = opener', Object.assign(context, { opener: { isConnected: true, focus: () => { focused = true; } } }));
  const press = key => keydown({ key, preventDefault() {} });
  pickerOpen = true;
  press('Escape');
  assert.equal(classes.has('hidden'), false, 'an open date picker handles Escape first');
  pickerOpen = false;
  press('Escape');
  assert.equal(classes.has('hidden'), true);
  assert.equal(focused, true);
});

test('My Bookings route label covers every leg', () => {
  const context = {};
  vm.runInNewContext(routeLabel, context);
  assert.equal(context.flightRouteLabel([{ from_code: 'BER', to_code: 'LHR' }, { from_code: 'LHR', to_code: 'BER' }]), 'BER → LHR → BER');
  assert.equal(context.flightRouteLabel([{ from_code: 'BER', to_code: 'LHR' }, { from_code: 'CDG', to_code: 'IST' }]), 'BER → LHR · CDG → IST');
});

test('multi-city form is refilled with the searched legs, adding legs as needed', () => {
  const restore = html.slice(html.indexOf('function restoreSearchLegs'), html.indexOf('restoreSearchLegs();'));
  const makeLeg = () => {
    const fields = Object.fromEntries(['.leg-from-display', '.leg-from-sky', '.leg-to-display', '.leg-to-sky', '.leg-date']
      .map(sel => [sel, { value: '' }]));
    return { fields, querySelector: sel => fields[sel] || null };
  };
  const legs = [makeLeg(), makeLeg()];
  const saved = [['BER', 'LHR', '2099-12-01'], ['CDG', 'IST', '2099-12-05'], ['IST', 'BER', '2099-12-09']]
    .map(([from, to, date]) => ({ dataset: { from, to, date } }));
  const context = {
    legCount: 2,
    addLeg() { legs.push(makeLeg()); context.legCount++; },
    document: {
      querySelectorAll: sel => (sel.startsWith('#search-legs-data') ? saved : legs),
    },
  };
  vm.runInNewContext(restore, context);
  context.restoreSearchLegs();
  assert.equal(legs.length, 3);
  assert.deepEqual(legs.map(l => [l.fields['.leg-from-sky'].value, l.fields['.leg-to-sky'].value, l.fields['.leg-date'].value]),
    [['BER', 'LHR', '2099-12-01'], ['CDG', 'IST', '2099-12-05'], ['IST', 'BER', '2099-12-09']]);
  assert.equal(legs[2].fields['.leg-from-display'].value, 'IST');
});
