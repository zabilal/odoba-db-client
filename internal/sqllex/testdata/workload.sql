-- Generated workload for spike W2 (T0.48): 5000 lines of realistic SQL.
/* Includes multi-line comments, string literals containing quotes and
   comment markers, CTEs, window functions and dollar-quoted bodies --
   all the things that make a lexer's state span more than one line. */
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 13
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000002';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 3 */
CREATE INDEX CONCURRENTLY idx_orders_4 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-5', 'reprice', '{"sku":"SKU-000005","delta":5}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_6(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-08' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_8 text;
WITH recent_9 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_10 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 130
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000011';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 12 */
CREATE INDEX CONCURRENTLY idx_orders_13 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-14', 'reprice', '{"sku":"SKU-000014","delta":14}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_15(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-17' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_17 text;
WITH recent_18 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_19 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 247
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000020';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 21 */
CREATE INDEX CONCURRENTLY idx_orders_22 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-23', 'reprice', '{"sku":"SKU-000023","delta":23}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_24(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-26' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_26 text;
WITH recent_27 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_28 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 364
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000029';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 30 */
CREATE INDEX CONCURRENTLY idx_orders_31 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-32', 'reprice', '{"sku":"SKU-000032","delta":32}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_33(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-07' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_35 text;
WITH recent_36 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_37 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 481
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000038';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 39 */
CREATE INDEX CONCURRENTLY idx_orders_40 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-41', 'reprice', '{"sku":"SKU-000041","delta":1}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_42(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-16' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_44 text;
WITH recent_45 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_46 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 598
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000047';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 48 */
CREATE INDEX CONCURRENTLY idx_orders_49 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-50', 'reprice', '{"sku":"SKU-000050","delta":10}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_51(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-25' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_53 text;
WITH recent_54 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_55 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 715
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000056';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 57 */
CREATE INDEX CONCURRENTLY idx_orders_58 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-59', 'reprice', '{"sku":"SKU-000059","delta":19}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_60(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-06' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_62 text;
WITH recent_63 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_64 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 832
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000065';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 66 */
CREATE INDEX CONCURRENTLY idx_orders_67 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-68', 'reprice', '{"sku":"SKU-000068","delta":28}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_69(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-15' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_71 text;
WITH recent_72 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_73 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 949
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000074';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 75 */
CREATE INDEX CONCURRENTLY idx_orders_76 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-77', 'reprice', '{"sku":"SKU-000077","delta":37}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_78(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-24' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_80 text;
WITH recent_81 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_82 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 1066
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000083';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 84 */
CREATE INDEX CONCURRENTLY idx_orders_85 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-86', 'reprice', '{"sku":"SKU-000086","delta":6}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_87(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-05' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_89 text;
WITH recent_90 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_91 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 1183
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000092';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 93 */
CREATE INDEX CONCURRENTLY idx_orders_94 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-95', 'reprice', '{"sku":"SKU-000095","delta":15}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_96(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-14' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_98 text;
WITH recent_99 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_100 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 1300
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000101';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 102 */
CREATE INDEX CONCURRENTLY idx_orders_103 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-104', 'reprice', '{"sku":"SKU-000104","delta":24}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_105(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-23' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_107 text;
WITH recent_108 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_109 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 1417
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000110';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 111 */
CREATE INDEX CONCURRENTLY idx_orders_112 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-113', 'reprice', '{"sku":"SKU-000113","delta":33}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_114(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-04' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_116 text;
WITH recent_117 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_118 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 1534
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000119';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 120 */
CREATE INDEX CONCURRENTLY idx_orders_121 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-122', 'reprice', '{"sku":"SKU-000122","delta":2}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_123(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-13' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_125 text;
WITH recent_126 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_127 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 1651
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000128';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 129 */
CREATE INDEX CONCURRENTLY idx_orders_130 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-131', 'reprice', '{"sku":"SKU-000131","delta":11}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_132(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-22' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_134 text;
WITH recent_135 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_136 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 1768
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000137';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 138 */
CREATE INDEX CONCURRENTLY idx_orders_139 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-140', 'reprice', '{"sku":"SKU-000140","delta":20}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_141(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-03' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_143 text;
WITH recent_144 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_145 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 1885
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000146';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 147 */
CREATE INDEX CONCURRENTLY idx_orders_148 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-149', 'reprice', '{"sku":"SKU-000149","delta":29}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_150(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-12' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_152 text;
WITH recent_153 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_154 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 2002
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000155';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 156 */
CREATE INDEX CONCURRENTLY idx_orders_157 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-158', 'reprice', '{"sku":"SKU-000158","delta":38}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_159(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-21' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_161 text;
WITH recent_162 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_163 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 2119
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000164';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 165 */
CREATE INDEX CONCURRENTLY idx_orders_166 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-167', 'reprice', '{"sku":"SKU-000167","delta":7}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_168(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-02' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_170 text;
WITH recent_171 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_172 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 2236
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000173';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 174 */
CREATE INDEX CONCURRENTLY idx_orders_175 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-176', 'reprice', '{"sku":"SKU-000176","delta":16}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_177(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-11' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_179 text;
WITH recent_180 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_181 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 2353
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000182';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 183 */
CREATE INDEX CONCURRENTLY idx_orders_184 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-185', 'reprice', '{"sku":"SKU-000185","delta":25}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_186(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-20' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_188 text;
WITH recent_189 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_190 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 2470
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000191';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 192 */
CREATE INDEX CONCURRENTLY idx_orders_193 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-194', 'reprice', '{"sku":"SKU-000194","delta":34}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_195(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-01' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_197 text;
WITH recent_198 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_199 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 2587
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000200';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 201 */
CREATE INDEX CONCURRENTLY idx_orders_202 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-203', 'reprice', '{"sku":"SKU-000203","delta":3}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_204(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-10' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_206 text;
WITH recent_207 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_208 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 2704
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000209';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 210 */
CREATE INDEX CONCURRENTLY idx_orders_211 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-212', 'reprice', '{"sku":"SKU-000212","delta":12}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_213(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-19' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_215 text;
WITH recent_216 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_217 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 2821
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000218';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 219 */
CREATE INDEX CONCURRENTLY idx_orders_220 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-221', 'reprice', '{"sku":"SKU-000221","delta":21}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_222(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-28' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_224 text;
WITH recent_225 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_226 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 2938
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000227';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 228 */
CREATE INDEX CONCURRENTLY idx_orders_229 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-230', 'reprice', '{"sku":"SKU-000230","delta":30}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_231(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-09' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_233 text;
WITH recent_234 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_235 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 3055
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000236';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 237 */
CREATE INDEX CONCURRENTLY idx_orders_238 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-239', 'reprice', '{"sku":"SKU-000239","delta":39}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_240(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-18' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_242 text;
WITH recent_243 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_244 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 3172
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000245';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 246 */
CREATE INDEX CONCURRENTLY idx_orders_247 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-248', 'reprice', '{"sku":"SKU-000248","delta":8}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_249(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-27' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_251 text;
WITH recent_252 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_253 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 3289
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000254';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 255 */
CREATE INDEX CONCURRENTLY idx_orders_256 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-257', 'reprice', '{"sku":"SKU-000257","delta":17}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_258(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-08' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_260 text;
WITH recent_261 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_262 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 3406
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000263';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 264 */
CREATE INDEX CONCURRENTLY idx_orders_265 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-266', 'reprice', '{"sku":"SKU-000266","delta":26}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_267(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-17' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_269 text;
WITH recent_270 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_271 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 3523
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000272';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 273 */
CREATE INDEX CONCURRENTLY idx_orders_274 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-275', 'reprice', '{"sku":"SKU-000275","delta":35}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_276(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-26' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_278 text;
WITH recent_279 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_280 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 3640
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000281';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 282 */
CREATE INDEX CONCURRENTLY idx_orders_283 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-284', 'reprice', '{"sku":"SKU-000284","delta":4}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_285(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-07' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_287 text;
WITH recent_288 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_289 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 3757
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000290';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 291 */
CREATE INDEX CONCURRENTLY idx_orders_292 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-293', 'reprice', '{"sku":"SKU-000293","delta":13}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_294(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-16' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_296 text;
WITH recent_297 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_298 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 3874
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000299';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 300 */
CREATE INDEX CONCURRENTLY idx_orders_301 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-302', 'reprice', '{"sku":"SKU-000302","delta":22}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_303(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-25' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_305 text;
WITH recent_306 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_307 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 3991
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000308';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 309 */
CREATE INDEX CONCURRENTLY idx_orders_310 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-311', 'reprice', '{"sku":"SKU-000311","delta":31}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_312(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-06' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_314 text;
WITH recent_315 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_316 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 4108
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000317';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 318 */
CREATE INDEX CONCURRENTLY idx_orders_319 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-320', 'reprice', '{"sku":"SKU-000320","delta":0}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_321(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-15' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_323 text;
WITH recent_324 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_325 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 4225
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000326';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 327 */
CREATE INDEX CONCURRENTLY idx_orders_328 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-329', 'reprice', '{"sku":"SKU-000329","delta":9}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_330(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-24' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_332 text;
WITH recent_333 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_334 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 4342
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000335';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 336 */
CREATE INDEX CONCURRENTLY idx_orders_337 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-338', 'reprice', '{"sku":"SKU-000338","delta":18}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_339(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-05' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_341 text;
WITH recent_342 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_343 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 4459
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000344';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 345 */
CREATE INDEX CONCURRENTLY idx_orders_346 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-347', 'reprice', '{"sku":"SKU-000347","delta":27}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_348(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-14' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_350 text;
WITH recent_351 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_352 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 4576
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000353';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 354 */
CREATE INDEX CONCURRENTLY idx_orders_355 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-356', 'reprice', '{"sku":"SKU-000356","delta":36}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_357(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-23' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_359 text;
WITH recent_360 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_361 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 4693
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000362';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 363 */
CREATE INDEX CONCURRENTLY idx_orders_364 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-365', 'reprice', '{"sku":"SKU-000365","delta":5}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_366(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-04' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_368 text;
WITH recent_369 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_370 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 4810
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000371';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 372 */
CREATE INDEX CONCURRENTLY idx_orders_373 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-374', 'reprice', '{"sku":"SKU-000374","delta":14}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_375(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-13' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_377 text;
WITH recent_378 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_379 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 4927
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000380';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 381 */
CREATE INDEX CONCURRENTLY idx_orders_382 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-383', 'reprice', '{"sku":"SKU-000383","delta":23}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_384(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-22' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_386 text;
WITH recent_387 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_388 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 5044
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000389';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 390 */
CREATE INDEX CONCURRENTLY idx_orders_391 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-392', 'reprice', '{"sku":"SKU-000392","delta":32}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_393(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-03' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_395 text;
WITH recent_396 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_397 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 5161
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000398';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 399 */
CREATE INDEX CONCURRENTLY idx_orders_400 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-401', 'reprice', '{"sku":"SKU-000401","delta":1}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_402(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-12' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_404 text;
WITH recent_405 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_406 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 5278
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000407';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 408 */
CREATE INDEX CONCURRENTLY idx_orders_409 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-410', 'reprice', '{"sku":"SKU-000410","delta":10}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_411(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-21' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_413 text;
WITH recent_414 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_415 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 5395
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000416';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 417 */
CREATE INDEX CONCURRENTLY idx_orders_418 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-419', 'reprice', '{"sku":"SKU-000419","delta":19}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_420(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-02' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_422 text;
WITH recent_423 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_424 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 5512
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000425';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 426 */
CREATE INDEX CONCURRENTLY idx_orders_427 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-428', 'reprice', '{"sku":"SKU-000428","delta":28}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_429(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-11' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_431 text;
WITH recent_432 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_433 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 5629
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000434';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 435 */
CREATE INDEX CONCURRENTLY idx_orders_436 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-437', 'reprice', '{"sku":"SKU-000437","delta":37}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_438(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-20' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_440 text;
WITH recent_441 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_442 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 5746
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000443';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 444 */
CREATE INDEX CONCURRENTLY idx_orders_445 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-446', 'reprice', '{"sku":"SKU-000446","delta":6}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_447(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-01' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_449 text;
WITH recent_450 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_451 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 5863
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000452';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 453 */
CREATE INDEX CONCURRENTLY idx_orders_454 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-455', 'reprice', '{"sku":"SKU-000455","delta":15}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_456(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-10' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_458 text;
WITH recent_459 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_460 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 5980
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000461';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 462 */
CREATE INDEX CONCURRENTLY idx_orders_463 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-464', 'reprice', '{"sku":"SKU-000464","delta":24}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_465(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-19' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_467 text;
WITH recent_468 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_469 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 6097
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000470';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 471 */
CREATE INDEX CONCURRENTLY idx_orders_472 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-473', 'reprice', '{"sku":"SKU-000473","delta":33}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_474(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-28' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_476 text;
WITH recent_477 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_478 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 6214
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000479';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 480 */
CREATE INDEX CONCURRENTLY idx_orders_481 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-482', 'reprice', '{"sku":"SKU-000482","delta":2}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_483(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-09' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_485 text;
WITH recent_486 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_487 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 6331
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000488';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 489 */
CREATE INDEX CONCURRENTLY idx_orders_490 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-491', 'reprice', '{"sku":"SKU-000491","delta":11}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_492(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-18' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_494 text;
WITH recent_495 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_496 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 6448
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000497';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 498 */
CREATE INDEX CONCURRENTLY idx_orders_499 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-500', 'reprice', '{"sku":"SKU-000500","delta":20}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_501(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-27' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_503 text;
WITH recent_504 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_505 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 6565
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000506';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 507 */
CREATE INDEX CONCURRENTLY idx_orders_508 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-509', 'reprice', '{"sku":"SKU-000509","delta":29}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_510(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-08' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_512 text;
WITH recent_513 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_514 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 6682
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000515';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 516 */
CREATE INDEX CONCURRENTLY idx_orders_517 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-518', 'reprice', '{"sku":"SKU-000518","delta":38}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_519(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-17' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_521 text;
WITH recent_522 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_523 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 6799
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000524';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 525 */
CREATE INDEX CONCURRENTLY idx_orders_526 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-527', 'reprice', '{"sku":"SKU-000527","delta":7}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_528(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-26' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_530 text;
WITH recent_531 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_532 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 6916
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000533';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 534 */
CREATE INDEX CONCURRENTLY idx_orders_535 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-536', 'reprice', '{"sku":"SKU-000536","delta":16}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_537(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-07' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_539 text;
WITH recent_540 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_541 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 7033
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000542';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 543 */
CREATE INDEX CONCURRENTLY idx_orders_544 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-545', 'reprice', '{"sku":"SKU-000545","delta":25}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_546(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-16' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_548 text;
WITH recent_549 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_550 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 7150
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000551';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 552 */
CREATE INDEX CONCURRENTLY idx_orders_553 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-554', 'reprice', '{"sku":"SKU-000554","delta":34}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_555(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-25' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_557 text;
WITH recent_558 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_559 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 7267
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000560';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 561 */
CREATE INDEX CONCURRENTLY idx_orders_562 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-563', 'reprice', '{"sku":"SKU-000563","delta":3}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_564(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-06' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_566 text;
WITH recent_567 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_568 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 7384
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000569';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 570 */
CREATE INDEX CONCURRENTLY idx_orders_571 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-572', 'reprice', '{"sku":"SKU-000572","delta":12}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_573(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-15' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_575 text;
WITH recent_576 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_577 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 7501
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000578';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 579 */
CREATE INDEX CONCURRENTLY idx_orders_580 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-581', 'reprice', '{"sku":"SKU-000581","delta":21}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_582(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-24' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_584 text;
WITH recent_585 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_586 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 7618
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000587';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 588 */
CREATE INDEX CONCURRENTLY idx_orders_589 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-590', 'reprice', '{"sku":"SKU-000590","delta":30}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_591(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-05' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_593 text;
WITH recent_594 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_595 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 7735
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000596';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 597 */
CREATE INDEX CONCURRENTLY idx_orders_598 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-599', 'reprice', '{"sku":"SKU-000599","delta":39}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_600(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-14' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_602 text;
WITH recent_603 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_604 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 7852
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000605';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 606 */
CREATE INDEX CONCURRENTLY idx_orders_607 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-608', 'reprice', '{"sku":"SKU-000608","delta":8}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_609(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-23' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_611 text;
WITH recent_612 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_613 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 7969
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000614';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 615 */
CREATE INDEX CONCURRENTLY idx_orders_616 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-617', 'reprice', '{"sku":"SKU-000617","delta":17}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_618(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-04' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_620 text;
WITH recent_621 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_622 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 8086
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000623';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 624 */
CREATE INDEX CONCURRENTLY idx_orders_625 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-626', 'reprice', '{"sku":"SKU-000626","delta":26}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_627(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-13' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_629 text;
WITH recent_630 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_631 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 8203
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000632';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 633 */
CREATE INDEX CONCURRENTLY idx_orders_634 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-635', 'reprice', '{"sku":"SKU-000635","delta":35}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_636(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-22' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_638 text;
WITH recent_639 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_640 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 8320
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000641';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 642 */
CREATE INDEX CONCURRENTLY idx_orders_643 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-644', 'reprice', '{"sku":"SKU-000644","delta":4}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_645(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-03' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_647 text;
WITH recent_648 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_649 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 8437
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000650';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 651 */
CREATE INDEX CONCURRENTLY idx_orders_652 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-653', 'reprice', '{"sku":"SKU-000653","delta":13}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_654(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-12' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_656 text;
WITH recent_657 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_658 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 8554
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000659';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 660 */
CREATE INDEX CONCURRENTLY idx_orders_661 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-662', 'reprice', '{"sku":"SKU-000662","delta":22}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_663(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-21' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_665 text;
WITH recent_666 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_667 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 8671
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000668';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 669 */
CREATE INDEX CONCURRENTLY idx_orders_670 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-671', 'reprice', '{"sku":"SKU-000671","delta":31}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_672(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-02' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_674 text;
WITH recent_675 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_676 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 8788
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000677';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 678 */
CREATE INDEX CONCURRENTLY idx_orders_679 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-680', 'reprice', '{"sku":"SKU-000680","delta":0}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_681(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-11' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_683 text;
WITH recent_684 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_685 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 8905
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000686';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 687 */
CREATE INDEX CONCURRENTLY idx_orders_688 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-689', 'reprice', '{"sku":"SKU-000689","delta":9}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_690(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-20' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_692 text;
WITH recent_693 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_694 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 9022
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000695';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 696 */
CREATE INDEX CONCURRENTLY idx_orders_697 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-698', 'reprice', '{"sku":"SKU-000698","delta":18}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_699(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-01' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_701 text;
WITH recent_702 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_703 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 9139
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000704';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 705 */
CREATE INDEX CONCURRENTLY idx_orders_706 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-707', 'reprice', '{"sku":"SKU-000707","delta":27}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_708(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-10' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_710 text;
WITH recent_711 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_712 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 9256
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000713';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 714 */
CREATE INDEX CONCURRENTLY idx_orders_715 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-716', 'reprice', '{"sku":"SKU-000716","delta":36}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_717(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-19' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_719 text;
WITH recent_720 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_721 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 9373
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000722';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 723 */
CREATE INDEX CONCURRENTLY idx_orders_724 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-725', 'reprice', '{"sku":"SKU-000725","delta":5}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_726(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-28' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_728 text;
WITH recent_729 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_730 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 9490
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000731';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 732 */
CREATE INDEX CONCURRENTLY idx_orders_733 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-734', 'reprice', '{"sku":"SKU-000734","delta":14}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_735(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-09' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_737 text;
WITH recent_738 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_739 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 9607
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000740';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 741 */
CREATE INDEX CONCURRENTLY idx_orders_742 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-743', 'reprice', '{"sku":"SKU-000743","delta":23}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_744(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-18' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_746 text;
WITH recent_747 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_748 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 9724
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000749';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 750 */
CREATE INDEX CONCURRENTLY idx_orders_751 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-752', 'reprice', '{"sku":"SKU-000752","delta":32}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_753(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-27' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_755 text;
WITH recent_756 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_757 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 9841
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000758';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 759 */
CREATE INDEX CONCURRENTLY idx_orders_760 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-761', 'reprice', '{"sku":"SKU-000761","delta":1}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_762(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-08' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_764 text;
WITH recent_765 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_766 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 9958
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000767';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 768 */
CREATE INDEX CONCURRENTLY idx_orders_769 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-770', 'reprice', '{"sku":"SKU-000770","delta":10}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_771(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-17' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_773 text;
WITH recent_774 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_775 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 10075
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000776';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 777 */
CREATE INDEX CONCURRENTLY idx_orders_778 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-779', 'reprice', '{"sku":"SKU-000779","delta":19}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_780(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-26' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_782 text;
WITH recent_783 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_784 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 10192
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000785';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 786 */
CREATE INDEX CONCURRENTLY idx_orders_787 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-788', 'reprice', '{"sku":"SKU-000788","delta":28}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_789(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-07' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_791 text;
WITH recent_792 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_793 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 10309
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000794';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 795 */
CREATE INDEX CONCURRENTLY idx_orders_796 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-797', 'reprice', '{"sku":"SKU-000797","delta":37}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_798(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-16' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_800 text;
WITH recent_801 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_802 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 10426
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000803';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 804 */
CREATE INDEX CONCURRENTLY idx_orders_805 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-806', 'reprice', '{"sku":"SKU-000806","delta":6}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_807(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-25' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_809 text;
WITH recent_810 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_811 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 10543
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000812';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 813 */
CREATE INDEX CONCURRENTLY idx_orders_814 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-815', 'reprice', '{"sku":"SKU-000815","delta":15}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_816(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-06' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_818 text;
WITH recent_819 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_820 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 10660
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000821';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 822 */
CREATE INDEX CONCURRENTLY idx_orders_823 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-824', 'reprice', '{"sku":"SKU-000824","delta":24}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_825(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-15' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_827 text;
WITH recent_828 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_829 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 10777
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000830';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 831 */
CREATE INDEX CONCURRENTLY idx_orders_832 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-833', 'reprice', '{"sku":"SKU-000833","delta":33}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_834(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-24' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_836 text;
WITH recent_837 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_838 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 10894
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000839';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 840 */
CREATE INDEX CONCURRENTLY idx_orders_841 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-842', 'reprice', '{"sku":"SKU-000842","delta":2}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_843(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-05' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_845 text;
WITH recent_846 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_847 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 11011
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000848';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 849 */
CREATE INDEX CONCURRENTLY idx_orders_850 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-851', 'reprice', '{"sku":"SKU-000851","delta":11}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_852(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-14' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_854 text;
WITH recent_855 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_856 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 11128
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000857';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 858 */
CREATE INDEX CONCURRENTLY idx_orders_859 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-860', 'reprice', '{"sku":"SKU-000860","delta":20}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_861(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-23' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_863 text;
WITH recent_864 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_865 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 11245
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000866';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 867 */
CREATE INDEX CONCURRENTLY idx_orders_868 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-869', 'reprice', '{"sku":"SKU-000869","delta":29}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_870(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-04' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_872 text;
WITH recent_873 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_874 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 11362
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000875';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 876 */
CREATE INDEX CONCURRENTLY idx_orders_877 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-878', 'reprice', '{"sku":"SKU-000878","delta":38}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_879(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-13' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_881 text;
WITH recent_882 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_883 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 11479
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000884';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 885 */
CREATE INDEX CONCURRENTLY idx_orders_886 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-887', 'reprice', '{"sku":"SKU-000887","delta":7}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_888(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-22' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_890 text;
WITH recent_891 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_892 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 11596
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000893';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 894 */
CREATE INDEX CONCURRENTLY idx_orders_895 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-896', 'reprice', '{"sku":"SKU-000896","delta":16}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_897(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-03' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_899 text;
WITH recent_900 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_901 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 11713
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000902';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 903 */
CREATE INDEX CONCURRENTLY idx_orders_904 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-905', 'reprice', '{"sku":"SKU-000905","delta":25}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_906(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-12' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_908 text;
WITH recent_909 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_910 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 11830
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000911';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 912 */
CREATE INDEX CONCURRENTLY idx_orders_913 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-914', 'reprice', '{"sku":"SKU-000914","delta":34}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_915(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-21' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_917 text;
WITH recent_918 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_919 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 11947
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000920';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 921 */
CREATE INDEX CONCURRENTLY idx_orders_922 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-923', 'reprice', '{"sku":"SKU-000923","delta":3}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_924(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-02' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_926 text;
WITH recent_927 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_928 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 12064
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000929';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 930 */
CREATE INDEX CONCURRENTLY idx_orders_931 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-932', 'reprice', '{"sku":"SKU-000932","delta":12}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_933(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-11' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_935 text;
WITH recent_936 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_937 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 12181
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000938';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 939 */
CREATE INDEX CONCURRENTLY idx_orders_940 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-941', 'reprice', '{"sku":"SKU-000941","delta":21}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_942(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-20' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_944 text;
WITH recent_945 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_946 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 12298
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000947';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 948 */
CREATE INDEX CONCURRENTLY idx_orders_949 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-950', 'reprice', '{"sku":"SKU-000950","delta":30}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_951(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-01' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_953 text;
WITH recent_954 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_955 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 12415
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000956';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 957 */
CREATE INDEX CONCURRENTLY idx_orders_958 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-959', 'reprice', '{"sku":"SKU-000959","delta":39}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_960(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-10' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_962 text;
WITH recent_963 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_964 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 12532
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000965';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 966 */
CREATE INDEX CONCURRENTLY idx_orders_967 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-968', 'reprice', '{"sku":"SKU-000968","delta":8}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_969(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-19' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_971 text;
WITH recent_972 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_973 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 12649
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000974';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 975 */
CREATE INDEX CONCURRENTLY idx_orders_976 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-977', 'reprice', '{"sku":"SKU-000977","delta":17}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_978(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-28' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_980 text;
WITH recent_981 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_982 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 12766
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000983';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 984 */
CREATE INDEX CONCURRENTLY idx_orders_985 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-986', 'reprice', '{"sku":"SKU-000986","delta":26}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_987(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-09' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_989 text;
WITH recent_990 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_991 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 12883
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-000992';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 993 */
CREATE INDEX CONCURRENTLY idx_orders_994 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-995', 'reprice', '{"sku":"SKU-000995","delta":35}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_996(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-18' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_998 text;
WITH recent_999 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1000 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 13000
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001001';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1002 */
CREATE INDEX CONCURRENTLY idx_orders_1003 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1004', 'reprice', '{"sku":"SKU-001004","delta":4}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1005(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-27' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1007 text;
WITH recent_1008 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1009 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 13117
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001010';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1011 */
CREATE INDEX CONCURRENTLY idx_orders_1012 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1013', 'reprice', '{"sku":"SKU-001013","delta":13}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1014(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-08' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1016 text;
WITH recent_1017 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1018 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 13234
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001019';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1020 */
CREATE INDEX CONCURRENTLY idx_orders_1021 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1022', 'reprice', '{"sku":"SKU-001022","delta":22}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1023(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-17' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1025 text;
WITH recent_1026 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1027 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 13351
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001028';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1029 */
CREATE INDEX CONCURRENTLY idx_orders_1030 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1031', 'reprice', '{"sku":"SKU-001031","delta":31}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1032(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-26' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1034 text;
WITH recent_1035 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1036 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 13468
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001037';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1038 */
CREATE INDEX CONCURRENTLY idx_orders_1039 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1040', 'reprice', '{"sku":"SKU-001040","delta":0}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1041(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-07' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1043 text;
WITH recent_1044 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1045 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 13585
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001046';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1047 */
CREATE INDEX CONCURRENTLY idx_orders_1048 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1049', 'reprice', '{"sku":"SKU-001049","delta":9}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1050(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-16' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1052 text;
WITH recent_1053 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1054 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 13702
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001055';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1056 */
CREATE INDEX CONCURRENTLY idx_orders_1057 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1058', 'reprice', '{"sku":"SKU-001058","delta":18}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1059(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-25' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1061 text;
WITH recent_1062 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1063 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 13819
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001064';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1065 */
CREATE INDEX CONCURRENTLY idx_orders_1066 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1067', 'reprice', '{"sku":"SKU-001067","delta":27}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1068(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-06' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1070 text;
WITH recent_1071 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1072 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 13936
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001073';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1074 */
CREATE INDEX CONCURRENTLY idx_orders_1075 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1076', 'reprice', '{"sku":"SKU-001076","delta":36}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1077(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-15' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1079 text;
WITH recent_1080 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1081 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 14053
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001082';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1083 */
CREATE INDEX CONCURRENTLY idx_orders_1084 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1085', 'reprice', '{"sku":"SKU-001085","delta":5}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1086(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-24' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1088 text;
WITH recent_1089 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1090 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 14170
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001091';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1092 */
CREATE INDEX CONCURRENTLY idx_orders_1093 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1094', 'reprice', '{"sku":"SKU-001094","delta":14}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1095(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-05' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1097 text;
WITH recent_1098 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1099 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 14287
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001100';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1101 */
CREATE INDEX CONCURRENTLY idx_orders_1102 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1103', 'reprice', '{"sku":"SKU-001103","delta":23}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1104(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-14' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1106 text;
WITH recent_1107 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1108 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 14404
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001109';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1110 */
CREATE INDEX CONCURRENTLY idx_orders_1111 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1112', 'reprice', '{"sku":"SKU-001112","delta":32}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1113(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-23' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1115 text;
WITH recent_1116 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1117 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 14521
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001118';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1119 */
CREATE INDEX CONCURRENTLY idx_orders_1120 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1121', 'reprice', '{"sku":"SKU-001121","delta":1}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1122(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-04' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1124 text;
WITH recent_1125 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1126 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 14638
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001127';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1128 */
CREATE INDEX CONCURRENTLY idx_orders_1129 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1130', 'reprice', '{"sku":"SKU-001130","delta":10}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1131(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-13' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1133 text;
WITH recent_1134 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1135 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 14755
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001136';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1137 */
CREATE INDEX CONCURRENTLY idx_orders_1138 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1139', 'reprice', '{"sku":"SKU-001139","delta":19}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1140(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-22' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1142 text;
WITH recent_1143 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1144 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 14872
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001145';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1146 */
CREATE INDEX CONCURRENTLY idx_orders_1147 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1148', 'reprice', '{"sku":"SKU-001148","delta":28}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1149(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-03' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1151 text;
WITH recent_1152 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1153 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 14989
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001154';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1155 */
CREATE INDEX CONCURRENTLY idx_orders_1156 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1157', 'reprice', '{"sku":"SKU-001157","delta":37}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1158(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-12' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1160 text;
WITH recent_1161 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1162 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 15106
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001163';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1164 */
CREATE INDEX CONCURRENTLY idx_orders_1165 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1166', 'reprice', '{"sku":"SKU-001166","delta":6}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1167(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-21' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1169 text;
WITH recent_1170 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1171 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 15223
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001172';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1173 */
CREATE INDEX CONCURRENTLY idx_orders_1174 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1175', 'reprice', '{"sku":"SKU-001175","delta":15}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1176(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-02' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1178 text;
WITH recent_1179 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1180 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 15340
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001181';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1182 */
CREATE INDEX CONCURRENTLY idx_orders_1183 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1184', 'reprice', '{"sku":"SKU-001184","delta":24}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1185(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-11' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1187 text;
WITH recent_1188 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1189 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 15457
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001190';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1191 */
CREATE INDEX CONCURRENTLY idx_orders_1192 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1193', 'reprice', '{"sku":"SKU-001193","delta":33}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1194(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-20' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1196 text;
WITH recent_1197 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1198 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 15574
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001199';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1200 */
CREATE INDEX CONCURRENTLY idx_orders_1201 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1202', 'reprice', '{"sku":"SKU-001202","delta":2}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1203(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-01' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1205 text;
WITH recent_1206 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1207 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 15691
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001208';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1209 */
CREATE INDEX CONCURRENTLY idx_orders_1210 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1211', 'reprice', '{"sku":"SKU-001211","delta":11}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1212(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-10' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1214 text;
WITH recent_1215 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1216 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 15808
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001217';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1218 */
CREATE INDEX CONCURRENTLY idx_orders_1219 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1220', 'reprice', '{"sku":"SKU-001220","delta":20}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1221(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-19' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1223 text;
WITH recent_1224 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1225 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 15925
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001226';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1227 */
CREATE INDEX CONCURRENTLY idx_orders_1228 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1229', 'reprice', '{"sku":"SKU-001229","delta":29}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1230(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-28' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1232 text;
WITH recent_1233 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1234 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 16042
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001235';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1236 */
CREATE INDEX CONCURRENTLY idx_orders_1237 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1238', 'reprice', '{"sku":"SKU-001238","delta":38}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1239(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-09' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1241 text;
WITH recent_1242 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1243 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 16159
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001244';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1245 */
CREATE INDEX CONCURRENTLY idx_orders_1246 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1247', 'reprice', '{"sku":"SKU-001247","delta":7}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1248(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-18' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1250 text;
WITH recent_1251 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1252 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 16276
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001253';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1254 */
CREATE INDEX CONCURRENTLY idx_orders_1255 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1256', 'reprice', '{"sku":"SKU-001256","delta":16}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1257(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-27' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1259 text;
WITH recent_1260 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1261 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 16393
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001262';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1263 */
CREATE INDEX CONCURRENTLY idx_orders_1264 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1265', 'reprice', '{"sku":"SKU-001265","delta":25}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1266(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-08' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1268 text;
WITH recent_1269 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1270 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 16510
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001271';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1272 */
CREATE INDEX CONCURRENTLY idx_orders_1273 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1274', 'reprice', '{"sku":"SKU-001274","delta":34}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1275(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-17' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1277 text;
WITH recent_1278 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1279 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 16627
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001280';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1281 */
CREATE INDEX CONCURRENTLY idx_orders_1282 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1283', 'reprice', '{"sku":"SKU-001283","delta":3}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1284(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-26' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1286 text;
WITH recent_1287 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1288 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 16744
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001289';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1290 */
CREATE INDEX CONCURRENTLY idx_orders_1291 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1292', 'reprice', '{"sku":"SKU-001292","delta":12}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1293(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-07' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1295 text;
WITH recent_1296 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1297 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 16861
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001298';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1299 */
CREATE INDEX CONCURRENTLY idx_orders_1300 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1301', 'reprice', '{"sku":"SKU-001301","delta":21}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1302(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-16' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1304 text;
WITH recent_1305 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1306 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 16978
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001307';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1308 */
CREATE INDEX CONCURRENTLY idx_orders_1309 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1310', 'reprice', '{"sku":"SKU-001310","delta":30}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1311(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-25' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1313 text;
WITH recent_1314 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1315 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 17095
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001316';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1317 */
CREATE INDEX CONCURRENTLY idx_orders_1318 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1319', 'reprice', '{"sku":"SKU-001319","delta":39}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1320(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-06' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1322 text;
WITH recent_1323 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1324 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 17212
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001325';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1326 */
CREATE INDEX CONCURRENTLY idx_orders_1327 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1328', 'reprice', '{"sku":"SKU-001328","delta":8}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1329(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-15' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1331 text;
WITH recent_1332 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1333 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 17329
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001334';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1335 */
CREATE INDEX CONCURRENTLY idx_orders_1336 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1337', 'reprice', '{"sku":"SKU-001337","delta":17}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1338(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-24' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1340 text;
WITH recent_1341 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1342 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 17446
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001343';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1344 */
CREATE INDEX CONCURRENTLY idx_orders_1345 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1346', 'reprice', '{"sku":"SKU-001346","delta":26}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1347(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-05' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1349 text;
WITH recent_1350 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1351 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 17563
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001352';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1353 */
CREATE INDEX CONCURRENTLY idx_orders_1354 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1355', 'reprice', '{"sku":"SKU-001355","delta":35}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1356(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-14' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1358 text;
WITH recent_1359 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1360 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 17680
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001361';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1362 */
CREATE INDEX CONCURRENTLY idx_orders_1363 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1364', 'reprice', '{"sku":"SKU-001364","delta":4}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1365(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-23' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1367 text;
WITH recent_1368 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1369 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 17797
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001370';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1371 */
CREATE INDEX CONCURRENTLY idx_orders_1372 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1373', 'reprice', '{"sku":"SKU-001373","delta":13}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1374(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-04' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1376 text;
WITH recent_1377 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1378 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 17914
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001379';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1380 */
CREATE INDEX CONCURRENTLY idx_orders_1381 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1382', 'reprice', '{"sku":"SKU-001382","delta":22}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1383(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-13' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1385 text;
WITH recent_1386 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1387 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 18031
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001388';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1389 */
CREATE INDEX CONCURRENTLY idx_orders_1390 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1391', 'reprice', '{"sku":"SKU-001391","delta":31}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1392(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-22' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1394 text;
WITH recent_1395 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1396 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 18148
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001397';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1398 */
CREATE INDEX CONCURRENTLY idx_orders_1399 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1400', 'reprice', '{"sku":"SKU-001400","delta":0}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1401(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-03' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1403 text;
WITH recent_1404 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1405 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 18265
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001406';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1407 */
CREATE INDEX CONCURRENTLY idx_orders_1408 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1409', 'reprice', '{"sku":"SKU-001409","delta":9}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1410(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-12' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1412 text;
WITH recent_1413 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '63 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1414 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 18382
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001415';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1416 */
CREATE INDEX CONCURRENTLY idx_orders_1417 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1418', 'reprice', '{"sku":"SKU-001418","delta":18}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1419(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-21' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1421 text;
WITH recent_1422 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '72 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1423 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 18499
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001424';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1425 */
CREATE INDEX CONCURRENTLY idx_orders_1426 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1427', 'reprice', '{"sku":"SKU-001427","delta":27}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1428(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-02' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1430 text;
WITH recent_1431 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '81 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1432 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 18616
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001433';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1434 */
CREATE INDEX CONCURRENTLY idx_orders_1435 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1436', 'reprice', '{"sku":"SKU-001436","delta":36}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1437(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-11' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1439 text;
WITH recent_1440 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '0 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1441 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 18733
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001442';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1443 */
CREATE INDEX CONCURRENTLY idx_orders_1444 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1445', 'reprice', '{"sku":"SKU-001445","delta":5}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1446(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-20' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1448 text;
WITH recent_1449 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '9 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1450 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 18850
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001451';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1452 */
CREATE INDEX CONCURRENTLY idx_orders_1453 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1454', 'reprice', '{"sku":"SKU-001454","delta":14}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1455(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-01' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1457 text;
WITH recent_1458 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '18 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1459 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 18967
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001460';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1461 */
CREATE INDEX CONCURRENTLY idx_orders_1462 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1463', 'reprice', '{"sku":"SKU-001463","delta":23}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1464(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-10' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1466 text;
WITH recent_1467 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '27 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1468 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 19084
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001469';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1470 */
CREATE INDEX CONCURRENTLY idx_orders_1471 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1472', 'reprice', '{"sku":"SKU-001472","delta":32}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1473(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-19' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1475 text;
WITH recent_1476 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '36 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1477 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 19201
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001478';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1479 */
CREATE INDEX CONCURRENTLY idx_orders_1480 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1481', 'reprice', '{"sku":"SKU-001481","delta":1}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1482(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-28' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1484 text;
WITH recent_1485 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '45 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1486 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 19318
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001487';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1488 */
CREATE INDEX CONCURRENTLY idx_orders_1489 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1490', 'reprice', '{"sku":"SKU-001490","delta":10}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1491(p_id bigint) RETURNS numeric AS $$
BEGIN
    RETURN (SELECT coalesce(sum(quantity * unit_price), 0) FROM order_lines WHERE order_id = p_id);
END;
$$ LANGUAGE plpgsql STABLE;
SELECT date_trunc('day', placed_at) AS d, count(*) FILTER (WHERE is_priority) AS urgent,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY total) AS p95
FROM orders WHERE placed_at > '2024-01-09' GROUP BY 1 ORDER BY 1;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS carrier_ref_1493 text;
WITH recent_1494 AS (
    SELECT o.id, o.customer_id, o.total, o.placed_at,
           row_number() OVER (PARTITION BY o.customer_id ORDER BY o.placed_at DESC) AS rn
    FROM public.orders o
    WHERE o.placed_at >= now() - interval '54 days'
      AND o.status IN ('paid', 'shipped', 'delivered')
)
SELECT c.name, sum(r.total) AS lifetime_value
FROM recent_1495 r JOIN customers c ON c.id = r.customer_id
WHERE r.rn = 1 GROUP BY c.name HAVING sum(r.total) > 19435
ORDER BY lifetime_value DESC LIMIT 100;
-- note: the literal below deliberately contains /* and -- to trip a naive lexer
UPDATE inventory SET note = 'contains /* not a comment */ and -- not one either'
WHERE sku = 'SKU-001496';
/* block comment spanning
   several lines to exercise
   cross-line lexer state 1497 */
CREATE INDEX CONCURRENTLY idx_orders_1498 ON orders (customer_id, placed_at DESC)
    WHERE status <> 'cancelled';
INSERT INTO audit_log (actor, action, payload, at) VALUES
    ('svc-1499', 'reprice', '{"sku":"SKU-001499","delta":19}'::jsonb, now());
CREATE OR REPLACE FUNCTION recompute_1500(p_id bigint) RETURNS numeric AS $$
BEGIN
