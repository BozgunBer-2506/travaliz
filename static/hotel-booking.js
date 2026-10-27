// All price/availability/terms shown here come from the authenticated prebook
// response. No card form, payment SDK, production mode or automatic book retry.
async function hotelSession() {
  const { data: { session } } = await _sb.auth.getSession();
  if (!session) throw new Error('Your session expired. Please sign in again.');
  return session;
}

async function hotelJSON(path, body) {
  const session = await hotelSession();
  const response = await fetch(path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + session.access_token },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  const data = await response.json();
  if (!response.ok || data.error) throw new Error(data.error || 'Hotel checkout is unavailable.');
  return data;
}

function hotelText(parent, text) {
  const p = document.createElement('p');
  p.textContent = text;
  parent.append(p);
}

async function reviewSandboxHotel() {
  const booking = pendingBooking;
  const button = document.getElementById('review-request-btn');
  if (button.disabled) return;
  button.disabled = true;
  hideError('step1-error');
  try {
    const data = await hotelJSON('/hotel/prebook', { offerId: booking.offerId || '' });
    if (pendingBooking !== booking) return;
    booking.hotelCheckout = data;
    if (['confirmed_sandbox', 'booking', 'unknown'].includes(data.status)) { showSandboxHotelOutcome(data); return; }
    if (data.status !== 'quoted' || !data.quote) throw new Error('This checkout is still being verified. Wait before trying again.');
    const q = data.quote;
    document.getElementById('payment-heading').textContent = 'Hotel sandbox confirmation';
    document.getElementById('payment-note').textContent = 'Sandbox test only: no real hotel reservation or card charge. Review the verified price and cancellation terms before confirming.';
    document.getElementById('modal-header-title').textContent = q.hotelName;
    const summary = document.getElementById('modal-summary-content');
    summary.replaceChildren();
    hotelText(summary, q.hotelName + ' · ' + q.checkin + ' → ' + q.checkout);
    document.getElementById('modal-total-label').textContent = 'Verified sandbox total';
    document.getElementById('modal-total-amount').textContent = q.currency + ' ' + q.price.toFixed(2);
    const details = document.getElementById('payment-placeholder');
    details.replaceChildren();
    hotelText(details, q.hotelName + ' · ' + q.roomName);
    hotelText(details, q.checkin + ' → ' + q.checkout + ' · ' + q.adults + ' adults · ' + q.children + ' children');
    hotelText(details, 'Cancellation: ' + q.cancellation.refundableTag);
    for (const rule of q.cancellation.cancelPolicyInfos || []) {
      hotelText(details, `${rule.cancelTime} ${rule.timezone || ''}: ${rule.amount} ${rule.currency} (${rule.type})`);
    }
    for (const remark of q.cancellation.hotelRemarks || []) hotelText(details, remark);
    for (const fee of q.fees || []) hotelText(details, `${fee.description}: ${fee.amount} ${fee.currency} · ${fee.included ? 'included' : 'not included in total'}`);
    if (q.terms) hotelText(details, q.terms);
    const label = document.createElement('label');
    label.className = 'flex gap-2 my-4 text-sm';
    const accept = document.createElement('input');
    accept.type = 'checkbox'; accept.id = 'hotel-accept-terms';
    label.append(accept, document.createTextNode('I accept this price and these cancellation terms for a sandbox test booking.'));
    details.append(label);
    document.getElementById('pay-btn').textContent = 'Confirm sandbox booking';
    document.getElementById('pay-btn').disabled = false;
    hideError('step2-error');
    showStep(2);
  } catch (error) {
    if (pendingBooking === booking) showError('step1-error', error.message);
  } finally { button.disabled = false; }
}

async function submitSandboxHotel() {
  const booking = pendingBooking;
  if (!document.getElementById('hotel-accept-terms')?.checked) return showError('step2-error', 'Accept the verified price and cancellation terms first.');
  const button = document.getElementById('pay-btn');
  if (button.disabled) return;
  button.disabled = true;
  try {
    const data = await hotelJSON('/book', {
      type: 'hotel', hotelCheckoutId: booking.hotelCheckout.checkoutId,
      quoteHash: booking.hotelCheckout.quoteHash, acceptQuote: true,
      firstName: document.getElementById('b-first-name').value.trim(),
      lastName: document.getElementById('b-last-name').value.trim(),
    });
    booking.hotelCheckout = data;
    if (pendingBooking === booking) showSandboxHotelOutcome(data);
  } catch (error) {
    // Retrying this same checkout is server-idempotent. Never create a new
    // prebook or retry a provider book from a network-error handler.
    showError('step2-error', error.message + ' If the result is unclear, check My Bookings before placing another reservation.');
    button.disabled = false;
  }
}

function showSandboxHotelOutcome(data) {
  const confirmed = data.status === 'confirmed_sandbox';
  document.getElementById('confirmed-title').textContent = confirmed ? 'Hotel test booking confirmed (sandbox)' : 'Hotel booking result pending';
  document.getElementById('confirmed-subtitle').textContent = confirmed
    ? 'Sandbox only — no real hotel reservation or card charge.'
    : 'Outcome unknown. Check My Bookings; do not place another reservation.';
  document.getElementById('confirmed-ref-label').textContent = confirmed ? 'Sandbox booking ID' : 'Checkout reference';
  document.getElementById('confirmed-ref').textContent = data.hotelBookingId || data.ref;
  document.getElementById('confirmed-name').textContent = '';
  document.getElementById('confirmed-details').textContent = data.hotelConfirmationCode ? 'Hotel confirmation: ' + data.hotelConfirmationCode : 'Checkout: ' + data.ref;
  showStep(3);
}
