const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const html = fs.readFileSync(path.join(__dirname, '../templates/index.html'), 'utf8');
const counters = html.slice(html.indexOf('// Counter dropdowns'), html.indexOf('// Trip type'));
const submit = html.slice(html.indexOf('function submitFlightForm'), html.indexOf('// ── Car booking'));
const controls = html.slice(html.indexOf('<!-- Passengers -->'), html.indexOf('<!-- OW / RT fields -->'));

function setup(search = '') {
  const fields = {};
  for (const [id, value] of Object.entries({
    'f_adults-val': '1', 'f_adults-in': '1', 'f_children-val': '0',
    'f_children-in': '0', 'f_children-ages': '', 'flight-guests-label': '',
  })) fields[id] = { tagName: id.endsWith('-val') && id.includes('adults') ? 'SPAN' : 'INPUT', textContent: value, value };
  const rows = [];
  fields['f-child-rows'] = {
    querySelectorAll: () => rows,
    appendChild(row) { rows.push(row); },
  };
  const context = vm.createContext({
    document: {
      getElementById: id => fields[id],
      querySelectorAll: selector => selector === '#f-child-rows select' ? rows.map(row => row.select) : [],
      createElement: () => ({
        style: {}, select: { value: '0' }, label: {},
        querySelector(selector) { return selector === 'span' ? this.label : this.select; },
        remove() { rows.splice(rows.indexOf(this), 1); },
      }),
    },
    location: { search }, URLSearchParams, setLoading() {},
  });
  vm.runInContext(counters + submit, context);
  context.updateGuestLabel();
  return { fields, rows, context };
}

function click(fixture, handler) {
  const button = [...controls.matchAll(/<button\b[^>]*>/g)].map(match => match[0])
    .find(tag => tag.includes(`onclick="${handler}"`));
  assert.ok(button, `missing button for ${handler}`);
  assert.doesNotMatch(button, /\bdisabled\b/);
  vm.runInContext(handler, fixture.context);
}

test('flight adult buttons update the count, submitted field and passenger label', () => {
  const f = setup();
  click(f, "changeCount('f_adults',1,1,9)");
  assert.equal(f.fields['f_adults-val'].textContent, 2);
  assert.equal(f.fields['f_adults-in'].value, 2);
  assert.equal(f.fields['flight-guests-label'].textContent, '2 Adults');
  click(f, "changeCount('f_adults',-1,1,9)");
  click(f, "changeCount('f_adults',-1,1,9)");
  assert.equal(f.fields['f_adults-in'].value, 1);
});

test('adding and removing children keeps counts, ages and submission consistent', () => {
  const f = setup();
  click(f, "addChildRow('f')");
  click(f, "addChildRow('f')");
  f.rows[0].select.value = '8';
  f.rows[1].select.value = '11';
  assert.equal(f.fields['f_children-in'].value, 2);
  assert.equal(f.fields['flight-guests-label'].textContent, '1 Adult · 2 Children');
  let submitted = false;
  f.context.submitFlightForm({ preventDefault() {}, target: { submit() { submitted = true; } } });
  assert.equal(submitted, true);
  assert.equal(f.fields['f_children-ages'].value, '8,11');
  const firstRow = f.rows[0];
  f.context.removeChildRow({ closest: () => firstRow }, 'f');
  assert.equal(f.fields['f_children-in'].value, 1);
  assert.equal(f.rows[0].label.textContent, 'Child 1');
  assert.equal(f.fields['flight-guests-label'].textContent, '1 Adult · 1 Child');
  f.context.submitFlightForm({ preventDefault() {}, target: { submit() {} } });
  assert.equal(f.fields['f_children-ages'].value, '11');
});

test('flight passenger controls enforce a combined nine passenger limit', () => {
  const f = setup();
  for (let i = 0; i < 12; i++) f.context.changeCount('f_adults', 1, 1, 9);
  assert.equal(f.fields['f_adults-in'].value, 9);
  click(f, "addChildRow('f')");
  assert.equal(f.rows.length, 0);
  f.context.changeCount('f_adults', -1, 1, 9);
  click(f, "addChildRow('f')");
  f.context.changeCount('f_adults', 1, 1, 9);
  assert.equal(f.fields['f_adults-in'].value, 8);
  assert.equal(f.rows.length, 1);
});

test('child ages restore after search and at most six child rows can be added', () => {
  const f = setup('?children=2&children_ages=0,17');
  assert.deepEqual(f.rows.map(row => row.select.value), ['0', '17']);
  assert.equal(f.fields['f_children-in'].value, 2);
  for (let i = 0; i < 8; i++) f.context.addChildRow('f');
  assert.equal(f.rows.length, 6);
  assert.equal(f.fields['f_children-in'].value, 6);
});
