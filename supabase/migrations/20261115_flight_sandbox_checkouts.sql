begin;

-- Minimal, flight-specific counterpart to hotel_sandbox_checkouts: a durable
-- lock row per (user, offer) that gates every CreateOrder attempt, so a
-- concurrent or retried /book request cannot create a second real sandbox
-- order for the same offer. Server-only state; never delete/recreate a row
-- after an uncertain order.
--
-- CreateOrder is only ever called from the 'pending' state (i.e. at most
-- once per row, ever) -- this schema and the handler built on it do not
-- assume Duffel's Idempotency-Key header is honoured, so a retried call is
-- never made on the strength of that header alone.
--
-- Statuses:
--   pending            fresh lock, no provider call made yet.
--   ordering           CreateOrder call in flight (or, per the handler,
--                       treated as abandoned once stale -- see
--                       flightOrderStaleAfter in booking_handler.go).
--   unknown            CreateOrder outcome unconfirmed (timeout). Never
--                       retried automatically; only a best-effort,
--                       read-only GET /air/orders lookup may resolve it
--                       (see matchOfferOrder in internal/duffel), and that
--                       lookup's filter is NOT confirmed against Duffel's
--                       real API in this codebase -- treat it as advisory.
--   order_placed       CreateOrder (or a confirmed GET reconciliation)
--                       succeeded at Duffel; order_id/booking_reference are
--                       known, but the local bookings-table row is not yet
--                       written. Recoverable without any further Duffel
--                       call: a later request only retries the local DB
--                       write.
--   confirmed_sandbox  Both the Duffel order and the local bookings row
--                       exist.
--   failed             Duffel rejected the order outright (non-timeout).
--   needs_review       An 'unknown' outcome could not be resolved from the
--                       read-only lookup above (ambiguous or failed query).
--                       Terminal: this schema has no automated path out of
--                       it, by design -- resolving it is a manual/support
--                       action outside this codebase.
create table public.flight_sandbox_checkouts (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null,
    offer_id text not null check (length(offer_id) between 1 and 512),
    status text not null check (status in ('pending', 'ordering', 'unknown', 'order_placed', 'confirmed_sandbox', 'failed', 'needs_review')),
    order_id text,
    booking_reference text,
    booking_ref text,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (user_id, offer_id),
    check (status not in ('order_placed', 'confirmed_sandbox') or order_id is not null),
    check (status <> 'confirmed_sandbox' or booking_ref is not null)
);

-- RLS note (matches hotel_sandbox_checkouts, same tradeoff already accepted
-- there): this table defines no RLS policies, so with RLS enabled, anon and
-- authenticated get zero rows by Postgres's default-deny -- that is the
-- actual protection against a leaked anon/publishable key. service_role, as
-- used by this server, bypasses RLS entirely in Supabase; row ownership
-- (the user_id filter) is enforced only in application code (every
-- ReserveFlight/GetFlight/UpdateFlight call in internal/db/flight.go), not
-- by a database policy. That is a known, pre-existing pattern in this
-- codebase, not something newly introduced here.
alter table public.flight_sandbox_checkouts enable row level security;
revoke all on public.flight_sandbox_checkouts from public, anon, authenticated, service_role;
grant select, insert, update on public.flight_sandbox_checkouts to service_role;

commit;
