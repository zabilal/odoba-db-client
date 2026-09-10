-- T0.38 — seed table for the W1 data-grid spike (RISK-1).
--
-- Deliberately mixed-width and mixed-type: a grid that is fast on ten integer
-- columns tells us nothing. Long text, NULLs, JSON, timestamps and numerics are
-- what actually cost time to lay out and measure.

DROP TABLE IF EXISTS grid_spike;

CREATE TABLE grid_spike (
    id          bigserial PRIMARY KEY,
    sku         text        NOT NULL,
    customer    text        NOT NULL,
    email       text,
    status      text        NOT NULL,
    quantity    integer     NOT NULL,
    unit_price  numeric(12,2) NOT NULL,
    total       numeric(14,2) NOT NULL,
    is_priority boolean     NOT NULL,
    notes       text,
    metadata    jsonb,
    placed_at   timestamptz NOT NULL,
    shipped_at  timestamptz
);

INSERT INTO grid_spike (
    sku, customer, email, status, quantity, unit_price, total,
    is_priority, notes, metadata, placed_at, shipped_at
)
SELECT
    'SKU-' || lpad((i % 99991)::text, 6, '0'),
    (ARRAY['Ada Lovelace','Grace Hopper','Alan Turing','Barbara Liskov',
           'Edsger Dijkstra','Katherine Johnson','Donald Knuth','Radia Perlman'])[1 + i % 8]
        || ' ' || (i % 4177)::text,
    CASE WHEN i % 7 = 0 THEN NULL
         ELSE 'user' || i || '@example.com' END,
    (ARRAY['pending','paid','shipped','delivered','refunded','cancelled'])[1 + i % 6],
    1 + (i % 40),
    ((i % 50000) / 100.0)::numeric(12,2),
    ((1 + (i % 40)) * ((i % 50000) / 100.0))::numeric(14,2),
    (i % 11 = 0),
    -- Every 13th row carries a long value: the worst case for text measurement.
    CASE WHEN i % 13 = 0
         THEN repeat('lorem ipsum dolor sit amet, consectetur adipiscing elit. ', 3)
         WHEN i % 3 = 0 THEN NULL
         ELSE 'note ' || i END,
    CASE WHEN i % 5 = 0 THEN NULL
         ELSE jsonb_build_object('channel', (ARRAY['web','ios','android','pos'])[1 + i % 4],
                                 'retries', i % 4,
                                 'region', (ARRAY['emea','amer','apac'])[1 + i % 3]) END,
    timestamptz '2020-01-01 00:00:00+00' + (i % 2000000) * interval '1 minute',
    CASE WHEN i % 4 = 0 THEN NULL
         ELSE timestamptz '2020-01-01 00:00:00+00' + (i % 2000000) * interval '1 minute'
              + interval '2 days' END
FROM generate_series(1, :rows) AS i;

CREATE INDEX grid_spike_status_idx  ON grid_spike (status);
CREATE INDEX grid_spike_placed_idx  ON grid_spike (placed_at);

ANALYZE grid_spike;
