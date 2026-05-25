-- Add a minimum request-count guard to noisy rate-based alert rules.
UPDATE ops_alert_rules
SET
    filters = jsonb_set(coalesce(filters, '{}'::jsonb), '{min_request_count}', '20'::jsonb, true),
    updated_at = NOW()
WHERE name IN ('错误率过高', '成功率过低');

UPDATE ops_alert_rules
SET
    filters = jsonb_set(coalesce(filters, '{}'::jsonb), '{min_request_count}', '10'::jsonb, true),
    updated_at = NOW()
WHERE name = '错误率极高';
