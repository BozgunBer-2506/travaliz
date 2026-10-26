const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const html = fs.readFileSync(path.join(__dirname, '../templates/index.html'), 'utf8');
const script = html.slice(html.indexOf('const cityDisplay ='), html.indexOf('const fromDisplay ='));

function search() {
  const fields = Object.fromEntries(['city-display', 'city-input', 'city-entity', 'city-help'].map(id => [id, {
    value: '', validationMessage: '', listeners: {},
    addEventListener(event, handler) { this.listeners[event] = handler; },
    setCustomValidity(message) { this.validationMessage = message; },
    reportValidity() { return this.value !== '' && this.validationMessage === ''; },
  }]));
  let select;
  const context = vm.createContext({
    document: { getElementById: id => fields[id] },
    makeAutocomplete: (_input, _url, callback) => { select = callback; },
  });
  vm.runInContext(script, context);
  return { fields, select, validate: () => vm.runInContext('validateHotelDestination()', context) };
}

test('a typed full city name can be submitted without selecting a suggestion', () => {
  for (const name of ['Berlin', '  bErLiN  ', 'İstanbul', 'Londra', 'Roma']) {
    const { fields, validate } = search();
    const city = fields['city-display'];
    city.value = name;
    city.listeners.input();
    assert.equal(city.validationMessage, '');
    assert.equal(validate(), true);
    assert.equal(fields['city-input'].value, name.trim());
    assert.equal(fields['city-entity'].value, '');
  }
});

test('editing a selected city clears the stale entity and preserves suggestion selection', () => {
  const { fields, select, validate } = search();
  select({ name: 'Berlin', entityId: 'Berlin', hierarchy: 'Germany' });
  assert.equal(validate(), true);
  assert.equal(fields['city-entity'].value, 'Berlin');
  fields['city-display'].value = 'Paris';
  fields['city-display'].listeners.input();
  assert.equal(validate(), true);
  assert.equal(fields['city-input'].value, 'Paris');
  assert.equal(fields['city-entity'].value, '');
});

test('autofilled city text cannot retain a different previously selected entity', () => {
  const { fields, select, validate } = search();
  select({ name: 'Berlin', entityId: 'Berlin', hierarchy: 'Germany' });
  fields['city-display'].value = 'London';
  assert.equal(validate(), true);
  assert.equal(fields['city-input'].value, 'London');
  assert.equal(fields['city-entity'].value, '');
});

test('blank destinations are rejected and typing clears an earlier validation error', () => {
  const { fields, validate } = search();
  fields['city-display'].value = '   ';
  assert.equal(validate(), false);
  assert.equal(fields['city-display'].validationMessage, 'Enter a destination city.');
  fields['city-display'].value = 'Berlin';
  fields['city-display'].listeners.input();
  assert.equal(fields['city-display'].validationMessage, '');
  assert.equal(validate(), true);
});
