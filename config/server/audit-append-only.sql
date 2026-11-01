-- ohc_audit_events is append-only. Oh CRUD owns its tables, so revoking UPDATE and
-- DELETE from its own role would not stop it; this trigger does, for every role
-- short of one that drops the trigger itself.
CREATE OR REPLACE FUNCTION ohc_audit_events_append_only()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'ohc_audit_events is append-only: % is not allowed', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$;

DROP TRIGGER IF EXISTS ohc_audit_events_append_only ON ohc_audit_events;
CREATE TRIGGER ohc_audit_events_append_only
    BEFORE UPDATE OR DELETE ON ohc_audit_events
    FOR EACH ROW EXECUTE FUNCTION ohc_audit_events_append_only();

-- TRUNCATE skips row triggers, so it needs its own
DROP TRIGGER IF EXISTS ohc_audit_events_no_truncate ON ohc_audit_events;
CREATE TRIGGER ohc_audit_events_no_truncate
    BEFORE TRUNCATE ON ohc_audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION ohc_audit_events_append_only();
