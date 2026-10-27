begin;

-- Server-only state: never delete/recreate a checkout after an uncertain book.
create table public.hotel_sandbox_checkouts (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null,
    offer_hash text not null check (length(offer_hash) = 64),
    status text not null check (status in ('prebooking', 'quoted', 'booking', 'unknown', 'confirmed_sandbox', 'failed')),
    prebook_id text unique,
    quote jsonb,
    guest jsonb,
    booking_id text unique,
    hotel_confirmation_code text,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (user_id, offer_hash),
    check (status not in ('quoted', 'booking', 'unknown', 'confirmed_sandbox') or (prebook_id is not null and quote is not null)),
    check (status <> 'confirmed_sandbox' or booking_id is not null)
);

alter table public.hotel_sandbox_checkouts enable row level security;
revoke all on public.hotel_sandbox_checkouts from public, anon, authenticated, service_role;
grant select, insert, update on public.hotel_sandbox_checkouts to service_role;

commit;
