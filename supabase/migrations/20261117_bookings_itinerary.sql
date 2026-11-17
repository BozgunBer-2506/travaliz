-- Round-trip and multi-city flights are booked as one Duffel order whose
-- offer has several slices. The legacy flat columns (from_code, to_code,
-- depart_time, arrive_time) can describe only one leg, so My Bookings would
-- show such an itinerary incompletely. This nullable column stores every
-- slice, taken server-side from the Duffel offer (never from the client).
--
-- The application only sends this column for multi-slice flights; one-way
-- flights and hotel rows are unaffected. Apply this migration before
-- deploying the round-trip/multi-city release: until it exists, a
-- multi-slice booking's local write fails after the sandbox order succeeds,
-- leaving its flight_sandbox_checkouts lock at "order_placed" (a retry only
-- repeats the local write, never CreateOrder).

-- Compatibility: the column is nullable with no default, so existing rows
-- keep itinerary = null, are not rewritten, and pass the check below. Old
-- clients and one-way/hotel writes never send it. Security: no grant or RLS
-- policy is changed; the column inherits the table's existing access, and
-- the application reads it only through the service key, filtered by the
-- authenticated email in MyBookingsHandler.

alter table public.bookings
    add column if not exists itinerary jsonb;

-- 2-6 slices, matching the application's limits (round trip = 2, multi-city
-- up to 6). CASE guards jsonb_array_length, which errors on non-arrays.
alter table public.bookings
    drop constraint if exists bookings_itinerary_is_array;
alter table public.bookings
    add constraint bookings_itinerary_is_array
    check (
        itinerary is null
        or case when jsonb_typeof(itinerary) = 'array'
                then jsonb_array_length(itinerary) between 2 and 6
                else false
           end
    );
