const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const html = fs.readFileSync(path.join(__dirname, '../templates/index.html'), 'utf8');

const EXPECTED_CITIES = [
  'Paris', 'Tokyo', 'New York', 'Dubai', 'Barcelona', 'Istanbul',
  'Paris', 'London', 'Berlin', 'Istanbul', 'Antalya', 'Rome', 'Barcelona', 'Amsterdam',
  'Rome', 'Amsterdam', 'Prague', 'Lisbon',
];

test('every quick destination link carries a working href and an onclick guard', () => {
  const linkPattern = /<a href="(\/\?q=[^"]*)" onclick="return quickDestination\(&#39;([^&]*)&#39;, event\)"/g;
  const found = [...html.matchAll(linkPattern)].map(m => ({ href: decodeURIComponent(m[1].replace(/\+/g, ' ')), city: m[2] }));
  assert.equal(found.length, 18, 'expected exactly 18 quick destination links');
  assert.deepEqual(found.map(f => f.city), EXPECTED_CITIES);
  for (const { href, city } of found) assert.equal(href, '/?q=' + city);
});

const script = html.slice(html.indexOf('function saveGuestPrefs'), html.indexOf('// Init'));
const currencyScript = html.slice(html.indexOf('const CURRENCY_RATES'), html.indexOf('function toggleTheme'));

function setup({ savedNationality, savedCurrency } = {}) {
  const store = new Map();
  if (savedNationality) store.set('tm_guest_nationality', savedNationality);
  if (savedCurrency) store.set('tm_guest_currency', savedCurrency);
  const fields = Object.fromEntries(['hotel-nationality', 'hotel-currency', 'city-display', 'city-input', 'city-entity'].map(id => [id, {
    value: '', focusCalled: false, scrolledIntoView: false,
    focus() { this.focusCalled = true; },
    scrollIntoView() { this.scrolledIntoView = true; },
  }]));
  const toasts = [];
  let navigatedTo = null;
  const context = vm.createContext({
    document: {
      getElementById: id => fields[id],
      querySelector: () => null, // no server-rendered <option selected> in these tests
      addEventListener() {},
    },
    localStorage: {
      getItem: k => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, v),
    },
    showToast: msg => toasts.push(msg),
    location: { set href(v) { navigatedTo = v; }, get href() { return navigatedTo; } },
    URLSearchParams,
  });
  vm.runInContext(currencyScript + script, context);
  return { context, fields, toasts, store, getNavigatedTo: () => navigatedTo };
}

test('quickDestination sends the visitor to choose a nationality instead of guessing one', () => {
  const { context, fields, toasts, getNavigatedTo } = setup();
  context.preventedCount = 0;
  context.ev = { preventDefault: () => { context.preventedCount++; } };
  const result = vm.runInContext("quickDestination('Paris', ev)", context);
  assert.equal(context.preventedCount, 1);
  assert.equal(result, false);
  assert.equal(getNavigatedTo(), null, 'must not search with a guessed nationality');
  assert.equal(fields['city-display'].value, 'Paris');
  assert.equal(fields['city-input'].value, 'Paris');
  assert.equal(fields['hotel-nationality'].focusCalled, true);
  assert.equal(fields['hotel-nationality'].scrolledIntoView, true);
  assert.equal(toasts.length, 1);
  assert.match(toasts[0], /Paris/);
});

test('quickDestination runs the real search once a nationality is already known', () => {
  const { context, getNavigatedTo } = setup({ savedNationality: 'DE', savedCurrency: 'GBP' });
  vm.runInContext("quickDestination('New York', { preventDefault: () => {} })", context);
  const url = new URL(getNavigatedTo(), 'https://travaliz.com');
  assert.equal(url.pathname, '/');
  assert.equal(url.searchParams.get('q'), 'New York');
  assert.equal(url.searchParams.get('guestNationality'), 'DE');
  assert.equal(url.searchParams.get('currency'), 'GBP');
});

test('quickDestination clears a previous autocomplete entity before requesting nationality', () => {
  const { context, fields, getNavigatedTo } = setup();
  fields['city-entity'].value = 'Berlin';
  vm.runInContext("quickDestination('Paris', { preventDefault: () => {} })", context);
  assert.equal(fields['city-display'].value, 'Paris');
  assert.equal(fields['city-input'].value, 'Paris');
  assert.equal(fields['city-entity'].value, '');
  assert.equal(getNavigatedTo(), null);
});

test('quickDestination defaults currency to EUR, never to a guessed nationality default', () => {
  const { context, getNavigatedTo } = setup({ savedNationality: 'TR' });
  vm.runInContext("quickDestination('Rome', { preventDefault: () => {} })", context);
  const url = new URL(getNavigatedTo(), 'https://travaliz.com');
  assert.equal(url.searchParams.get('currency'), 'EUR');
});

test('saveGuestPrefs only persists a well-formed two-letter nationality', () => {
  const { context, fields, store } = setup();
  fields['hotel-nationality'].value = 'de';
  fields['hotel-currency'].value = 'USD';
  vm.runInContext('saveGuestPrefs()', context);
  assert.equal(store.get('tm_guest_nationality'), 'DE');
  assert.equal(store.get('currency'), 'USD');

  const { context: ctx2, fields: fields2, store: store2 } = setup();
  fields2['hotel-nationality'].value = 'Germany';
  vm.runInContext('saveGuestPrefs()', ctx2);
  assert.equal(store2.has('tm_guest_nationality'), false, 'a non-ISO value must not be saved');
});

test('prefillGuestPrefs fills an empty field from a prior saved choice but never overwrites one already set', () => {
  const { context, fields } = setup({ savedNationality: 'GB' });
  vm.runInContext('prefillGuestPrefs()', context);
  assert.equal(fields['hotel-nationality'].value, 'GB');

  const { context: ctx2, fields: fields2 } = setup({ savedNationality: 'GB' });
  fields2['hotel-nationality'].value = 'US';
  vm.runInContext('prefillGuestPrefs()', ctx2);
  assert.equal(fields2['hotel-nationality'].value, 'US', 'a server-rendered value must not be replaced');
});
