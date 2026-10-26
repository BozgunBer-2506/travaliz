const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const html = fs.readFileSync(process.env.TRAVALIZ_ACCOUNT_TEMPLATE ||
  path.join(__dirname, '../templates/account.html'), 'utf8');
const script = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].at(-1)[1];
const payload = '<svg/onload=TRAVALIZ_XSS>';

// Exercise the template's actual JavaScript without a browser or dependencies.
// Fail whenever tainted data reaches an HTML parser instead of a text node.
class Element {
  constructor(tag) {
    this.tagName = tag;
    this.children = [];
    this.style = {};
    this.dataset = {};
    this.listeners = {};
    this.classList = { add() {}, remove() {} };
    this.textContent = '';
  }
  set textContent(value) { this.text = String(value ?? ''); this.children = []; }
  get textContent() { return this.text + this.children.map(child => child.textContent).join(''); }
  set innerHTML(value) {
    assert.ok(!value.includes('TRAVALIZ_XSS'), 'untrusted booking data reached innerHTML');
    this.textContent = value;
  }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.text = ''; this.children = nodes; }
  addEventListener(event, listener) { this.listeners[event] = listener; }
  getAttribute() { return null; }
  click() { this.listeners.click(); }
}

function account() {
  const nodes = new Map();
  const document = {
    body: new Element('body'),
    createElement: tag => new Element(tag),
    getElementById(id) {
      if (!nodes.has(id)) nodes.set(id, new Element('div'));
      return nodes.get(id);
    },
  };
  const context = vm.createContext({
    document, Chart: function() {}, window: { location: {} },
    supabase: { createClient: () => ({ auth: {
      getSession: async () => ({ data: { session: null } }),
    } }) },
  });
  vm.runInContext(script, context);
  return { context, node: id => document.getElementById(id) };
}

for (const type of ['hotel', 'flight', 'car']) {
  test(`${type} booking data remains text in list, details and analytics`, () => {
    const { context, node } = account();
    const booking = Object.fromEntries([
      'hotel_name', 'from_code', 'to_code', 'airline', 'checkin', 'checkout',
      'depart_time', 'arrive_time', 'duration', 'first_name', 'last_name', 'email', 'currency',
    ].map(key => [key, payload]));
    Object.assign(booking, { type, price: 12.34, stops: 0,
      ref: "');TRAVALIZ_XSS;//", created_at: '2026-01-01T00:00:00Z' });
    context.bookings = [booking];
    vm.runInContext('renderBookings(bookings)', context);
    const row = node('bookings-list').children[0];
    assert.ok(row.textContent.includes(payload));
    assert.ok(row.textContent.includes(booking.ref));
    row.click();
    assert.equal(node('bmodal-ref').textContent, booking.ref);
    assert.ok(node('bmodal-rows').textContent.includes(payload));
    for (const detail of node('bmodal-rows').children) {
      assert.equal(detail.children.length, 2);
      assert.equal(detail.children[1].children.length, 0);
    }
    vm.runInContext('initiateCancelBooking(); renderAnalytics(bookings)', context);
    assert.equal(node('cancel-confirm-btn').dataset.ref, booking.ref);
    assert.ok(node('top-destinations').textContent.includes(payload));
  });
}

test('rerendering clears stale bookings and keeps the empty state usable', () => {
  const { context, node } = account();
  context.bookings = [{ type: 'hotel', hotel_name: 'Example Hotel', ref: 'TM-TEST', price: 50 }];
  vm.runInContext('renderBookings(bookings); renderBookings(bookings)', context);
  assert.equal(node('bookings-list').children.length, 1);
  vm.runInContext('renderBookings([])', context);
  assert.equal(node('bookings-list').children.length, 1);
  assert.ok(node('bookings-list').textContent.includes('No bookings yet.'));
  assert.ok(!node('bookings-list').textContent.includes('Example Hotel'));
});
