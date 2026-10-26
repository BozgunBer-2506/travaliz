const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

test('hotel /book 409 displays the error without saving or confirming a booking', async () => {
  const html = fs.readFileSync(path.join(__dirname, '../templates/index.html'), 'utf8');
  const script = html.slice(html.indexOf('function showError('), html.indexOf('// Hotel requests use'));
  const message = 'Hotel booking is not available yet. No reservation was made and no payment was taken.';
  let hidden = true;
  const error = { textContent: '', classList: {
    add: () => { hidden = true; }, remove: () => { hidden = false; },
  } };
  const button = { disabled: false, textContent: 'Save Request' };
  const fields = {
    'step2-error': error, 'pay-btn': button,
    'b-first-name': { value: 'Test' }, 'b-last-name': { value: 'Guest' },
    'b-email': { value: 'guest@example.com' }, 'b-phone': { value: '+49123456789' },
  };
  let calls = 0;
  const context = vm.createContext({
    pendingBooking: { type: 'hotel', name: 'Example Hotel', price: 50, currency: 'EUR' },
    document: { getElementById: id => {
      assert.ok(fields[id], `unexpected DOM access: ${id}`);
      return fields[id];
    } },
    _sb: { auth: { getSession: async () => ({ data: { session: { access_token: 'test-token' } } }) } },
    fetch: async (url, options) => {
      calls++;
      assert.equal(url, '/book');
      assert.equal(options.method, 'POST');
      assert.equal(JSON.parse(options.body).type, 'hotel');
      return { ok: false, status: 409, json: async () => ({ error: message }) };
    },
    addBooking: () => assert.fail('rejected hotel request was saved locally'),
    showStep: () => assert.fail('rejected hotel request was shown as confirmed'),
  });
  vm.runInContext(script, context);
  await vm.runInContext('submitBooking()', context);
  assert.equal(calls, 1);
  assert.equal(hidden, false);
  assert.equal(error.textContent, message);
  assert.equal(button.disabled, false);
  assert.equal(button.textContent, 'Save Request');
});
