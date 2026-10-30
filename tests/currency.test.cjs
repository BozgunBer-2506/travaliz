const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const html = fs.readFileSync(path.join(__dirname, '../templates/index.html'), 'utf8');
const script = html.slice(html.indexOf('const CURRENCY_RATES'), html.indexOf('function toggleTheme')) +
  html.slice(html.indexOf('function saveGuestPrefs'), html.indexOf('// Init'));

function setup({ savedCurrency, legacyCurrency, renderedCurrency, url = '/' } = {}) {
  const store = new Map([['tm_guest_nationality', 'TR']]);
  if (savedCurrency) store.set('currency', savedCurrency);
  if (legacyCurrency) store.set('tm_guest_currency', legacyCurrency);
  const fields = Object.fromEntries([
    'currency-select', 'currency-select-mobile', 'hotel-currency', 'hotel-nationality',
    'city-display', 'city-input', 'city-entity',
  ].map(id => [id, { value: '' }]));
  const listeners = {};
  const navigations = [];
  let href = new URL(url, 'https://travaliz.com').href;
  const location = {
    get href() { return href; },
    set href(value) { href = new URL(value, href).href; navigations.push(href); },
  };
  const context = vm.createContext({
    document: {
      getElementById: id => fields[id],
      querySelector: () => renderedCurrency ? { value: renderedCurrency } : null,
      querySelectorAll: () => [],
      addEventListener: (event, fn) => { listeners[event] = fn; },
    },
    localStorage: {
      getItem: key => store.get(key) ?? null,
      setItem: (key, value) => store.set(key, value),
    },
    location, URL, URLSearchParams,
  });
  vm.runInContext(script, context);
  context.prefillGuestPrefs();
  listeners.DOMContentLoaded();
  return { context, fields, store, navigations, location };
}

function assertSelectors(fields, code) {
  for (const id of ['currency-select', 'currency-select-mobile', 'hotel-currency']) {
    assert.equal(fields[id].value, code, id);
  }
}

test('TRY results override a stale USD header preference on page load', () => {
  const f = setup({ savedCurrency: 'USD', legacyCurrency: 'TRY', renderedCurrency: 'TRY', url: '/?q=Dubai&currency=TRY' });
  assertSelectors(f.fields, 'TRY');
  assert.equal(f.store.get('currency'), 'TRY');
  assert.equal(f.navigations.length, 0);
});

test('switching away from TRY requests fresh offers and preserves the full search', () => {
  const search = '/?q=Dubai&entityId=Dubai&checkin=2099-11-10&checkout=2099-11-14&adults=2&children=1&children_ages=7&rooms=1&guestNationality=TR&currency=TRY#hotel-grid';
  const f = setup({ renderedCurrency: 'TRY', url: search });
  for (const code of ['USD', 'GBP', 'EUR', 'TRY', 'USD']) {
    f.context.setCurrency(code);
    assertSelectors(f.fields, code);
    assert.equal(f.store.get('currency'), code);
    const result = new URL(f.location.href);
    const expected = new URL(search, 'https://travaliz.com');
    expected.searchParams.set('currency', code);
    assert.equal(result.href, expected.href);
  }
  assert.equal(f.navigations.length, 5);
});

test('desktop, mobile and form currency selectors all use the shared handler', () => {
  for (const id of ['currency-select', 'currency-select-mobile', 'hotel-currency']) {
    assert.match(html, new RegExp(`<select id="${id}"[^>]*onchange="setCurrency\\(this.value\\)"`));
  }
});

test('landing page changes persist across reloads and quick destination searches', () => {
  const f = setup({ legacyCurrency: 'TRY' });
  f.context.setCurrency('USD');
  assertSelectors(f.fields, 'USD');
  assert.equal(f.navigations.length, 0);
  const reloaded = setup({ savedCurrency: f.store.get('currency'), legacyCurrency: 'TRY' });
  assertSelectors(reloaded.fields, 'USD');
  reloaded.context.quickDestination('Dubai', { preventDefault() {} });
  const search = new URL(reloaded.location.href);
  assert.equal(search.searchParams.get('currency'), 'USD');
  assert.equal(search.searchParams.get('guestNationality'), 'TR');
});

test('form submission saves the same preference as the header', () => {
  const f = setup({ savedCurrency: 'TRY' });
  f.fields['hotel-currency'].value = 'GBP';
  f.context.saveGuestPrefs();
  assertSelectors(f.fields, 'GBP');
  assert.equal(f.store.get('currency'), 'GBP');
  assert.equal(f.navigations.length, 0, 'submission must not trigger a second search');
});

test('category searches also reload in the chosen currency', () => {
  const f = setup({ renderedCurrency: 'TRY', url: '/?category=beach&guestNationality=TR&currency=TRY' });
  f.context.setCurrency('EUR');
  const search = new URL(f.location.href);
  assert.equal(search.searchParams.get('category'), 'beach');
  assert.equal(search.searchParams.get('currency'), 'EUR');
});

test('selecting the current currency does not reload or repeat provider calls', () => {
  for (const url of ['/?q=Dubai&currency=EUR', '/?q=Dubai']) {
    const f = setup({ renderedCurrency: 'EUR', url });
    f.context.setCurrency('EUR');
    assert.equal(f.navigations.length, 0);
  }
});

test('legacy preferences migrate, unsupported saved values fall back safely', () => {
  assertSelectors(setup({ legacyCurrency: 'GBP' }).fields, 'GBP');
  assertSelectors(setup({ savedCurrency: 'invalid', legacyCurrency: 'TRY' }).fields, 'TRY');
  const f = setup({ savedCurrency: 'invalid', legacyCurrency: 'invalid' });
  assertSelectors(f.fields, 'EUR');
  f.context.setCurrency('invalid');
  assertSelectors(f.fields, 'EUR');
  assert.equal(f.navigations.length, 0);
});

test('flight page preferences do not trigger a hotel search', () => {
  const f = setup({ url: '/flights?fromSky=BER&toSky=LHR' });
  f.context.setCurrency('USD');
  assertSelectors(f.fields, 'USD');
  assert.equal(f.navigations.length, 0);
});
