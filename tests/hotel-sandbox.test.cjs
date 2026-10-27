const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

class Node {
  constructor(tag = 'div') { this.tag = tag; this.children = []; this.text = ''; this.disabled = false; }
  set textContent(value) { this.text = String(value); this.children = []; }
  get textContent() { return this.text + this.children.map(node => node.textContent).join(''); }
  set innerHTML(_) { assert.fail('hotel provider content reached an HTML parser'); }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.text = ''; this.children = nodes; }
}

function setup(outcome = 'confirmed_sandbox') {
  const nodes = new Map();
  for (const id of ['review-request-btn', 'pay-btn', 'payment-heading', 'payment-note', 'modal-header-title',
    'modal-total-label', 'modal-total-amount', 'modal-summary-content', 'payment-placeholder',
    'b-first-name', 'b-last-name', 'confirmed-title', 'confirmed-subtitle', 'confirmed-ref-label',
    'confirmed-ref', 'confirmed-name', 'confirmed-details']) nodes.set(id, new Node());
  nodes.get('b-first-name').value = 'Test'; nodes.get('b-last-name').value = 'Guest';
  const find = (node, id) => node.id === id ? node : node.children.map(child => find(child, id)).find(Boolean);
  const document = {
    createElement: tag => new Node(tag), createTextNode: text => { const n = new Node('text'); n.textContent = text; return n; },
    getElementById: id => nodes.get(id) || [...nodes.values()].map(node => find(node, id)).find(Boolean),
  };
  const calls = [], errors = [], steps = [];
  const quote = { hotelName: '<svg/onload=HACK>', roomName: 'Provider Room', checkin: '2099-01-01', checkout: '2099-01-02',
    price: 100, currency: 'EUR', adults: 2, children: 0, cancellation: { refundableTag: 'NRFN' },
    terms: '<img/src=x/onerror=HACK>', fees: [{ description: 'Local tax', amount: 5, currency: 'EUR', included: false }] };
  const context = vm.createContext({
    document, pendingBooking: { type: 'hotel', offerId: 'opaque-offer', price: 0.01, currency: 'USD' },
    _sb: { auth: { getSession: async () => ({ data: { session: { access_token: 'test-token' } } }) } },
    showError: (id, text) => errors.push({ id, text }), hideError() {}, showStep: n => steps.push(n),
    fetch: async (url, options) => {
      const body = JSON.parse(options.body); calls.push({ url, body });
      const data = url === '/hotel/prebook'
        ? { checkoutId: 'checkout', ref: 'checkout', status: 'quoted', quoteHash: 'digest', quote }
        : { ref: 'checkout', status: outcome, hotelBookingId: outcome === 'confirmed_sandbox' ? 'provider-booking' : undefined };
      return { ok: true, json: async () => data };
    },
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, '../static/hotel-booking.js'), 'utf8'), context);
  return { context, document, calls, errors, steps, nodes };
}

test('review uses verified price, taxes and cancellation terms as text', async () => {
  const f = setup();
  await vm.runInContext('reviewSandboxHotel()', f.context);
  assert.deepEqual(f.calls[0], { url: '/hotel/prebook', body: { offerId: 'opaque-offer' } });
  assert.equal(f.nodes.get('modal-total-amount').textContent, 'EUR 100.00');
  const details = f.nodes.get('payment-placeholder').textContent;
  assert.ok(details.includes('NRFN') && details.includes('not included in total'));
  assert.ok(details.includes('<img/src=x/onerror=HACK>'));
  assert.equal(f.steps.at(-1), 2);
});

test('explicit consent is required and repeated clicks send one confirmation', async () => {
  const f = setup(); await vm.runInContext('reviewSandboxHotel()', f.context);
  await vm.runInContext('submitSandboxHotel()', f.context);
  assert.equal(f.calls.length, 1);
  assert.match(f.errors.at(-1).text, /Accept/);
  f.document.getElementById('hotel-accept-terms').checked = true;
  await vm.runInContext('submitSandboxHotel();', f.context);
  await vm.runInContext('submitSandboxHotel();', f.context);
  assert.equal(f.calls.length, 2);
  assert.deepEqual(f.calls[1].body, { type: 'hotel', hotelCheckoutId: 'checkout', quoteHash: 'digest', acceptQuote: true, firstName: 'Test', lastName: 'Guest' });
  assert.equal(f.nodes.get('confirmed-ref').textContent, 'provider-booking');
  assert.match(f.nodes.get('confirmed-title').textContent, /sandbox/);
});

test('unknown outcome stays pending and never triggers a new prebook or automatic book retry', async () => {
  const f = setup('unknown'); await vm.runInContext('reviewSandboxHotel()', f.context);
  f.document.getElementById('hotel-accept-terms').checked = true;
  await vm.runInContext('submitSandboxHotel()', f.context);
  assert.equal(f.calls.length, 2);
  assert.match(f.nodes.get('confirmed-title').textContent, /pending/);
  assert.match(f.nodes.get('confirmed-subtitle').textContent, /do not place another reservation/);
});
