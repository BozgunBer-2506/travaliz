# LiteAPI hotel sandbox

This flow does not enable production reservations or payments. Existing flight
booking and B2B webhook payloads are unchanged. Hotel sandbox bookings never
invoke the B2B webhook or collect card details.

## Setup

1. Apply `supabase/migrations/20261026_hotel_sandbox_checkouts.sql` in the existing
   Supabase project's SQL Editor. It creates one isolated table, enables RLS,
   and grants only server-side select/insert/update access. No existing table
   is modified; browser roles and service-role DELETE are denied.
2. Set `LITEAPI_API_KEY` to a `sand_` or `sandbox_` key and explicitly set
   `LITEAPI_SANDBOX_BOOKING_ENABLED=true` in the local server environment.
   The application does not automatically load `.env`. Booking is disabled
   by default; a `prod_` or unknown key is rejected even when the flag is true.
3. Search hotels, sign in, select an offer and enter the lead guest's details.
   Prebook verifies the opaque offer and retrieves provider hotel content,
   price, dates, occupancy, fees and cancellation terms. Review and explicitly
   accept the quote. Only then does `/book` use `ACC_CREDIT_CARD` in sandbox.

## Durability and recovery

Each user/offer hash has one durable checkout. PostgreSQL's unique constraint
prevents concurrent prebooks for that same selection. An owner-scoped atomic
`quoted -> booking` update is persisted before the provider request; only its
winner can call book. The checkout UUID is also LiteAPI's `clientReference`.
Prebook/booking identifiers and guest details remain in the server-only table.
The client sees its checkout UUID and confirmed booking reference, not an API
key, payment token or opaque offer token returned from prebook.

Quotes have an application-level ten-minute confirmation window. The server
retrieves the same prebook again and checks the accepted quote digest before
claiming it. A changed price, fee or cancellation policy requires a new search.

A timeout, 409, 503, malformed response, crash or uncertain storage response
never releases a booking lock and never automatically resubmits a book. The
outcome stays `booking`/`unknown`. The account's refresh action performs only a
GET, filtered by sandbox, dates and the server-generated checkout tag; it
requires the matching prebook and client reference before saving confirmation.
A missing recovery result stays unknown and requires investigation. Do not
delete checkout rows or start a replacement booking while investigating.

Local request removal does not cancel provider bookings. Sandbox checkout
references cannot be removed through `/cancel-booking`; provider cancellation
is intentionally outside this implementation.

## Validation

`go test ./...` covers production-key rejection, ownership, price/terms binding,
concurrent confirmations, provider timeout/recovery and ambiguous database
writes. Run `go test -race ./internal/handlers ./internal/db` for concurrency
checks and `node --test tests/*.test.cjs` for UI regressions. These tests use
offline transports and do not send messages, create provider bookings or spend
quota. A live end-to-end sandbox test additionally requires the migration and
real Supabase authentication; never substitute a production key.

Official contracts: [prebook](https://docs.liteapi.travel/reference/post_rates-prebook),
[book](https://docs.liteapi.travel/reference/post_rates-book),
[booking lookup](https://docs.liteapi.travel/reference/get_bookings).
Live verification established two response details: GET prebook omits the
top-level sandbox marker, while booking-list recovery requires a date pair.
